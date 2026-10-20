package bondbridge

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/avanha/pmaas-plugin-bond-bridge/data"
	"github.com/avanha/pmaas-plugin-bond-bridge/internal/bond"
	"github.com/avanha/pmaas-plugin-bond-bridge/internal/common"
	spi "github.com/avanha/pmaas-spi"
	"github.com/avanha/pmaas-spi/device"
	"github.com/avanha/pmaas-spi/environment"
)

// fakeBridge is a bridge with two ceiling fans like the real ones: three speeds, an up and a down light, and no brightness.
type fakeBridge struct {
	mu      sync.Mutex
	actions []string
	state   map[string]bond.State
	down    bool // the bridge doesn't answer
	failDo  map[string]bool

	// commands are the ids to transmit by action for fan2, as the client works them out.
	commands map[string]string
}

func ptr(i int) *int { return &i }

var fanActions = []string{"DecreaseSpeed", "IncreaseSpeed", "SetDirection", "SetSpeed", "StartDownLightDimmer", "StartUpLightDimmer", "Stop",
	"ToggleDirection", "TurnDownLightOff", "TurnDownLightOn", "TurnLightOff", "TurnLightOn", "TurnOff", "TurnOn", "TurnUpLightOff", "TurnUpLightOn"}

func newFakeBridge() *fakeBridge {
	return &fakeBridge{
		state: map[string]bond.State{
			"fan1": {Power: ptr(0), Speed: ptr(1), Direction: ptr(1), Light: ptr(1), UpLight: ptr(0), DownLight: ptr(1)},
			"fan2": {Power: ptr(1), Speed: ptr(2), Direction: ptr(-1), Light: ptr(0), UpLight: ptr(0), DownLight: ptr(0)},
		},
		failDo: map[string]bool{},
	}
}

func (b *fakeBridge) setDown(down bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.down = down
}

func (b *fakeBridge) Devices(context.Context) ([]bond.Device, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.down {
		return nil, errors.New("no route to host")
	}

	return []bond.Device{
		{ID: "fan1", Name: "Living Room Ceiling Fan", Type: "CF", Location: "Living Room", Actions: fanActions, MaxSpeed: 3},
		{ID: "fan2", Name: "Bedroom Ceiling Fan", Type: "CF", Location: "Master Bedroom", Actions: fanActions, MaxSpeed: 3, Commands: b.commands},
	}, nil
}

func (b *fakeBridge) Transmit(_ context.Context, id string, command string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.down {
		return errors.New("the bridge answered 500")
	}

	b.actions = append(b.actions, id+" transmit "+command)

	return nil
}

func (b *fakeBridge) State(_ context.Context, id string) (bond.State, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.down {
		return bond.State{}, errors.New("no route to host")
	}

	return b.state[id], nil
}

func (b *fakeBridge) Do(_ context.Context, id string, action string, argument *int) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.down || b.failDo[action] {
		return errors.New("the bridge answered 500")
	}

	text := id + " " + action
	if argument != nil {
		text += fmt.Sprintf(" %d", *argument)
	}

	b.actions = append(b.actions, text)

	s := b.state[id]

	switch action {
	case "TurnOn":
		s.Power = ptr(1)
	case "TurnOff":
		s.Power = ptr(0)
	case "SetSpeed":
		s.Power, s.Speed = ptr(1), argument
	case "SetDirection":
		s.Direction = argument
	case "TurnDownLightOn":
		s.Light = ptr(1)
	case "TurnDownLightOff":
		s.Light = ptr(0)
	}

	b.state[id] = s

	return nil
}

func (b *fakeBridge) Correct(_ context.Context, id string, patch map[string]int) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.down {
		return errors.New("no route to host")
	}

	text := id + " correct"
	for _, key := range []string{"power", "speed", "light"} {
		if v, ok := patch[key]; ok {
			text += fmt.Sprintf(" %s=%d", key, v)
		}
	}

	b.actions = append(b.actions, text)

	s := b.state[id]

	if v, ok := patch["light"]; ok {
		s.Light = ptr(v)
	}

	if v, ok := patch["power"]; ok {
		s.Power = ptr(v)
	}

	if v, ok := patch["speed"]; ok {
		s.Speed = ptr(v)
	}

	b.state[id] = s

	return nil
}

func (b *fakeBridge) done() []string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return append([]string(nil), b.actions...)
}

// fakeContainer runs what is enqueued on one goroutine, as the real one does on the plugin's.
type fakeContainer struct {
	spi.IPMAASContainer

	queue chan func()
	quit  chan struct{}

	mu         sync.Mutex
	entities   map[string]reflect.Type
	ids        []string
	broadcasts []any
	removed    []string
}

func newFakeContainer() *fakeContainer {
	c := &fakeContainer{queue: make(chan func(), 1000), quit: make(chan struct{}), entities: map[string]reflect.Type{}}

	go func() {
		for {
			select {
			case f := <-c.queue:
				f()
			case <-c.quit:
				return
			}
		}
	}()

	return c
}

func (c *fakeContainer) ProvideContentFS(fs.FS, string)                                         {}
func (c *fakeContainer) EnableStaticContent(string)                                             {}
func (c *fakeContainer) RegisterEntityRenderer(reflect.Type, spi.EntityRendererFactory)         {}
func (c *fakeContainer) AddRouteWithOptions(string, http.HandlerFunc, *spi.HttpHandlerOptions)  {}
func (c *fakeContainer) AddJsonRoute(string, spi.RequestObjectFactoryFunc, spi.JsonHandlerFunc) {}

func (c *fakeContainer) RegisterEntity(uniqueData string, entityType reflect.Type, _ string, _ spi.EntityStubFactoryFunc) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	id := fmt.Sprintf("bond_%d", len(c.ids)+1)
	c.ids = append(c.ids, id)
	c.entities[uniqueData] = entityType

	return id, nil
}

func (c *fakeContainer) DeregisterEntity(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.removed = append(c.removed, id)

	return nil
}

func (c *fakeContainer) BroadcastEvent(_ string, event any) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.broadcasts = append(c.broadcasts, event)

	return nil
}

func (c *fakeContainer) EnqueueOnPluginGoRoutine(f func()) error {
	select {
	case <-c.quit:
		return errors.New("stopped")
	default:
	}

	select {
	case c.queue <- f:
		return nil
	case <-c.quit:
		return errors.New("stopped")
	}
}

func (c *fakeContainer) ClosedCallbackChannel() chan func() {
	ch := make(chan func())
	close(ch)

	return ch
}

func (c *fakeContainer) switchChanges() []device.SwitchStateChangedEvent {
	c.mu.Lock()
	defer c.mu.Unlock()

	var result []device.SwitchStateChangedEvent

	for _, b := range c.broadcasts {
		if e, ok := b.(device.SwitchStateChangedEvent); ok {
			result = append(result, e)
		}
	}

	return result
}

type fixture struct {
	t         *testing.T
	bridge    *fakeBridge
	container *fakeContainer
	plugin    *plugin

	mu     sync.Mutex
	clock  time.Time
	timers []func()
}

func newFixture(t *testing.T, mutate func(*PluginConfig)) *fixture {
	t.Helper()

	return newFixtureWith(t, newFakeBridge(), mutate)
}

func newFixtureWith(t *testing.T, bridge *fakeBridge, mutate func(*PluginConfig)) *fixture {
	t.Helper()

	f := &fixture{t: t, bridge: bridge, container: newFakeContainer(), clock: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)}
	config := NewPluginConfig()
	config.PollEvery = 0
	config.api = f.bridge

	if mutate != nil {
		mutate(&config)
	}

	f.plugin = NewPlugin(config).(*plugin)
	f.plugin.now = func() time.Time {
		f.mu.Lock()
		defer f.mu.Unlock()

		return f.clock
	}

	f.plugin.Init(f.container)
	f.plugin.after = func(d time.Duration, fn func()) func() {
		f.mu.Lock()
		defer f.mu.Unlock()

		f.timers = append(f.timers, fn)

		return func() {}
	}

	f.plugin.Start()

	t.Cleanup(func() {
		f.onPlugin(func() { f.plugin.Stop() })
		close(f.container.quit)
	})

	return f
}

func (f *fixture) advance(d time.Duration) {
	f.mu.Lock()
	f.clock = f.clock.Add(d)
	f.mu.Unlock()
}

func (f *fixture) timerCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.timers)
}

// fire runs the timers that are waiting, as if their time had come.
func (f *fixture) fire() {
	f.mu.Lock()
	timers := f.timers
	f.timers = nil
	f.mu.Unlock()

	for _, fn := range timers {
		fn()
	}
}

// onPlugin runs the function on the plugin goroutine and waits for it.
func (f *fixture) onPlugin(fn func()) {
	f.t.Helper()

	done := make(chan struct{})
	f.container.queue <- func() { fn(); close(done) }
	<-done
}

// eventually waits for the condition, which is checked on the plugin goroutine.
func (f *fixture) eventually(what string, condition func() bool) {
	f.t.Helper()

	deadline := time.Now().Add(3 * time.Second)

	for time.Now().Before(deadline) {
		ok := false
		f.onPlugin(func() { ok = condition() })

		if ok {
			return
		}

		time.Sleep(5 * time.Millisecond)
	}

	f.t.Fatalf("timed out waiting for %s", what)
}

// waitTimers waits for the plugin to be waiting on n timers.
func (f *fixture) waitTimers(n int) {
	f.t.Helper()

	deadline := time.Now().Add(3 * time.Second)

	for time.Now().Before(deadline) {
		if f.timerCount() >= n {
			return
		}

		time.Sleep(5 * time.Millisecond)
	}

	f.t.Fatalf("timed out waiting for %d timers, have %d", n, f.timerCount())
}

func (f *fixture) waitActions(n int) []string {
	f.t.Helper()

	deadline := time.Now().Add(3 * time.Second)

	for time.Now().Before(deadline) {
		if got := f.bridge.done(); len(got) >= n {
			return got
		}

		time.Sleep(5 * time.Millisecond)
	}

	f.t.Fatalf("timed out waiting for %d actions, have %v", n, f.bridge.done())

	return nil
}

// ready waits for the devices to be found and their state to be read.
func (f *fixture) ready() {
	f.t.Helper()
	f.eventually("the devices", func() bool {
		return len(f.plugin.devices) == 2 && f.plugin.devices[0].fan.data.HasData && f.plugin.devices[1].fan.data.HasData
	})
}

func (f *fixture) dev(id string) *bondDevice {
	var d *bondDevice

	f.onPlugin(func() { d = f.plugin.byID[id] })

	return d
}

func TestStart_FindsTheDevicesAndMakesAFanAndALightOfEach(t *testing.T) {
	f := newFixture(t, nil)
	f.ready()

	f.container.mu.Lock()
	defer f.container.mu.Unlock()

	for _, key := range []string{"fan1/fan", "fan1/light", "fan2/fan", "fan2/light"} {
		want := device.IFanType
		if strings.HasSuffix(key, "light") {
			want = device.ILightType
		}

		if f.container.entities[key] != want {
			t.Errorf("%s is %v", key, f.container.entities[key])
		}
	}
}

func TestState_IsWhatTheBridgeBelieves(t *testing.T) {
	f := newFixture(t, nil)
	f.ready()

	var one, two device.Fan
	var lightOne, lightTwo device.Light

	f.onPlugin(func() {
		one, two = f.plugin.byID["fan1"].fan.data, f.plugin.byID["fan2"].fan.data
		lightOne, lightTwo = f.plugin.byID["fan1"].light.data, f.plugin.byID["fan2"].light.data
	})

	if one.State != device.SwitchStateOff || one.Speed != 1 || one.MaxSpeed != 3 || one.Direction != device.FanDirectionForward || !one.Assumed {
		t.Errorf("%+v", one)
	}

	if two.State != device.SwitchStateOn || two.Speed != 2 || two.Direction != device.FanDirectionReverse {
		t.Errorf("%+v", two)
	}

	// The down light is the one by default: the first fan's is on though its plain "light" is off.
	if lightOne.State != device.SwitchStateOn || lightTwo.State != device.SwitchStateOff || lightOne.Dimmable || lightOne.Name != "Living Room Ceiling Fan Light" {
		t.Errorf("%+v %+v", lightOne, lightTwo)
	}

	if len(f.container.switchChanges()) != 0 {
		t.Error("the first report was taken for a change")
	}
}

func TestOptions_HideADeviceOrItsLightOrPickTheUpLight(t *testing.T) {
	f := newFixture(t, func(c *PluginConfig) {
		c.Options("Bedroom Ceiling Fan", DeviceOptions{Hidden: true})
		c.Options("fan1", DeviceOptions{Light: LightNone})
	})

	f.eventually("the devices", func() bool { return len(f.plugin.devices) == 1 })

	d := f.dev("fan1")
	if d.fan == nil || d.light != nil {
		t.Errorf("fan %v light %v", d.fan, d.light)
	}

	if f.dev("fan2") != nil {
		t.Error("a hidden device was used")
	}

	up := newFixture(t, func(c *PluginConfig) { c.Options("fan1", DeviceOptions{Light: LightUp}) })
	up.ready()

	if up.dev("fan1").light.acts.on != "TurnUpLightOn" {
		t.Error("not the up light")
	}
}

func TestFan_CommandsGoToTheBridgeAndTheStateIsReadBack(t *testing.T) {
	var handle *FanHandle

	f := newFixture(t, func(c *PluginConfig) { handle = c.Fan("Living Room Ceiling Fan") })
	f.ready()

	if err := handle.SetSpeed(3); err != nil {
		t.Fatal(err)
	}

	if err := handle.SetDirection(device.FanDirectionReverse); err != nil {
		t.Fatal(err)
	}

	got := f.waitActions(2)
	if got[0] != "fan1 SetSpeed 3" || got[1] != "fan1 SetDirection -1" {
		t.Errorf("%v", got)
	}

	f.eventually("the state read back", func() bool {
		d := f.plugin.byID["fan1"].fan.data
		return d.State == device.SwitchStateOn && d.Speed == 3 && d.Direction == device.FanDirectionReverse
	})

	// A change of state is announced, to automations.
	if changes := f.container.switchChanges(); len(changes) != 1 || changes[0].NewState != device.SwitchStateOn || changes[0].OldState != device.SwitchStateOff {
		t.Errorf("%+v", changes)
	}

	// And it is a switch, which is how an automation uses it.
	if err := handle.SetState(device.SwitchStateOff); err != nil {
		t.Fatal(err)
	}

	f.waitActions(3)
	f.eventually("off", func() bool { return f.plugin.byID["fan1"].fan.data.State == device.SwitchStateOff })

	if handle.GetSwitchData().State != device.SwitchStateOff || handle.GetFanData().Speed != 3 {
		t.Errorf("%+v", handle.GetFanData())
	}
}

func TestFan_RefusesWhatItCannotDo(t *testing.T) {
	var handle *FanHandle

	f := newFixture(t, func(c *PluginConfig) { handle = c.Fan("fan1") })
	f.ready()

	if err := handle.SetSpeed(4); !errors.Is(err, device.ErrInvalidFanSpeed) {
		t.Errorf("speed 4: %v", err)
	}

	if err := handle.SetSpeed(0); !errors.Is(err, device.ErrInvalidFanSpeed) {
		t.Errorf("speed 0: %v", err)
	}

	if err := handle.SetDirection(device.FanDirectionUnknown); !errors.Is(err, device.ErrInvalidFanDirection) {
		t.Errorf("direction: %v", err)
	}

	if err := handle.SetState(device.SwitchStateUnknown); !errors.Is(err, device.ErrInvalidSwitchState) {
		t.Errorf("state: %v", err)
	}

	if len(f.bridge.done()) != 0 {
		t.Errorf("sent %v", f.bridge.done())
	}
}

func TestHandle_AnswersAsNeverHeardBeforeTheDeviceIsFoundAndRefuses(t *testing.T) {
	config := NewPluginConfig()
	handle := config.Fan("Living Room Ceiling Fan")

	if handle.GetFanData().HasData || handle.SetSpeed(1) == nil || handle.SetState(device.SwitchStateOn) == nil {
		t.Error("a handle with nothing behind it")
	}

	light := config.Light("x")
	if light.GetLightData().HasData || light.SetBrightness(5) == nil || light.StepBrightness(5) == nil {
		t.Error("a light with nothing behind it")
	}

	if config.Fan("Living Room Ceiling Fan") != handle {
		t.Error("asking twice gave another handle")
	}
}

func TestPolling_ABridgeThatStopsAnsweringMakesTheDevicesOfflineAndRefusesCommands(t *testing.T) {
	var handle *FanHandle

	f := newFixture(t, func(c *PluginConfig) { handle = c.Fan("fan1") })
	f.ready()

	f.bridge.setDown(true)
	f.plugin.exec.submit(f.plugin.pollAllJob)

	f.eventually("offline", func() bool {
		return f.plugin.byID["fan1"].fan.data.Connectivity == environment.ConnectivityOffline
	})

	if err := handle.SetSpeed(2); err == nil || !strings.Contains(err.Error(), "isn't answering") {
		t.Errorf("%v", err)
	}

	// The state it had is kept, and back online when it answers.
	if handle.GetFanData().Speed != 1 {
		t.Errorf("%+v", handle.GetFanData())
	}

	f.bridge.setDown(false)
	f.plugin.exec.submit(f.plugin.pollAllJob)

	f.eventually("online", func() bool {
		d := f.plugin.byID["fan1"].fan.data
		return d.Connectivity == environment.ConnectivityOnline && !d.OnlineSince.IsZero()
	})
}

func TestDiscovery_ABridgeThatDoesntAnswerIsAskedAgainLater(t *testing.T) {
	bridge := newFakeBridge()
	bridge.setDown(true)

	f := newFixtureWith(t, bridge, nil)
	f.waitTimers(1)

	bridge.setDown(false)
	f.fire()
	f.ready()
}

const testRamp = 4 * time.Second

// testDelay is how long a hold has to be before the light moves.
const testDelay = 500 * time.Millisecond

func dimmable(c *PluginConfig) {
	c.Options("fan1", DeviceOptions{DimRamp: testRamp, DimDelay: testDelay})
}

func TestLight_WithoutARampItCannotBeDimmed(t *testing.T) {
	var handle *LightHandle

	f := newFixture(t, func(c *PluginConfig) { handle = c.Light("fan1") })
	f.ready()

	if err := handle.SetBrightness(50); !errors.Is(err, device.ErrNotDimmable) {
		t.Errorf("%v", err)
	}

	if handle.GetLightData().Dimmable {
		t.Error("dimmable")
	}
}

func TestLight_OnAndOffGoThroughTheChosenLight(t *testing.T) {
	var handle *LightHandle

	f := newFixture(t, func(c *PluginConfig) { handle = c.Light("fan1") })
	f.ready()

	if err := handle.SetState(device.SwitchStateOff); err != nil {
		t.Fatal(err)
	}

	if got := f.waitActions(1); got[0] != "fan1 TurnDownLightOff" {
		t.Errorf("%v", got)
	}

	f.eventually("off", func() bool { return f.plugin.byID["fan1"].light.data.State == device.SwitchStateOff })
}

// calibrate tells the light where its dimmer is, as the page does.
func (f *fixture) calibrate(id string, level int) {
	f.t.Helper()

	var err error
	f.onPlugin(func() { err = f.plugin.byID[id].light.Calibrate(level) })

	if err != nil {
		f.t.Fatal(err)
	}
}

// release lets a hold in progress run out: the time passes and the timer for the stop fires.
func (f *fixture) release(hold time.Duration) {
	f.t.Helper()

	f.waitTimers(1)
	f.advance(hold)
	f.fire()
}

func TestLight_ABrightnessIsReachedByRunningTheDimmerForTheTimeItTakes(t *testing.T) {
	var handle *LightHandle

	f := newFixture(t, func(c *PluginConfig) {
		dimmable(c)
		handle = c.Light("fan1")
	})
	f.ready()

	if !handle.GetLightData().Dimmable || handle.GetLightData().HasBrightness {
		t.Fatalf("%+v", handle.GetLightData())
	}

	// It is at its dimmest, so the next hold goes up: 1 to 51 is 50 of the 99 levels.
	f.calibrate("fan1", 1)

	if err := handle.SetBrightness(51); err != nil {
		t.Fatal(err)
	}

	if got := f.waitActions(1); got[0] != "fan1 StartDownLightDimmer" {
		t.Fatalf("%v", got)
	}

	f.release(testDelay + testRamp*50/99)

	if got := f.waitActions(2); got[1] != "fan1 Stop" {
		t.Fatalf("%v", got)
	}

	f.eventually("the brightness", func() bool {
		d := f.plugin.byID["fan1"].light.data
		return d.HasBrightness && d.Brightness == 51 && !f.plugin.byID["fan1"].light.busy
	})

	if d := handle.GetLightData(); !d.BrightnessEstimated {
		t.Errorf("%+v", d)
	}

	// The next hold goes down, so a step down is one hold.
	if err := handle.StepBrightness(-10); err != nil {
		t.Fatal(err)
	}

	f.waitActions(3)
	f.release(testDelay + testRamp*10/99)
	f.waitActions(4)
	f.eventually("41%", func() bool { return f.plugin.byID["fan1"].light.data.Brightness == 41 })
}

func TestLight_TheWrongWayIsATapThatTurnsTheDimmerRoundAndThenTheHold(t *testing.T) {
	var handle *LightHandle

	f := newFixture(t, func(c *PluginConfig) {
		dimmable(c)
		handle = c.Light("fan1")
	})
	f.ready()

	f.calibrate("fan1", 1)

	// The next hold goes up, and 1 is as low as it gets: go up to 51 first.
	_ = handle.SetBrightness(51)
	f.waitActions(1)
	f.release(testDelay + testRamp*50/99)
	f.eventually("51%", func() bool { return f.plugin.byID["fan1"].light.data.Brightness == 51 })

	// Now the next hold goes down. Calibrating as "at 51 going up" makes down the wrong way.
	f.onPlugin(func() { f.plugin.byID["fan1"].light.model.Calibrate(51, true) })

	if err := handle.StepBrightness(-10); err != nil {
		t.Fatal(err)
	}

	// A tap up, and then the hold down.
	f.waitActions(3)
	f.release(testDelay + minimumTap)
	f.waitActions(5)
	f.release(testDelay + testRamp*11/99)
	f.waitActions(6)
	f.eventually("41%", func() bool {
		d := f.plugin.byID["fan1"].light.data
		return d.Brightness >= 40 && d.Brightness <= 42 && !f.plugin.byID["fan1"].light.busy
	})
}

// minimumTap is the shortest hold the dimmer model makes.
const minimumTap = 80 * time.Millisecond

func TestLight_ASwitchedOffLightIsSwitchedOnBeforeItIsDimmed(t *testing.T) {
	var handle *LightHandle

	f := newFixture(t, func(c *PluginConfig) {
		c.Options("fan2", DeviceOptions{DimRamp: testRamp, DimDelay: testDelay})
		handle = c.Light("fan2") // its down light is off
	})
	f.ready()
	f.calibrate("fan2", 1)

	if err := handle.SetBrightness(50); err != nil {
		t.Fatal(err)
	}

	got := f.waitActions(2)
	if got[0] != "fan2 TurnDownLightOn" || got[1] != "fan2 StartDownLightDimmer" {
		t.Errorf("%v", got)
	}
}

func TestLight_ABrightnessOrAStepBeforeCalibratingIsRefusedAndSoIsALevelOutOfRange(t *testing.T) {
	var handle *LightHandle

	f := newFixture(t, func(c *PluginConfig) {
		dimmable(c)
		handle = c.Light("fan1")
	})
	f.ready()

	for name, err := range map[string]error{"a step": handle.StepBrightness(10), "a level": handle.SetBrightness(50)} {
		if err == nil || !strings.Contains(err.Error(), "calibrate it first") {
			t.Errorf("%s: %v", name, err)
		}
	}

	f.calibrate("fan1", 1)

	for _, bad := range []int{0, 101, -5} {
		if err := handle.SetBrightness(bad); !errors.Is(err, device.ErrInvalidBrightness) {
			t.Errorf("%d: %v", bad, err)
		}
	}

	if len(f.bridge.done()) != 0 {
		t.Errorf("sent %v", f.bridge.done())
	}
}

func TestLight_ADimmerThatCannotBeStoppedIsReportedAndForgotten(t *testing.T) {
	var handle *LightHandle

	f := newFixture(t, func(c *PluginConfig) {
		dimmable(c)
		handle = c.Light("fan1")
	})
	f.ready()
	f.calibrate("fan1", 1)

	f.bridge.mu.Lock()
	f.bridge.failDo["Stop"] = true
	f.bridge.mu.Unlock()

	if err := handle.SetBrightness(50); err != nil {
		t.Fatal(err)
	}

	f.waitActions(1)
	f.release(testRamp / 2)

	f.eventually("giving up", func() bool {
		l := f.plugin.byID["fan1"].light
		return !l.busy && !l.model.Known() && len(l.queue) == 0 && !l.data.HasBrightness
	})
}

func TestLight_OnlyAFewChangesWaitForTheLastToFinish(t *testing.T) {
	var handle *LightHandle

	f := newFixture(t, func(c *PluginConfig) {
		dimmable(c)
		handle = c.Light("fan1")
	})
	f.ready()
	f.calibrate("fan1", 1)

	var refused error

	for range maxQueued + 2 {
		if err := handle.SetBrightness(80); err != nil {
			refused = err
		}
	}

	if refused == nil {
		t.Error("an endless queue")
	}
}

func TestLight_CalibratingNeedsADimmableLightAndOneThatIsNotBeingRun(t *testing.T) {
	f := newFixture(t, nil)
	f.ready()

	var err error
	f.onPlugin(func() { err = f.plugin.byID["fan1"].light.Calibrate(1) })

	if !errors.Is(err, device.ErrNotDimmable) {
		t.Errorf("%v", err)
	}

	g := newFixture(t, dimmable)
	g.ready()
	g.calibrate("fan1", 1)
	g.onPlugin(func() {
		err = g.plugin.command(common.Command{Device: "fan1", Target: "light", Op: "brightness", Value: 60})
	})

	if err != nil {
		t.Fatal(err)
	}

	g.waitActions(1)
	g.onPlugin(func() { err = g.plugin.byID["fan1"].light.Calibrate(100) })

	if err == nil {
		t.Error("calibrated while the dimmer was being run")
	}
}

func TestStop_EachEntityIsDeregisteredAndAHandleGoesBackToNothing(t *testing.T) {
	var handle *FanHandle

	f := newFixture(t, func(c *PluginConfig) { handle = c.Fan("fan1") })
	f.ready()

	f.onPlugin(func() { f.plugin.Stop() })

	f.container.mu.Lock()
	removed := len(f.container.removed)
	f.container.mu.Unlock()

	if removed != 4 {
		t.Errorf("%d removed", removed)
	}

	if handle.GetFanData().HasData || handle.SetSpeed(1) == nil {
		t.Error("the handle still works")
	}
}

func TestGetDevices_AreDevicesWithTheirFanAndLight(t *testing.T) {
	f := newFixture(t, dimmable)
	f.ready()

	var items []data.DeviceView
	f.onPlugin(func() { items = f.plugin.getDevices() })

	if len(items) != 2 {
		t.Fatalf("%d items", len(items))
	}

	first := items[0] // sorted by name
	if first.Name != "Bedroom Ceiling Fan" || first.Fan == nil || first.Light == nil || len(first.Fan.Speeds) != 3 {
		t.Errorf("%+v", first)
	}

	second := items[1]
	if !second.Light.Dimmable || second.Location != "Living Room" {
		t.Errorf("%+v", second)
	}
}

func TestCommand_FromThePageDoesWhatItSays(t *testing.T) {
	f := newFixture(t, nil)
	f.ready()

	var err error
	f.onPlugin(func() { err = f.plugin.command(common.Command{Device: "fan1", Target: "fan", Op: "speed", Value: 2}) })

	if err != nil {
		t.Fatal(err)
	}

	if got := f.waitActions(1); got[0] != "fan1 SetSpeed 2" {
		t.Errorf("%v", got)
	}

	for _, bad := range []common.Command{
		{Device: "nope", Target: "fan", Op: "power"},
		{Device: "fan1", Target: "toaster", Op: "power"},
		{Device: "fan1", Target: "fan", Op: "explode"},
		{Device: "fan1", Target: "fan", Op: "speed", Value: 9},
	} {
		f.onPlugin(func() { err = f.plugin.command(bad) })

		if err == nil {
			t.Errorf("%+v was accepted", bad)
		}
	}
}

func TestLight_TheDimmerIsStartedWithTheCommandTheDeviceObeysIfTheBridgeHasTwo(t *testing.T) {
	var handle *LightHandle

	bridge := newFakeBridge()
	bridge.commands = map[string]string{"StartDownLightDimmer": "5484616f"}

	f := newFixtureWith(t, bridge, func(c *PluginConfig) {
		c.Options("fan2", DeviceOptions{DimRamp: testRamp, DimDelay: testDelay})
		handle = c.Light("fan2")
	})
	f.ready()
	f.calibrate("fan2", 1)

	if err := handle.SetBrightness(50); err != nil {
		t.Fatal(err)
	}

	got := f.waitActions(2)
	if got[0] != "fan2 TurnDownLightOn" || got[1] != "fan2 transmit 5484616f" {
		t.Errorf("%v", got)
	}
}
