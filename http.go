package bondbridge

import (
	"embed"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sort"
	"text/template"
	"time"

	spi "github.com/avanha/pmaas-spi"
	"github.com/avanha/pmaas-spi/device"
)

//go:embed content/static content/templates
var contentFS embed.FS

// The templates are rendered by PMAAS's template engine plugin, which uses text/template: it does no HTML escaping, so every
// string a template prints has to go through the html function itself.
var deviceTemplate = spi.TemplateInfo{
	Name:    "bondbridge_device",
	FuncMap: template.FuncMap{"RelativeTime": relativeTime},
	Paths:   []string{"templates/bondbridge_device.htmlt"},
	Styles:  []string{"css/bondbridge.css"},
	Scripts: []string{"js/bondbridge.js"},
}

var deviceViewType = reflect.TypeFor[deviceView]()

// deviceView is a bridge device as the page shows it.
type deviceView struct {
	ID       string
	Name     string
	Location string

	Offline      bool
	OfflineSince time.Time

	Fan   *fanView
	Light *lightView
}

type fanView struct {
	device.Fan
	Speeds []int
	On     bool
}

type lightView struct {
	device.Light
	On   bool
	Busy bool

	// Step is how much the brightness button changes it by.
	Step    int
	Presets []int
}

// commandRequest is what a button on the page sends.
type commandRequest struct {
	Device string
	Target string // "fan" or "light"
	Op     string
	Value  int
}

type commandResponse struct {
	Accepted bool
}

type httpHandler struct {
	container spi.IPMAASContainer
	plugin    *plugin
}

func (h *httpHandler) init(container spi.IPMAASContainer, p *plugin) {
	h.container, h.plugin = container, p

	container.ProvideContentFS(&contentFS, "content")
	container.EnableStaticContent("static")
	container.AddRouteWithOptions("", h.handleList, &spi.HttpHandlerOptions{SupportsXsrfValidation: true, MenuLabel: "Bond Bridge"})
	container.RegisterEntityRenderer(deviceViewType, h.rendererFactory)
	container.AddJsonRoute("command", func() any { return &commandRequest{} }, h.handleCommand)
}

func (h *httpHandler) handleList(w http.ResponseWriter, r *http.Request) {
	items, err := spi.Exec(h.container, h.plugin.listItems)
	if err != nil {
		h.plugin.log.Error("unable to list the devices", "error", err)
		items = nil
	}

	h.container.RenderList(w, r, spi.RenderListOptions{Title: "Bond Bridge", Masonry: true}, items)
}

func (h *httpHandler) handleCommand(_ http.ResponseWriter, r *http.Request, request any) (any, error) {
	if r.Method != http.MethodPost {
		return nil, errors.New("method not allowed")
	}

	body, ok := request.(*commandRequest)
	if !ok || body.Device == "" {
		return nil, errors.New("a device is required")
	}

	result, err := spi.Exec(h.container, func() error { return h.plugin.command(*body) })
	if err != nil {
		return nil, err
	}

	if result != nil {
		return nil, result
	}

	return commandResponse{Accepted: true}, nil
}

func (h *httpHandler) rendererFactory() (spi.EntityRenderer, error) {
	compiled, err := h.container.GetTemplate(&deviceTemplate)
	if err != nil {
		return spi.EntityRenderer{}, fmt.Errorf("unable to load the bondbridge_device template: %w", err)
	}

	render := func(w io.Writer, entity any) error {
		view, ok := entity.(deviceView)
		if !ok {
			return errors.New("item is not a deviceView")
		}

		if err := compiled.Instance.Execute(w, view); err != nil {
			return fmt.Errorf("unable to execute the bondbridge_device template: %w", err)
		}

		return nil
	}

	return spi.EntityRenderer{StreamingRenderFunc: render, Styles: compiled.Styles, Scripts: compiled.Scripts}, nil
}

// listItems is what the page shows. Runs on the plugin goroutine.
func (p *plugin) listItems() []any {
	views := make([]deviceView, 0, len(p.devices))

	for _, dev := range p.devices {
		view := deviceView{ID: dev.info.ID, Name: dev.info.Name, Location: dev.info.Location, Offline: dev.heard && !dev.reachable}

		if dev.fan != nil {
			speeds := make([]int, dev.fan.data.MaxSpeed)
			for i := range speeds {
				speeds[i] = i + 1
			}

			view.Fan = &fanView{Fan: dev.fan.data, Speeds: speeds, On: dev.fan.data.State == device.SwitchStateOn}
			view.OfflineSince = dev.fan.data.OfflineSince
		}

		if dev.light != nil {
			view.Light = &lightView{Light: dev.light.data, On: dev.light.data.State == device.SwitchStateOn, Busy: dev.light.busy,
				Step: 10, Presets: []int{10, 25, 50, 75, 100}}

			if view.Fan == nil {
				view.OfflineSince = dev.light.data.OfflineSince
			}
		}

		views = append(views, view)
	}

	sort.Slice(views, func(i, j int) bool { return views[i].Name < views[j].Name })

	items := make([]any, len(views))
	for i, v := range views {
		items[i] = v
	}

	return items
}

// command does what a button on the page asked. Runs on the plugin goroutine.
func (p *plugin) command(request commandRequest) error {
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
		}
	}

	return fmt.Errorf("there is no %s %q", request.Target, request.Op)
}

// relativeTime says how long ago a time was, in its two most significant units: "5m ago", "2h 5m ago".
func relativeTime(t time.Time) string {
	if t.IsZero() {
		return "never"
	}

	elapsed := time.Since(t).Truncate(time.Minute)

	switch {
	case elapsed < time.Minute:
		return "just now"
	case elapsed < time.Hour:
		return fmt.Sprintf("%dm ago", int(elapsed.Minutes()))
	case elapsed < 24*time.Hour:
		return fmt.Sprintf("%dh %dm ago", int(elapsed.Hours()), int(elapsed.Minutes())%60)
	default:
		return fmt.Sprintf("%dd %dh ago", int(elapsed.Hours())/24, int(elapsed.Hours())%24)
	}
}
