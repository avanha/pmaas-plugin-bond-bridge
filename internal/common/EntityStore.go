package common

import "github.com/avanha/pmaas-plugin-bond-bridge/data"

// Command is what a button on the page asks for.
type Command struct {
	Device string
	Target string // "fan" or "light"
	Op     string
	Value  int
}

// EntityStore is what the HTTP handler needs from the plugin, which owns the devices on its own goroutine.
type EntityStore interface {
	// GetDevices is the devices with their fan and light, sorted by name.
	GetDevices() ([]data.DeviceView, error)

	// SendCommand does what a button on the page asked, and returns once it is accepted for delivery.
	SendCommand(command Command) error
}
