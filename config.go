package bondbridge

import (
	"fmt"
	"time"

	"github.com/avanha/pmaas-plugin-bond-bridge/internal/bond"
	spicommon "github.com/avanha/pmaas-spi/common"
	"github.com/avanha/pmaas-spi/device"
)

// LightChoice is which of a device's lights is the one to control. A ceiling fan may have an up light and a down light, with
// only one fitted.
type LightChoice int

const (
	// LightAuto is the down light if the device has one, and otherwise its only light.
	LightAuto LightChoice = iota
	LightDown
	LightUp
	// LightNone leaves the light out: the device is only a fan.
	LightNone
)

// DeviceOptions says what to make of one device the bridge has, by its name or its id (see PluginConfig.Options).
type DeviceOptions struct {
	// Hidden leaves the device out altogether.
	Hidden bool

	// Light is which light to control. The default is LightAuto.
	Light LightChoice

	// DimRamp is how long the light takes to go from its dimmest to its brightest while the dimmer is held. A bridge can't be told
	// a brightness, only to start the dimmer, which sweeps up and down until it is stopped, so a brightness is reached by timing a
	// hold. Time it once, from the dimmest to the brightest, with a remote. Zero, the default, leaves the light switched on and off
	// only. The position is not read from anywhere, so it has to be calibrated on the page once (it is at its dimmest, or at its
	// brightest) and drifts after that.
	DimRamp time.Duration

	// DimKeepsDirection is that a new hold of the dimmer carries on the way the last went. The default is that it starts the other
	// way, as most remotes do. Check by holding the remote's dim button twice.
	DimKeepsDirection bool
}

// PluginConfig configures the plugin.
type PluginConfig struct {
	// Host is the bridge's address or name, with a port if it isn't 80: "10.47.6.39". Token is its local token (in the Bond app,
	// under Settings, Advanced, Local Token), which is kept out of source control by the assembly.
	Host  string
	Token string

	// PollEvery is how often the bridge is asked what it believes the state of each device is. The default is 30 seconds. The bridge
	// knows only what it has sent a fan, so this is only about a change made some other way through the bridge (its app).
	PollEvery time.Duration

	// Timeout is how long a request to the bridge may take. The default is 5 seconds.
	Timeout time.Duration

	devices map[string]DeviceOptions
	fans    map[string]*FanHandle
	lights  map[string]*LightHandle

	// api replaces the real bridge, for tests.
	api bond.API
}

func NewPluginConfig() PluginConfig {
	return PluginConfig{
		PollEvery: 30 * time.Second,
		Timeout:   5 * time.Second,
		devices:   make(map[string]DeviceOptions),
		fans:      make(map[string]*FanHandle),
		lights:    make(map[string]*LightHandle),
	}
}

// Options says what to make of a device, by the name it has in the Bond app (or its id). Devices need nothing said: all of what
// the bridge has is found and shown.
func (c *PluginConfig) Options(device string, options DeviceOptions) {
	c.devices[device] = options
}

// Fan is the handle on the fan of the device, by its name or id, for an automation: it is a device.IFan, which is also a switch.
// It answers as a fan that has not reported anything until the plugin has found the device. Asking twice gives the same handle.
func (c *PluginConfig) Fan(device string) *FanHandle {
	if handle, ok := c.fans[device]; ok {
		return handle
	}

	handle := &FanHandle{name: device}
	c.fans[device] = handle

	return handle
}

// Light is the handle on the light of the device, as Fan is on its fan: a device.ILight, which is also a switch.
func (c *PluginConfig) Light(device string) *LightHandle {
	if handle, ok := c.lights[device]; ok {
		return handle
	}

	handle := &LightHandle{name: device}
	c.lights[device] = handle

	return handle
}

// FanHandle is the assembly's handle on a fan. Before the plugin has found it, and after the plugin stops, it answers as a fan
// that has not reported anything, and refuses commands.
type FanHandle struct {
	name string
	stub spicommon.StubHolder[device.IFan]
}

var _ device.IFan = (*FanHandle)(nil)

func (h *FanHandle) get() (device.IFan, error) {
	stub, err := h.stub.Stub()
	if err != nil {
		return nil, fmt.Errorf("the fan of %q is not available: %w", h.name, err)
	}

	return stub, nil
}

func (h *FanHandle) GetFanData() device.Fan {
	stub, err := h.get()
	if err != nil {
		return device.Fan{Name: h.name}
	}

	return stub.GetFanData()
}

func (h *FanHandle) GetSwitchData() device.Switch {
	stub, err := h.get()
	if err != nil {
		return device.Switch{Name: h.name}
	}

	return stub.GetSwitchData()
}

func (h *FanHandle) SetState(state device.SwitchState) error {
	stub, err := h.get()
	if err != nil {
		return err
	}

	return stub.SetState(state)
}

func (h *FanHandle) SetSpeed(speed int) error {
	stub, err := h.get()
	if err != nil {
		return err
	}

	return stub.SetSpeed(speed)
}

func (h *FanHandle) SetDirection(direction device.FanDirection) error {
	stub, err := h.get()
	if err != nil {
		return err
	}

	return stub.SetDirection(direction)
}

// LightHandle is the assembly's handle on a light, with the same manners as FanHandle.
type LightHandle struct {
	name string
	stub spicommon.StubHolder[device.ILight]
}

var _ device.ILight = (*LightHandle)(nil)

func (h *LightHandle) get() (device.ILight, error) {
	stub, err := h.stub.Stub()
	if err != nil {
		return nil, fmt.Errorf("the light of %q is not available: %w", h.name, err)
	}

	return stub, nil
}

func (h *LightHandle) GetLightData() device.Light {
	stub, err := h.get()
	if err != nil {
		return device.Light{Name: h.name}
	}

	return stub.GetLightData()
}

func (h *LightHandle) GetSwitchData() device.Switch {
	stub, err := h.get()
	if err != nil {
		return device.Switch{Name: h.name}
	}

	return stub.GetSwitchData()
}

func (h *LightHandle) SetState(state device.SwitchState) error {
	stub, err := h.get()
	if err != nil {
		return err
	}

	return stub.SetState(state)
}

func (h *LightHandle) SetBrightness(percent int) error {
	stub, err := h.get()
	if err != nil {
		return err
	}

	return stub.SetBrightness(percent)
}

func (h *LightHandle) StepBrightness(percent int) error {
	stub, err := h.get()
	if err != nil {
		return err
	}

	return stub.StepBrightness(percent)
}
