package bondbridge

import (
	"fmt"

	"github.com/avanha/pmaas-plugin-bond-bridge/internal/bond"
	spicommon "github.com/avanha/pmaas-spi/common"
	"github.com/avanha/pmaas-spi/device"
	"github.com/avanha/pmaas-spi/events"
)

// fan is a device's fan. Owned by the plugin goroutine.
type fan struct {
	dev  *bondDevice
	id   string
	data device.Fan
	stub *fanStub
}

var _ device.IFan = (*fan)(nil)

func newFan(dev *bondDevice) *fan {
	maxSpeed := dev.info.MaxSpeed
	if maxSpeed < 1 {
		maxSpeed = 1
	}

	return &fan{dev: dev, data: device.Fan{Name: dev.info.Name, MaxSpeed: maxSpeed, Assumed: true}, stub: &fanStub{}}
}

func (f *fan) entityEvent() events.EntityEvent {
	return events.EntityEvent{Id: f.id, EntityType: device.IFanType, Name: f.data.Name}
}

func (f *fan) register() {
	p := f.dev.p
	f.stub.wrapper.Store(&spicommon.ThreadSafeEntityWrapper[device.IFan]{Container: p.container, Entity: f})

	id, err := p.container.RegisterEntity(f.dev.info.ID+"/fan", device.IFanType, f.data.Name, func() (any, error) { return f.stub, nil })
	if err != nil {
		p.log.Error("unable to register a fan", "name", f.data.Name, "error", err)

		return
	}

	f.id = id

	if handle := p.handleForFan(f.dev.info); handle != nil {
		event := spicommon.StubAvailableEvent[device.IFan]{EntityEvent: f.entityEvent(), EntityStub: f.stub}
		if err := handle.stub.Provide(p.container, event); err != nil {
			p.log.Error("unable to provide the stub", "error", err)
		}
	}
}

func (f *fan) close() {
	p := f.dev.p

	if handle := p.handleForFan(f.dev.info); handle != nil {
		handle.stub.Withdraw()
	}

	if f.id != "" {
		if err := p.container.DeregisterEntity(f.id); err != nil {
			p.log.Error("unable to deregister a fan", "name", f.data.Name, "error", err)
		}
	}

	f.stub.wrapper.Store(nil)
}

func (p *plugin) handleForFan(info bond.Device) *FanHandle {
	if h, ok := p.config.fans[info.Name]; ok {
		return h
	}

	return p.config.fans[info.ID]
}

func (f *fan) GetFanData() device.Fan {
	return f.data
}

func (f *fan) GetSwitchData() device.Switch {
	d := f.data

	return bondSwitch(d.Name, d.HasData, d.State, d.LastChangeTime, d.LastUpdateTime, d.Connectivity, d.OfflineSince, d.OnlineSince)
}

func (f *fan) SetState(state device.SwitchState) error {
	if !state.IsSettable() {
		return fmt.Errorf("unable to set %q to %s: %w", f.data.Name, state, device.ErrInvalidSwitchState)
	}

	if err := f.dev.checkReachable(f.data.Name); err != nil {
		return err
	}

	action := "TurnOff"
	if state == device.SwitchStateOn {
		action = "TurnOn"
	}

	return f.dev.p.send(f.dev, action, nil)
}

func (f *fan) SetSpeed(speed int) error {
	if speed < 1 || speed > f.data.MaxSpeed {
		return fmt.Errorf("unable to set %q to speed %d (1 to %d): %w", f.data.Name, speed, f.data.MaxSpeed, device.ErrInvalidFanSpeed)
	}

	if err := f.dev.checkReachable(f.data.Name); err != nil {
		return err
	}

	return f.dev.p.send(f.dev, "SetSpeed", &speed)
}

func (f *fan) SetDirection(direction device.FanDirection) error {
	if direction != device.FanDirectionForward && direction != device.FanDirectionReverse {
		return fmt.Errorf("unable to set %q to %s: %w", f.data.Name, direction, device.ErrInvalidFanDirection)
	}

	if !f.dev.info.Has("SetDirection") {
		return fmt.Errorf("%q can't change direction", f.data.Name)
	}

	if err := f.dev.checkReachable(f.data.Name); err != nil {
		return err
	}

	argument := 1
	if direction == device.FanDirectionReverse {
		argument = -1
	}

	return f.dev.p.send(f.dev, "SetDirection", &argument)
}

// apply takes the state the bridge reports. The first establishes the state without being a change.
func (f *fan) apply(state bond.State) {
	now := f.dev.p.now()
	old := f.data
	changed := false

	if state.Power != nil {
		on := device.SwitchStateOff
		if *state.Power != 0 {
			on = device.SwitchStateOn
		}

		if f.data.State != on {
			f.data.State = on
			changed = true

			if old.HasData {
				f.data.LastChangeTime = now
			}
		}
	}

	if state.Speed != nil && *state.Speed >= 1 && f.data.Speed != *state.Speed {
		f.data.Speed, changed = *state.Speed, true
	}

	if state.Direction != nil {
		direction := device.FanDirectionForward
		if *state.Direction < 0 {
			direction = device.FanDirectionReverse
		}

		if f.data.Direction != direction {
			f.data.Direction, changed = direction, true
		}
	}

	f.data.HasData, f.data.LastUpdateTime = true, now

	if !changed && old.HasData {
		return
	}

	f.broadcastState()

	if old.HasData && old.State != f.data.State {
		f.dev.p.broadcast(f.id, device.SwitchStateChangedEvent{EntityEvent: f.entityEvent(), NewState: f.data.State, OldState: old.State})
	}
}

func (f *fan) broadcastState() {
	if f.id == "" {
		return
	}

	f.dev.p.broadcast(f.id, events.EntityStateChangedEvent{EntityEvent: f.entityEvent(), NewState: f.data})
}
