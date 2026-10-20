// Package bond is a client for the local HTTP API of a Bond Bridge (https://docs-local.appbond.com). It does one thing per call
// and keeps nothing: the bridge is a small device, so what to ask it, and when, is the caller's business.
package bond

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// API is what the plugin needs from a bridge. Client is the real one; tests use a fake.
type API interface {
	// Devices lists the devices paired with the bridge, with what each can do.
	Devices(ctx context.Context) ([]Device, error)

	// State is what the bridge believes the device's state is.
	State(ctx context.Context, id string) (State, error)

	// Do sends an action, with its argument if it takes one.
	Do(ctx context.Context, id string, action string, argument *int) error

	// Correct changes what the bridge believes the state of a device is, by state variable ("light": 0), without the bridge
	// transmitting anything. It is for when the belief is wrong: the bridge only knows what it has sent.
	Correct(ctx context.Context, id string, state map[string]int) error

	// Transmit sends one of the device's recorded commands by its id, which Do can't: when a device has two commands for an action,
	// Do uses the first, and it is not always the one the device obeys.
	Transmit(ctx context.Context, id string, command string) error
}

// Device is a thing the bridge controls.
type Device struct {
	ID       string
	Name     string
	Type     string
	Location string

	// Actions are the names of what it can be told to do ("TurnOn", "SetSpeed").
	Actions []string

	// MaxSpeed is the number of speeds of a fan, or 0.
	MaxSpeed int

	// Commands are the ids of the commands to send, by action, where Do's choice of command for the action isn't the one the
	// device obeys. See pickCommands.
	Commands map[string]string
}

// Has is whether the device can do the action.
func (d Device) Has(action string) bool {
	for _, a := range d.Actions {
		if a == action {
			return true
		}
	}

	return false
}

// State is the state the bridge keeps for a device. It is what the bridge last sent, unless the device reports back, so for a
// fan on RF it is an assumption. A field the device doesn't have is nil.
type State struct {
	Power      *int `json:"power"`
	Speed      *int `json:"speed"`
	Direction  *int `json:"direction"` // 1 forward, -1 reverse
	Light      *int `json:"light"`
	UpLight    *int `json:"up_light"`
	DownLight  *int `json:"down_light"`
	Brightness *int `json:"brightness"`
}

// Client talks to one bridge.
type Client struct {
	base  string
	token string
	http  *http.Client
}

var _ API = (*Client)(nil)

// NewClient is a client for the bridge at the host (an address or name, with a port if it is not 80), with its local token.
func NewClient(host string, token string, timeout time.Duration) *Client {
	base := host
	if !strings.Contains(base, "://") {
		base = "http://" + base
	}

	return &Client{base: strings.TrimRight(base, "/"), token: token, http: &http.Client{Timeout: timeout}}
}

func (c *Client) do(ctx context.Context, method string, path string, body any, out any) error {
	var reader io.Reader

	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}

		reader = bytes.NewReader(encoded)
	}

	request, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return err
	}

	request.Header.Set("BOND-Token", c.token)

	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := c.http.Do(request)
	if err != nil {
		return err
	}

	defer response.Body.Close()

	if response.StatusCode/100 != 2 {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 200))

		return fmt.Errorf("the bridge answered %s to %s %s: %s", response.Status, method, path, strings.TrimSpace(string(detail)))
	}

	if out == nil {
		_, _ = io.Copy(io.Discard, response.Body)

		return nil
	}

	return json.NewDecoder(response.Body).Decode(out)
}

// Devices lists every device with its details. The bridge answers the list with ids only, so it is one call for the list and
// two for each device.
func (c *Client) Devices(ctx context.Context) ([]Device, error) {
	var listed map[string]json.RawMessage

	if err := c.do(ctx, http.MethodGet, "/v2/devices", nil, &listed); err != nil {
		return nil, err
	}

	var devices []Device

	for id := range listed {
		// "_" and "__" are the bridge's revision markers, not devices.
		if strings.HasPrefix(id, "_") {
			continue
		}

		var info struct {
			Name     string   `json:"name"`
			Type     string   `json:"type"`
			Location string   `json:"location"`
			Actions  []string `json:"actions"`
		}

		escaped := url.PathEscape(id)

		if err := c.do(ctx, http.MethodGet, "/v2/devices/"+escaped, nil, &info); err != nil {
			return nil, fmt.Errorf("unable to read device %s: %w", id, err)
		}

		var properties struct {
			MaxSpeed int `json:"max_speed"`
		}

		if err := c.do(ctx, http.MethodGet, "/v2/devices/"+escaped+"/properties", nil, &properties); err != nil {
			return nil, fmt.Errorf("unable to read the properties of device %s: %w", id, err)
		}

		// Best effort: without the commands the actions are used as they are.
		commands, _ := c.pickCommands(ctx, id)

		devices = append(devices, Device{
			ID: id, Name: info.Name, Type: info.Type, Location: info.Location, Actions: info.Actions, MaxSpeed: properties.MaxSpeed,
			Commands: commands,
		})
	}

	sortDevices(devices)

	return devices, nil
}

func sortDevices(devices []Device) {
	for i := 1; i < len(devices); i++ {
		for j := i; j > 0 && devices[j].Name < devices[j-1].Name; j-- {
			devices[j], devices[j-1] = devices[j-1], devices[j]
		}
	}
}

func (c *Client) State(ctx context.Context, id string) (State, error) {
	var state State

	err := c.do(ctx, http.MethodGet, "/v2/devices/"+url.PathEscape(id)+"/state", nil, &state)

	return state, err
}

func (c *Client) Correct(ctx context.Context, id string, state map[string]int) error {
	return c.do(ctx, http.MethodPatch, "/v2/devices/"+url.PathEscape(id)+"/state", state, nil)
}

func (c *Client) Do(ctx context.Context, id string, action string, argument *int) error {
	body := map[string]any{}
	if argument != nil {
		body["argument"] = *argument
	}

	return c.do(ctx, http.MethodPut, "/v2/devices/"+url.PathEscape(id)+"/actions/"+url.PathEscape(action), body, nil)
}

func (c *Client) Transmit(ctx context.Context, id string, command string) error {
	return c.do(ctx, http.MethodPut, "/v2/devices/"+url.PathEscape(id)+"/commands/"+url.PathEscape(command)+"/tx", map[string]any{}, nil)
}

// command is one of a device's recorded commands.
type command struct {
	id     string
	action string
	data   string // what it transmits
}

// dimmerLights is the command that toggles a light, for each action that dims it. The dimmer is the same signal held down.
var dimmerLights = map[string]string{
	"StartDownLightDimmer": "ToggleDownLight",
	"StartUpLightDimmer":   "ToggleUpLight",
	"StartDimmer":          "ToggleLight",
}

// pickCommands finds, for each dimmer action the device has more than one command for, the one that is the toggle of the light
// held down. A fan with the Bond firmware seen (v4.39) has two "Bottom Dimmer" commands for StartDownLightDimmer. Do uses the first,
// and the fan ignores it. The other sends the toggle's signal for longer, which is what the remote does when it is held. An action
// that has a single command, or none that matches, is left to Do.
func (c *Client) pickCommands(ctx context.Context, id string) (map[string]string, error) {
	escaped := url.PathEscape(id)

	var listed map[string]json.RawMessage

	if err := c.do(ctx, http.MethodGet, "/v2/devices/"+escaped+"/commands", nil, &listed); err != nil {
		return nil, err
	}

	var commands []command

	for commandID := range listed {
		if strings.HasPrefix(commandID, "_") {
			continue
		}

		var info struct {
			Action string `json:"action"`
		}

		path := "/v2/devices/" + escaped + "/commands/" + url.PathEscape(commandID)

		if err := c.do(ctx, http.MethodGet, path, nil, &info); err != nil {
			return nil, err
		}

		// Only the commands that matter are read further.
		if _, ok := dimmerLights[info.Action]; !ok && !isToggle(info.Action) {
			continue
		}

		var signal struct {
			Data string `json:"data"`
		}

		if err := c.do(ctx, http.MethodGet, path+"/signal", nil, &signal); err != nil {
			return nil, err
		}

		commands = append(commands, command{id: commandID, action: info.Action, data: signal.Data})
	}

	return matchDimmers(commands), nil
}

func isToggle(action string) bool {
	for _, toggle := range dimmerLights {
		if toggle == action {
			return true
		}
	}

	return false
}

// matchDimmers is the command id for each dimmer action that has several commands, and one of them sends the signal of the
// light's toggle.
func matchDimmers(commands []command) map[string]string {
	picked := map[string]string{}

	for dimmer, toggle := range dimmerLights {
		var candidates []command

		var toggleData string

		for _, c := range commands {
			switch c.action {
			case dimmer:
				candidates = append(candidates, c)
			case toggle:
				toggleData = c.data
			}
		}

		if len(candidates) < 2 || toggleData == "" {
			continue
		}

		for _, candidate := range candidates {
			if candidate.data == toggleData {
				picked[dimmer] = candidate.id

				break
			}
		}
	}

	if len(picked) == 0 {
		return nil
	}

	return picked
}
