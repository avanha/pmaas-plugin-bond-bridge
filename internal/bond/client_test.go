package bond

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A bridge with one fan, answering as the real one did.
func fakeBridge(t *testing.T, onAction func(path string, body string)) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("BOND-Token") != "secret" {
			http.Error(w, `{"_error_msg":"Invalid token"}`, http.StatusUnauthorized)
			return
		}

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/devices":
			_, _ = io.WriteString(w, `{"_":"d438ad30","__":"00000000","05dd38b9":{"_":"424dd10b"}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/devices/05dd38b9":
			_, _ = io.WriteString(w, `{"name":"Living Room Ceiling Fan","type":"CF","location":"Living Room","actions":["SetSpeed","TurnOn"],"_":"x"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/devices/05dd38b9/properties":
			_, _ = io.WriteString(w, `{"addr":"11010","max_speed":3,"trust_state":false}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/devices/05dd38b9/state":
			_, _ = io.WriteString(w, `{"power":0,"speed":1,"direction":-1,"light":0,"down_light":1,"_lock_priority":100}`)
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/v2/devices/05dd38b9/actions/"):
			body, _ := io.ReadAll(r.Body)
			onAction(r.URL.Path, string(body))
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestClient_ListsDevicesWithTheirDetails(t *testing.T) {
	server := fakeBridge(t, nil)
	defer server.Close()

	devices, err := NewClient(server.URL, "secret", time.Second).Devices(context.Background())
	if err != nil || len(devices) != 1 {
		t.Fatalf("%v %v", devices, err)
	}

	d := devices[0]
	if d.ID != "05dd38b9" || d.Name != "Living Room Ceiling Fan" || d.Type != "CF" || d.Location != "Living Room" || d.MaxSpeed != 3 || !d.Has("SetSpeed") || d.Has("SetBrightness") {
		t.Errorf("%+v", d)
	}
}

func TestClient_StateKeepsWhatTheDeviceDoesntHaveNil(t *testing.T) {
	server := fakeBridge(t, nil)
	defer server.Close()

	state, err := NewClient(server.URL, "secret", time.Second).State(context.Background(), "05dd38b9")
	if err != nil {
		t.Fatal(err)
	}

	if *state.Power != 0 || *state.Speed != 1 || *state.Direction != -1 || *state.DownLight != 1 || state.UpLight != nil || state.Brightness != nil {
		t.Errorf("%+v", state)
	}
}

func TestClient_ActionsAreSentWithTheirArgumentOnlyIfTheyHaveOne(t *testing.T) {
	var got []string

	server := fakeBridge(t, func(path string, body string) { got = append(got, path+" "+body) })
	defer server.Close()

	client := NewClient(strings.TrimPrefix(server.URL, "http://"), "secret", time.Second)
	two := 2

	if err := client.Do(context.Background(), "05dd38b9", "TurnOn", nil); err != nil {
		t.Fatal(err)
	}

	if err := client.Do(context.Background(), "05dd38b9", "SetSpeed", &two); err != nil {
		t.Fatal(err)
	}

	want := []string{`/v2/devices/05dd38b9/actions/TurnOn {}`, `/v2/devices/05dd38b9/actions/SetSpeed {"argument":2}`}
	encoded, _ := json.Marshal(got)
	expected, _ := json.Marshal(want)

	if string(encoded) != string(expected) {
		t.Errorf("%s", encoded)
	}
}

func TestClient_AFailureSaysWhatTheBridgeAnswered(t *testing.T) {
	server := fakeBridge(t, nil)
	defer server.Close()

	_, err := NewClient(server.URL, "wrong", time.Second).State(context.Background(), "05dd38b9")
	if err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "Invalid token") {
		t.Errorf("%v", err)
	}

	if strings.Contains(err.Error(), "wrong") {
		t.Error("the token is in the error")
	}

	if err := NewClient(server.URL, "secret", time.Second).Do(context.Background(), "nope", "TurnOn", nil); err == nil {
		t.Error("an action on a device that isn't there worked")
	}
}
