package bond

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLive_ReadsARealBridge reads from the bridge named by BOND_HOST and BOND_TOKEN. It only reads: it sends no action.
func TestLive_ReadsARealBridge(t *testing.T) {
	host, token := os.Getenv("BOND_HOST"), os.Getenv("BOND_TOKEN")
	if host == "" || token == "" {
		t.Skip("BOND_HOST and BOND_TOKEN are not set")
	}

	client := NewClient(host, token, 5*time.Second)

	devices, err := client.Devices(context.Background())
	if err != nil || len(devices) == 0 {
		t.Fatalf("%v %v", devices, err)
	}

	for _, d := range devices {
		state, err := client.State(context.Background(), d.ID)
		if err != nil {
			t.Fatal(err)
		}

		t.Logf("%s (%s) %s max speed %d, %d actions, power %v speed %v", d.Name, d.ID, d.Location, d.MaxSpeed, len(d.Actions), state.Power, state.Speed)
	}
}
