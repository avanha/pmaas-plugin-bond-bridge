package bondbridge

import (
	"sync/atomic"

	spicommon "github.com/avanha/pmaas-spi/common"
	"github.com/avanha/pmaas-spi/device"
)

// fanStub is the fan as every other goroutine sees it: each call hops onto the plugin's.
type fanStub struct {
	wrapper atomic.Pointer[spicommon.ThreadSafeEntityWrapper[device.IFan]]
}

var _ device.IFan = (*fanStub)(nil)

func (s *fanStub) GetFanData() device.Fan {
	return spicommon.ThreadSafeEntityWrapperExecValueFunc(s.wrapper.Load(), func(t device.IFan) device.Fan { return t.GetFanData() })
}

func (s *fanStub) GetSwitchData() device.Switch {
	return spicommon.ThreadSafeEntityWrapperExecValueFunc(s.wrapper.Load(), func(t device.IFan) device.Switch { return t.GetSwitchData() })
}

func (s *fanStub) SetState(state device.SwitchState) error {
	return spicommon.ThreadSafeEntityWrapperExecValueFunc(s.wrapper.Load(), func(t device.IFan) error { return t.SetState(state) })
}

func (s *fanStub) SetSpeed(speed int) error {
	return spicommon.ThreadSafeEntityWrapperExecValueFunc(s.wrapper.Load(), func(t device.IFan) error { return t.SetSpeed(speed) })
}

func (s *fanStub) SetDirection(direction device.FanDirection) error {
	return spicommon.ThreadSafeEntityWrapperExecValueFunc(s.wrapper.Load(), func(t device.IFan) error { return t.SetDirection(direction) })
}

// lightStub is the light as every other goroutine sees it.
type lightStub struct {
	wrapper atomic.Pointer[spicommon.ThreadSafeEntityWrapper[device.ILight]]
}

var _ device.ILight = (*lightStub)(nil)

func (s *lightStub) GetLightData() device.Light {
	return spicommon.ThreadSafeEntityWrapperExecValueFunc(s.wrapper.Load(), func(t device.ILight) device.Light { return t.GetLightData() })
}

func (s *lightStub) GetSwitchData() device.Switch {
	return spicommon.ThreadSafeEntityWrapperExecValueFunc(s.wrapper.Load(), func(t device.ILight) device.Switch { return t.GetSwitchData() })
}

func (s *lightStub) SetState(state device.SwitchState) error {
	return spicommon.ThreadSafeEntityWrapperExecValueFunc(s.wrapper.Load(), func(t device.ILight) error { return t.SetState(state) })
}

func (s *lightStub) SetBrightness(percent int) error {
	return spicommon.ThreadSafeEntityWrapperExecValueFunc(s.wrapper.Load(), func(t device.ILight) error { return t.SetBrightness(percent) })
}

func (s *lightStub) StepBrightness(percent int) error {
	return spicommon.ThreadSafeEntityWrapperExecValueFunc(s.wrapper.Load(), func(t device.ILight) error { return t.StepBrightness(percent) })
}
