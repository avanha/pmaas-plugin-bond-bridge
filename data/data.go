// Package data is what the plugin shows: the devices of a Bond Bridge as the page sees them.
package data

import (
	"time"

	"github.com/avanha/pmaas-spi/device"
)

// DeviceView is a bridge device with its fan and light.
type DeviceView struct {
	ID       string
	Name     string
	Location string

	Offline      bool
	OfflineSince time.Time

	Fan   *FanView
	Light *LightView
}

type FanView struct {
	device.Fan
	Speeds []int
	On     bool
}

type LightView struct {
	device.Light
	On   bool
	Busy bool

	// Step is how much the brightness buttons change it by, and Presets are the levels to go to.
	Step    int
	Presets []int

	// Holds are the seconds the hold buttons hold the dimmer for. NextUp is the way the next hold is expected to go, and
	// DirectionKnown is whether anything is known about it: only once the dimmer has been calibrated.
	Holds          []int
	NextUp         bool
	DirectionKnown bool
}
