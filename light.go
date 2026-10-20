package bondbridge

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/avanha/pmaas-plugin-bond-bridge/internal/bond"
	"github.com/avanha/pmaas-plugin-bond-bridge/internal/dimmer"
	spicommon "github.com/avanha/pmaas-spi/common"
	"github.com/avanha/pmaas-spi/device"
	"github.com/avanha/pmaas-spi/events"
)

// maxQueued is how many brightness changes are kept waiting while one is being made. More than that are a mistake, or a held key.
const maxQueued = 5

// stopTries is how often the stop that ends a dimming is tried: a dimmer left running goes on for good.
const stopTries = 3

// lightActions is how one of a device's lights is controlled.
type lightActions struct {
	key     string // the state variable that says whether it is on
	on, off string
	dim     string // starts the dimmer, which sweeps up and down until stopped; empty if it can't be dimmed
	stop    string
	state   func(bond.State) *int
}

// chooseLight is the light of the device to use, if it has one.
func chooseLight(info bond.Device, choice LightChoice) (lightActions, bool) {
	// Whichever light of the fan is used, the bridge reports whether it is on in "light". Its "down_light" and "up_light" say which
	// fixtures the fan has, and do not change with the commands.
	light := func(s bond.State) *int { return s.Light }

	down := lightActions{"light", "TurnDownLightOn", "TurnDownLightOff", "StartDownLightDimmer", "Stop", light}
	up := lightActions{"light", "TurnUpLightOn", "TurnUpLightOff", "StartUpLightDimmer", "Stop", light}
	plain := lightActions{"light", "TurnLightOn", "TurnLightOff", "StartDimmer", "Stop", light}

	has := func(l lightActions) bool { return info.Has(l.on) && info.Has(l.off) }

	switch choice {
	case LightNone:
		return lightActions{}, false
	case LightDown:
		return down, has(down)
	case LightUp:
		return up, has(up)
	}

	for _, candidate := range []lightActions{down, plain, up} {
		if has(candidate) {
			return candidate, true
		}
	}

	return lightActions{}, false
}

// light is a device's light. Owned by the plugin goroutine.
type light struct {
	dev  *bondDevice
	id   string
	acts lightActions
	data device.Light
	stub *lightStub

	// model is the estimate of the brightness, or nil if the light can't be dimmed.
	model *dimmer.Model

	// busy is that the dimmer is being run, or about to be. queue are the changes asked for meanwhile, each worked out from where
	// the dimmer is when its turn comes.
	busy  bool
	queue []func() []dimmer.Move
}

var _ device.ILight = (*light)(nil)

func newLight(dev *bondDevice, acts lightActions) *light {
	l := &light{dev: dev, acts: acts, stub: &lightStub{}}
	l.data = device.Light{Name: dev.info.Name + " Light", Assumed: true, BrightnessEstimated: true}

	if dev.info.Has(acts.dim) && dev.info.Has(acts.stop) && dev.options.DimRamp > 0 {
		l.model = dimmer.New(dev.options.DimRamp)
		l.model.Alternates = !dev.options.DimKeepsDirection
		l.data.Dimmable = true
	}

	return l
}

func (l *light) entityEvent() events.EntityEvent {
	return events.EntityEvent{Id: l.id, EntityType: device.ILightType, Name: l.data.Name}
}

func (l *light) register() {
	p := l.dev.p
	l.stub.wrapper.Store(&spicommon.ThreadSafeEntityWrapper[device.ILight]{Container: p.container, Entity: l})

	id, err := p.container.RegisterEntity(l.dev.info.ID+"/light", device.ILightType, l.data.Name, func() (any, error) { return l.stub, nil })
	if err != nil {
		p.log.Error("unable to register a light", "name", l.data.Name, "error", err)

		return
	}

	l.id = id

	if handle := p.handleForLight(l.dev.info); handle != nil {
		event := spicommon.StubAvailableEvent[device.ILight]{EntityEvent: l.entityEvent(), EntityStub: l.stub}
		if err := handle.stub.Provide(p.container, event); err != nil {
			p.log.Error("unable to provide the stub", "error", err)
		}
	}
}

func (l *light) close() {
	p := l.dev.p

	if handle := p.handleForLight(l.dev.info); handle != nil {
		handle.stub.Withdraw()
	}

	if l.id != "" {
		if err := p.container.DeregisterEntity(l.id); err != nil {
			p.log.Error("unable to deregister a light", "name", l.data.Name, "error", err)
		}
	}

	l.stub.wrapper.Store(nil)
}

func (p *plugin) handleForLight(info bond.Device) *LightHandle {
	if h, ok := p.config.lights[info.Name]; ok {
		return h
	}

	return p.config.lights[info.ID]
}

func (l *light) GetLightData() device.Light {
	return l.data
}

func (l *light) GetSwitchData() device.Switch {
	d := l.data

	return bondSwitch(d.Name, d.HasData, d.State, d.LastChangeTime, d.LastUpdateTime, d.Connectivity, d.OfflineSince, d.OnlineSince)
}

func (l *light) SetState(state device.SwitchState) error {
	if !state.IsSettable() {
		return fmt.Errorf("unable to set %q to %s: %w", l.data.Name, state, device.ErrInvalidSwitchState)
	}

	if err := l.dev.checkReachable(l.data.Name); err != nil {
		return err
	}

	action := l.acts.off
	if state == device.SwitchStateOn {
		action = l.acts.on
	}

	return l.dev.p.send(l.dev, action, nil)
}

func (l *light) SetBrightness(percent int) error {
	if l.model == nil {
		return fmt.Errorf("unable to dim %q: %w", l.data.Name, device.ErrNotDimmable)
	}

	if percent < dimmer.Min || percent > dimmer.Max {
		return fmt.Errorf("unable to set %q to %d%%: %w", l.data.Name, percent, device.ErrInvalidBrightness)
	}

	if !l.model.Known() && !l.busy {
		return fmt.Errorf("unable to set the brightness of %q: it isn't known where the dimmer is, so calibrate it first", l.data.Name)
	}

	return l.request(func() []dimmer.Move { return l.model.Plan(percent) })
}

func (l *light) StepBrightness(percent int) error {
	if l.model == nil {
		return fmt.Errorf("unable to dim %q: %w", l.data.Name, device.ErrNotDimmable)
	}

	if !l.model.Known() && !l.busy {
		return fmt.Errorf("unable to step the brightness of %q: it isn't known where the dimmer is, so calibrate it first", l.data.Name)
	}

	return l.request(func() []dimmer.Move { return l.model.PlanStep(percent) })
}

// Sync tells the bridge the light is really on or off, when what it believes is wrong. Nothing is transmitted to the light. A fan
// whose light is switched by a toggle can't be turned on by a command the bridge thinks it needn't send.
func (l *light) Sync(on bool) error {
	value := 0
	if on {
		value = 1
	}

	return l.dev.p.correct(l.dev, map[string]int{l.acts.key: value})
}

// Calibrate says where the dimmer is, once someone has seen: at its dimmest (level 1, and the next hold goes up) or its brightest
// (level 100, and it goes down). The estimate drifts with every hold, so it is the way to put it right.
func (l *light) Calibrate(level int) error {
	if l.model == nil {
		return fmt.Errorf("unable to calibrate %q: %w", l.data.Name, device.ErrNotDimmable)
	}

	if l.busy {
		return errors.New("the dimmer is being run: wait for it to finish")
	}

	l.model.Calibrate(level, level <= dimmer.Min)
	l.refreshBrightness()
	l.broadcastState()

	return nil
}

// request has a change in brightness made, now or when the one in progress is done.
func (l *light) request(plan func() []dimmer.Move) error {
	if err := l.dev.checkReachable(l.data.Name); err != nil {
		return err
	}

	if len(l.queue) >= maxQueued {
		return errors.New("too many brightness changes are waiting for the last to finish")
	}

	l.queue = append(l.queue, plan)
	l.pump()

	return nil
}

// pump starts the next change in brightness, if the dimmer is free.
func (l *light) pump() {
	p := l.dev.p

	for !l.busy && len(l.queue) > 0 && !p.stopped {
		plan := l.queue[0]
		l.queue = l.queue[1:]

		moves := plan()
		if len(moves) == 0 {
			continue
		}

		l.busy = true

		if l.data.State == device.SwitchStateOn {
			l.run(moves, 0)

			return
		}

		// The dimmer only works on a light that is on.
		p.exec.submit(func(ctx context.Context) {
			err := p.api.Do(ctx, l.dev.info.ID, l.acts.on, nil)
			p.later(func() {
				if err != nil {
					l.fail("unable to turn the light on to dim it", err)

					return
				}

				l.setOn()
				l.run(moves, 0)
			})
		})

		return
	}
}

// run makes the hold at the index: start the dimmer, wait out the hold, stop it.
func (l *light) run(moves []dimmer.Move, index int) {
	p := l.dev.p

	if index >= len(moves) || p.stopped {
		l.busy = false
		l.refreshBrightness()
		l.broadcastState()
		l.pump()

		return
	}

	move := moves[index]

	accepted := p.exec.submit(func(ctx context.Context) {
		err := l.startDimmer(ctx)
		startedAt := p.now()

		p.later(func() {
			if err != nil {
				l.fail("unable to start the dimmer", err)

				return
			}

			p.log.Debug("dimming", "light", l.data.Name, "expectedUp", move.Up, "hold", move.Hold)

			p.after(move.Hold, func() { p.exec.submitUrgent(func(ctx context.Context) { l.stopAndGo(ctx, moves, index, startedAt) }) })
		})
	})

	if !accepted {
		l.fail("unable to dim", errors.New("the bridge's queue is full or the plugin is stopping"))
	}
}

// startDimmer starts the dimmer, with the command the device obeys if the bridge has two for the action.
func (l *light) startDimmer(ctx context.Context) error {
	p := l.dev.p

	if command, ok := l.dev.info.Commands[l.acts.dim]; ok {
		return p.api.Transmit(ctx, l.dev.info.ID, command)
	}

	return p.api.Do(ctx, l.dev.info.ID, l.acts.dim, nil)
}

// stopAndGo stops the dimmer and carries on with the next hold. Runs on the executor.
func (l *light) stopAndGo(ctx context.Context, moves []dimmer.Move, index int, startedAt time.Time) {
	p := l.dev.p

	var err error

	for range stopTries {
		if err = p.api.Do(ctx, l.dev.info.ID, l.acts.stop, nil); err == nil {
			break
		}
	}

	held := p.now().Sub(startedAt)

	p.later(func() {
		if err != nil {
			// The dimmer may still be running. Say so loudly, and don't pretend to know where it is.
			l.model.Forget()
			l.fail("UNABLE TO STOP THE DIMMER, the light may still be changing", err)

			return
		}

		l.model.Apply(held)
		l.run(moves, index+1)
	})
}

// stopNow is the stop for a dimmer in progress when the plugin stops.
func (l *light) stopNow() func(context.Context) {
	api, id, stop := l.dev.p.api, l.dev.info.ID, l.acts.stop

	return func(ctx context.Context) { _ = api.Do(ctx, id, stop, nil) }
}

// fail gives up on the changes in progress and waiting.
func (l *light) fail(what string, err error) {
	l.dev.p.log.Error(what, "light", l.data.Name, "error", err)

	l.busy, l.queue = false, nil
	l.refreshBrightness()
	l.broadcastState()
}

// setOn notes that the light is on, which the bridge was just told.
func (l *light) setOn() {
	if l.data.State != device.SwitchStateOn {
		l.data.State = device.SwitchStateOn
		l.data.LastChangeTime = l.dev.p.now()
		l.broadcastState()
	}
}

// refreshBrightness puts the estimate in the data.
func (l *light) refreshBrightness() {
	if l.model == nil {
		return
	}

	l.data.HasBrightness = l.model.Known()
	l.data.Brightness = l.model.Level()
}

// apply takes the state the bridge reports. The first establishes the state without being a change.
func (l *light) apply(state bond.State) {
	value := l.acts.state(state)
	if value == nil {
		return
	}

	now := l.dev.p.now()
	old := l.data

	on := device.SwitchStateOff
	if *value != 0 {
		on = device.SwitchStateOn
	}

	l.data.LastUpdateTime = now

	if l.data.State == on && old.HasData {
		return
	}

	if old.HasData {
		l.data.LastChangeTime = now
	}

	l.data.State, l.data.HasData = on, true
	l.refreshBrightness()
	l.broadcastState()

	if old.HasData {
		l.dev.p.broadcast(l.id, device.SwitchStateChangedEvent{EntityEvent: l.entityEvent(), NewState: on, OldState: old.State})
	}
}

func (l *light) broadcastState() {
	if l.id == "" {
		return
	}

	l.dev.p.broadcast(l.id, events.EntityStateChangedEvent{EntityEvent: l.entityEvent(), NewState: l.data})
}
