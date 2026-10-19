package bondbridge

import (
	"github.com/avanha/pmaas-plugin-bond-bridge/data"
	"github.com/avanha/pmaas-plugin-bond-bridge/internal/common"
	spi "github.com/avanha/pmaas-spi"
)

// entityStoreAdapter lets the HTTP handler, which is called on arbitrary goroutines, reach the devices on the plugin's.
type entityStoreAdapter struct {
	parent *plugin
}

func (e *entityStoreAdapter) GetDevices() ([]data.DeviceView, error) {
	return spi.ExecValueFunctionOnPluginGoRoutine(
		e.parent.container,
		e.parent.getDevices,
		func() []data.DeviceView { return nil },
		"unable to get the devices")
}

func (e *entityStoreAdapter) SendCommand(command common.Command) error {
	result, err := spi.Exec(e.parent.container, func() error { return e.parent.command(command) })
	if err != nil {
		return err
	}

	return result
}
