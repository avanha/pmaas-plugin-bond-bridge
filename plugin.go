package bondbridge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync/atomic"
	"time"

	"github.com/avanha/pmaas-plugin-bond-bridge/data"
	"github.com/avanha/pmaas-plugin-bond-bridge/internal/bond"
	"github.com/avanha/pmaas-plugin-bond-bridge/internal/common"
	"github.com/avanha/pmaas-plugin-bond-bridge/internal/http"
	spi "github.com/avanha/pmaas-spi"
	"github.com/avanha/pmaas-spi/device"
	"github.com/avanha/pmaas-spi/environment"
	"github.com/avanha/pmaas-spi/group"
	"github.com/avanha/pmaas-spi/logging"
)

// rediscoverAfter is how long to wait before looking for the bridge's devices again if it didn't answer.
const rediscoverAfter = 30 * time.Second

type Plugin interface {
	spi.IPMAASPlugin
}

// plugin finds the devices a Bond Bridge controls and makes a fan and a light entity of each that has them. Everything it owns
// belongs to the plugin goroutine; the calls to the bridge are made by the executor and hand their results back to it.
type plugin struct {
	log       *slog.Logger
	config    PluginConfig
	container spi.IPMAASContainer
	api       bond.API
	exec      *executor
	http      *http.Handler

	devices []*bondDevice
	byID    map[string]*bondDevice

	// ids are the devices to ask about when polling, read by the executor.
	ids atomic.Pointer[[]string]

	stopped    bool
	cancelWait func()

	now   func() time.Time
	after func(d time.Duration, f func()) (cancel func())
}

var _ spi.IPMAASPlugin = (*plugin)(nil)

func NewPlugin(config PluginConfig) Plugin {
	return &plugin{config: config, byID: make(map[string]*bondDevice), now: time.Now, http: http.NewHandler()}
}

func (p *plugin) ShortName() string {
	return "bondbridge"
}

func (p *plugin) Init(container spi.IPMAASContainer) {
	p.container = container
	p.log = logging.For(container)
	p.after = func(d time.Duration, f func()) func() {
		timer := time.AfterFunc(d, f)

		return func() { timer.Stop() }
	}

	p.http.Init(container, &entityStoreAdapter{parent: p})
}

func (p *plugin) Start() {
	p.log.Info("starting", "host", p.config.Host)

	p.api = p.config.api
	if p.api == nil {
		p.api = bond.NewClient(p.config.Host, p.config.Token, p.config.Timeout)
	}

	p.exec = newExecutor(p.pollAll, p.config.PollEvery)
	p.exec.start()
	p.discover()

	p.log.Info("started")
}

// later has the function run on the plugin goroutine, from another one.
func (p *plugin) later(f func()) {
	_ = p.container.EnqueueOnPluginGoRoutine(f)
}

// discover asks the bridge what it controls, and tries again later if it doesn't say.
func (p *plugin) discover() {
	p.exec.submit(func(ctx context.Context) {
		devices, err := p.api.Devices(ctx)
		p.later(func() { p.onDiscovered(devices, err) })
	})
}

func (p *plugin) onDiscovered(devices []bond.Device, err error) {
	if p.stopped {
		return
	}

	if err != nil {
		p.log.Warn("unable to list the bridge's devices, will try again", "error", err, "in", rediscoverAfter)

		p.cancelWait = p.after(rediscoverAfter, func() { p.later(p.discover) })

		return
	}

	p.log.Info("found the bridge's devices", "count", len(devices))

	var ids []string

	for _, info := range devices {
		options := p.config.devices[info.ID]
		if byName, ok := p.config.devices[info.Name]; ok {
			options = byName
		}

		if options.Hidden {
			p.log.Debug("leaving a device out", "name", info.Name, "id", info.ID)

			continue
		}

		dev := p.startDevice(info, options)
		if dev == nil {
			continue
		}

		p.devices = append(p.devices, dev)
		p.byID[info.ID] = dev
		ids = append(ids, info.ID)
	}

	p.ids.Store(&ids)

	// What the bridge believes now, instead of waiting for the first poll.
	p.exec.submit(p.pollAllJob)
}

// pollAllJob is pollAll as a job.
func (p *plugin) pollAllJob(ctx context.Context) { p.pollAll(ctx) }

// pollAll asks about each device in turn. Runs on the executor.
func (p *plugin) pollAll(ctx context.Context) {
	ids := p.ids.Load()
	if ids == nil {
		return
	}

	for _, id := range *ids {
		if ctx.Err() != nil {
			return
		}

		state, err := p.api.State(ctx, id)
		p.later(func() { p.onState(id, state, err) })
	}
}

// onState takes what the bridge says a device's state is. A device that can't be asked is taken to be unreachable.
func (p *plugin) onState(id string, state bond.State, err error) {
	dev, ok := p.byID[id]
	if !ok || p.stopped {
		return
	}

	if err != nil {
		if dev.reachable || !dev.heard {
			p.log.Warn("the bridge did not answer", "device", dev.info.Name, "error", err)
		}

		dev.setReachable(false)

		return
	}

	dev.setReachable(true)

	if dev.fan != nil {
		dev.fan.apply(state)
	}

	if dev.light != nil {
		dev.light.apply(state)
	}
}

// send has the bridge do an action, then ask what the state is now. It reports whether the request was accepted for delivery.
func (p *plugin) send(dev *bondDevice, action string, argument *int) error {
	accepted := p.exec.submit(func(ctx context.Context) {
		err := p.api.Do(ctx, dev.info.ID, action, argument)
		if err != nil {
			p.later(func() { p.onSendFailed(dev, action, err) })

			return
		}

		state, err := p.api.State(ctx, dev.info.ID)
		p.later(func() { p.onState(dev.info.ID, state, err) })
	})

	if !accepted {
		return errors.New("the bridge's queue is full or the plugin is stopping")
	}

	return nil
}

// correct tells the bridge what the state of the device really is, then reads back what it believes.
func (p *plugin) correct(dev *bondDevice, state map[string]int) error {
	accepted := p.exec.submit(func(ctx context.Context) {
		err := p.api.Correct(ctx, dev.info.ID, state)
		if err != nil {
			p.later(func() { p.onSendFailed(dev, "correct the state", err) })

			return
		}

		read, err := p.api.State(ctx, dev.info.ID)
		p.later(func() { p.onState(dev.info.ID, read, err) })
	})

	if !accepted {
		return errors.New("the bridge's queue is full or the plugin is stopping")
	}

	return nil
}

func (p *plugin) onSendFailed(dev *bondDevice, action string, err error) {
	p.log.Error("the bridge refused a command", "device", dev.info.Name, "action", action, "error", err)
	dev.setReachable(false)
}

func (p *plugin) broadcast(id string, event any) {
	if err := p.container.BroadcastEvent(id, event); err != nil {
		p.log.Error("unable to broadcast", "type", fmt.Sprintf("%T", event), "error", err)
	}
}

func (p *plugin) Stop() chan func() {
	p.log.Info("stopping")

	p.stopped = true

	if p.cancelWait != nil {
		p.cancelWait()
	}

	// A light that is being dimmed would go on until told to stop.
	var stops []func(context.Context)

	for _, dev := range p.devices {
		if dev.light != nil && dev.light.busy {
			stops = append(stops, dev.light.stopNow())
		}
	}

	for _, dev := range p.devices {
		if dev.fan != nil {
			dev.fan.close()
		}

		if dev.light != nil {
			dev.light.close()
		}
	}

	p.devices = nil
	p.byID = map[string]*bondDevice{}

	if p.exec != nil {
		// The executor may be in a call to the bridge: wait for it off the plugin's goroutine.
		done := p.container.ClosedCallbackChannel()
		exec := p.exec

		go func() {
			exec.stop()

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			for _, stop := range stops {
				stop(ctx)
			}
		}()

		return done
	}

	return p.container.ClosedCallbackChannel()
}

// bondDevice is one device of the bridge, with the entities made of it.
type bondDevice struct {
	p       *plugin
	info    bond.Device
	options DeviceOptions
	fan     *fan
	light   *light

	// heard is whether the bridge has answered about it; reachable is whether it did the last time.
	heard     bool
	reachable bool
}

// startDevice registers the entities of a device. It returns nil if there is nothing of it that is used.
func (p *plugin) startDevice(info bond.Device, options DeviceOptions) *bondDevice {
	dev := &bondDevice{p: p, info: info, options: options}

	if info.Has("SetSpeed") {
		dev.fan = newFan(dev)
	}

	if acts, ok := chooseLight(info, options.Light); ok {
		dev.light = newLight(dev, acts)
	}

	if dev.fan == nil && dev.light == nil {
		p.log.Info("a device has no fan or light, so it is not used", "name", info.Name, "type", info.Type)

		return nil
	}

	if dev.fan != nil {
		dev.fan.register()
	}

	if dev.light != nil {
		dev.light.register()
	}

	return dev
}

// setReachable notes whether the bridge answers for the device, and tells of it if that has changed.
func (d *bondDevice) setReachable(reachable bool) {
	if d.heard && d.reachable == reachable {
		return
	}

	d.heard, d.reachable = true, reachable
	now := d.p.now()

	connectivity := environment.ConnectivityOnline
	if !reachable {
		connectivity = environment.ConnectivityOffline
	}

	apply := func(c *environment.Connectivity, offlineSince *time.Time, onlineSince *time.Time) {
		if reachable {
			*onlineSince = now
		} else {
			*offlineSince = now
		}

		*c = connectivity
	}

	if d.fan != nil {
		apply(&d.fan.data.Connectivity, &d.fan.data.OfflineSince, &d.fan.data.OnlineSince)
		d.fan.broadcastState()
	}

	if d.light != nil {
		apply(&d.light.data.Connectivity, &d.light.data.OfflineSince, &d.light.data.OnlineSince)
		d.light.broadcastState()
	}
}

// checkReachable is the error for a command to a device the bridge isn't answering for.
func (d *bondDevice) checkReachable(what string) error {
	if d.heard && !d.reachable {
		return fmt.Errorf("unable to control %s: the bridge isn't answering for it (since %s)", what, d.p.now().Format(time.TimeOnly))
	}

	return nil
}

// bondSwitch is the on and off part of the data of a fan or a light.
func bondSwitch(name string, hasData bool, state device.SwitchState, changed time.Time, updated time.Time,
	connectivity environment.Connectivity, offlineSince time.Time, onlineSince time.Time) device.Switch {
	return device.Switch{
		Name: name, HasData: hasData, State: state, LastChangeTime: changed, LastUpdateTime: updated,
		Connectivity: connectivity, OfflineSince: offlineSince, OnlineSince: onlineSince,
	}
}

// getDevices is the devices with their fan and light, sorted by name. Runs on the plugin goroutine.
func (p *plugin) getDevices() []data.DeviceView {
	views := make([]data.DeviceView, 0, len(p.devices))

	for _, dev := range p.devices {
		views = append(views, p.deviceView(dev))
	}

	sort.Slice(views, func(i, j int) bool { return views[i].Name < views[j].Name })

	return views
}

// deviceView is a device as the page shows it: its card, with its fan and its light. Runs on the plugin goroutine.
func (p *plugin) deviceView(dev *bondDevice) data.DeviceView {
	view := data.DeviceView{ID: dev.info.ID, Name: dev.info.Name, Location: dev.info.Location, Offline: dev.heard && !dev.reachable}

	if dev.fan != nil {
		speeds := make([]int, dev.fan.data.MaxSpeed)
		for i := range speeds {
			speeds[i] = i + 1
		}

		view.Fan = &data.FanView{Fan: dev.fan.data, Speeds: speeds, On: dev.fan.data.State == device.SwitchStateOn}
		view.OfflineSince = dev.fan.data.OfflineSince
	}

	if dev.light != nil {
		view.Light = &data.LightView{Light: dev.light.data, On: dev.light.data.State == device.SwitchStateOn, Busy: dev.light.busy,
			Step: 10, Presets: []int{10, 25, 50, 75, 100},
			Holds: []int{1, 2, 3, 4, 5}, DirectionKnown: dev.light.model != nil && dev.light.model.Known(), NextUp: dev.light.model != nil && dev.light.model.NextUp()}

		if view.Fan == nil {
			view.OfflineSince = dev.light.data.OfflineSince
		}
	}

	return view
}

// registerView gives an entity of the device the view of its card, for pages that list entities of different plugins. The fan and the
// light of a device are one card, so they share a key and a page that holds both draws it once.
func (p *plugin) registerView(dev *bondDevice, entityId string) {
	err := p.container.RegisterEntityView(entityId, func() (spi.EntityView, error) {
		view := p.deviceView(dev)

		// A device with a fan is a fan on a page that sorts by kind, whichever of its entities is asked about: it is one card.
		category := group.CategoryFans
		if view.Fan == nil {
			category = group.CategoryLights
		}

		return spi.EntityView{Key: "bond/" + dev.info.ID, Category: category, Value: view}, nil
	})
	if err != nil {
		p.log.Error("unable to register the view of an entity", "device", dev.info.Name, "error", err)
	}
}

// command does what a button on the page asked. Runs on the plugin goroutine.
func (p *plugin) command(request common.Command) error {
	dev, ok := p.byID[request.Device]
	if !ok {
		return fmt.Errorf("there is no device %q", request.Device)
	}

	state := device.SwitchStateOff
	if request.Value != 0 {
		state = device.SwitchStateOn
	}

	switch request.Target {
	case "fan":
		if dev.fan == nil {
			return errors.New("the device has no fan")
		}

		switch request.Op {
		case "power":
			return dev.fan.SetState(state)
		case "speed":
			return dev.fan.SetSpeed(request.Value)
		case "sync":
			return dev.fan.Sync(request.Value)
		case "direction":
			direction := device.FanDirectionForward
			if request.Value < 0 {
				direction = device.FanDirectionReverse
			}

			return dev.fan.SetDirection(direction)
		}
	case "light":
		if dev.light == nil {
			return errors.New("the device has no light")
		}

		// A page can't see what is queued, and a press while the dimmer runs only confuses the fan.
		if dev.light.busy && request.Op != "sync" {
			return errors.New("the light is being dimmed: wait for it to finish")
		}

		switch request.Op {
		case "power":
			return dev.light.SetState(state)
		case "brightness":
			return dev.light.SetBrightness(request.Value)
		case "step":
			return dev.light.StepBrightness(request.Value)
		case "sync":
			return dev.light.Sync(request.Value != 0)
		case "calibrate":
			return dev.light.Calibrate(request.Value)
		case "hold":
			if request.Value < 1 || request.Value > maxHoldSeconds {
				return fmt.Errorf("a hold of %d seconds is not between 1 and %d", request.Value, maxHoldSeconds)
			}

			return dev.light.Hold(time.Duration(request.Value) * time.Second)
		case "direction":
			return dev.light.SetNextUp(request.Value > 0)
		}
	}

	return fmt.Errorf("there is no %s %q", request.Target, request.Op)
}
