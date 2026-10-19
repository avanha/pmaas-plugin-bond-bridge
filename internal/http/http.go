package http

import (
	"embed"
	"errors"
	"fmt"
	"github.com/avanha/pmaas-plugin-bond-bridge/data"
	"github.com/avanha/pmaas-plugin-bond-bridge/internal/common"
	spi "github.com/avanha/pmaas-spi"
	"github.com/avanha/pmaas-spi/format"
	"github.com/avanha/pmaas-spi/logging"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"text/template"
)

//go:embed content/static content/templates
var contentFS embed.FS

// The templates are rendered by PMAAS's template engine plugin, which uses text/template: it does no HTML escaping, so every
// string a template prints has to go through the html function itself.
var deviceTemplate = spi.TemplateInfo{
	Name:    "bondbridge_device",
	FuncMap: template.FuncMap{"RelativeTime": format.Ago},
	Paths:   []string{"templates/bondbridge_device.htmlt"},
	Styles:  []string{"css/bondbridge.css"},
	Scripts: []string{"js/bondbridge.js"},
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

// Handler owns the plugin's HTTP surface: the page, and the renderer for what is on it. It reaches back into the plugin only
// through entityStore.
type Handler struct {
	log         *slog.Logger
	container   spi.IPMAASContainer
	entityStore common.EntityStore
}

func NewHandler() *Handler {
	return &Handler{}
}

func (h *Handler) Init(container spi.IPMAASContainer, entityStore common.EntityStore) {
	h.container = container
	h.log = logging.For(container, "http")
	h.entityStore = entityStore

	container.ProvideContentFS(&contentFS, "content")
	container.EnableStaticContent("static")
	container.AddRouteWithOptions("", h.handleHttpListRequest, &spi.HttpHandlerOptions{SupportsXsrfValidation: true, MenuLabel: "Bond Bridge"})
	container.RegisterEntityRenderer(reflect.TypeFor[data.DeviceView](), h.deviceRendererFactory)
	container.AddJsonRoute("command", func() any { return &commandRequest{} }, h.handleHttpCommandRequest)
}

func (h *Handler) handleHttpListRequest(w http.ResponseWriter, r *http.Request) {
	devices, err := h.entityStore.GetDevices()
	if err != nil {
		h.log.Error("unable to list the devices", "error", err)
		devices = nil
	}

	items := make([]any, len(devices))
	for i, device := range devices {
		items[i] = device
	}

	h.container.RenderList(w, r, spi.RenderListOptions{Title: "Bond Bridge", Masonry: true}, items)
}

// handleHttpCommandRequest does what a button on the page asked. Like every JSON route it needs the XSRF cookie, which is what
// keeps another site from making a visitor's browser run a fan.
func (h *Handler) handleHttpCommandRequest(_ http.ResponseWriter, r *http.Request, request any) (any, error) {
	if r.Method != http.MethodPost {
		return nil, errors.New("method not allowed")
	}

	body, ok := request.(*commandRequest)
	if !ok || body.Device == "" {
		return nil, errors.New("a device is required")
	}

	command := common.Command{Device: body.Device, Target: body.Target, Op: body.Op, Value: body.Value}
	if err := h.entityStore.SendCommand(command); err != nil {
		return nil, err
	}

	return commandResponse{Accepted: true}, nil
}

func (h *Handler) deviceRendererFactory() (spi.EntityRenderer, error) {
	compiled, err := h.container.GetTemplate(&deviceTemplate)
	if err != nil {
		return spi.EntityRenderer{}, fmt.Errorf("unable to load the bondbridge_device template: %w", err)
	}

	render := func(w io.Writer, entity any) error {
		view, ok := entity.(data.DeviceView)
		if !ok {
			return errors.New("item is not a data.DeviceView")
		}

		if err := compiled.Instance.Execute(w, view); err != nil {
			return fmt.Errorf("unable to execute the bondbridge_device template: %w", err)
		}

		return nil
	}

	return spi.EntityRenderer{StreamingRenderFunc: render, Styles: compiled.Styles, Scripts: compiled.Scripts}, nil
}
