package http

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"text/template"

	"github.com/avanha/pmaas-plugin-bond-bridge/data"
	"github.com/avanha/pmaas-plugin-bond-bridge/internal/common"
	"github.com/avanha/pmaas-spi/device"
)

func renderDevice(t *testing.T, view data.DeviceView) string {
	t.Helper()

	compiled, err := template.New("device").Funcs(deviceTemplate.FuncMap).ParseFS(contentFS, "content/templates/bondbridge_device.htmlt")
	if err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := compiled.ExecuteTemplate(&out, "bondbridge_device.htmlt", view); err != nil {
		t.Fatal(err)
	}

	return out.String()
}

func livingRoom(known bool) data.DeviceView {
	light := data.LightView{
		Light: device.Light{Name: "Living Room Ceiling Fan Light", HasData: true, Dimmable: true},
		Step:  10, Presets: []int{10, 25, 50, 75, 100},
	}

	if known {
		light.HasBrightness, light.Brightness = true, 1
	}

	return data.DeviceView{
		ID: "fan1", Name: "Living Room Ceiling Fan", Location: "Living Room",
		Fan:   &data.FanView{Fan: device.Fan{Name: "Living Room Ceiling Fan", HasData: true, MaxSpeed: 3}, Speeds: []int{1, 2, 3}},
		Light: &light,
	}
}

func TestTemplate_RendersAFanAndADimmableLightThatHasNotBeenCalibrated(t *testing.T) {
	page := renderDevice(t, livingRoom(false))

	for _, want := range []string{
		"Living Room Ceiling Fan", `data-op="speed" data-value="3"`, "Brightness not known", `data-op="calibrate" data-value="1"`,
		`data-op="sync" data-value="0"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("missing %q in %s", want, page)
		}
	}

	if strings.Contains(page, `data-op="step"`) {
		t.Error("steps offered before the dimmer was calibrated")
	}
}

func TestTemplate_OffersStepsAndLevelsOnceTheDimmerIsCalibrated(t *testing.T) {
	page := renderDevice(t, livingRoom(true))

	if !strings.Contains(page, "About 1%") || !strings.Contains(page, `data-op="step" data-value="10"`) ||
		!strings.Contains(page, `data-op="brightness" data-value="50"`) {
		t.Errorf("%s", page)
	}
}

func TestTemplate_ANameCantBecomeMarkup(t *testing.T) {
	view := livingRoom(true)
	view.Name = "<script>x</script>"

	if strings.Contains(renderDevice(t, view), "<script>x") {
		t.Error("a name was printed unescaped")
	}
}

func TestTemplate_AFanOnlyDeviceHasNoLightControls(t *testing.T) {
	view := livingRoom(true)
	view.Light = nil

	page := renderDevice(t, view)
	if strings.Contains(page, `data-target="light"`) || !strings.Contains(page, `data-target="fan"`) {
		t.Errorf("%s", page)
	}
}

type fakeStore struct {
	commands []common.Command
	err      error
}

func (s *fakeStore) GetDevices() ([]data.DeviceView, error) { return nil, nil }

func (s *fakeStore) SendCommand(c common.Command) error {
	s.commands = append(s.commands, c)

	return s.err
}

func TestCommandRoute_PassesACommandOnAndRefusesWhatIsNotOne(t *testing.T) {
	store := &fakeStore{}
	h := &Handler{entityStore: store}
	post := httptest.NewRequest(http.MethodPost, "/plugins/bondbridge/command", nil)

	response, err := h.handleHttpCommandRequest(nil, post, &commandRequest{Device: "fan1", Target: "fan", Op: "speed", Value: 2})
	if err != nil || response.(commandResponse).Accepted != true {
		t.Fatalf("%v %v", response, err)
	}

	if len(store.commands) != 1 || store.commands[0] != (common.Command{Device: "fan1", Target: "fan", Op: "speed", Value: 2}) {
		t.Errorf("%+v", store.commands)
	}

	get := httptest.NewRequest(http.MethodGet, "/plugins/bondbridge/command", nil)

	if _, err := h.handleHttpCommandRequest(nil, get, &commandRequest{Device: "fan1"}); err == nil {
		t.Error("a GET was accepted")
	}

	if _, err := h.handleHttpCommandRequest(nil, post, &commandRequest{}); err == nil {
		t.Error("a command with no device was accepted")
	}

	if _, err := h.handleHttpCommandRequest(nil, post, nil); err == nil {
		t.Error("no command at all was accepted")
	}

	store.err = errors.New("the bridge isn't answering")

	if _, err := h.handleHttpCommandRequest(nil, post, &commandRequest{Device: "fan1"}); err == nil {
		t.Error("a refusal was reported as accepted")
	}
}
