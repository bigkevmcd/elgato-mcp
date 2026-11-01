package elgatomcp

import (
	"bytes"
	"context"
	"flag"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bigkevmcd/elgato-mcp/elgato"
)

func TestParseLights(t *testing.T) {
	lights, err := ParseLights(" left=192.168.1.20 , 192.168.1.21:9124,")
	if err != nil {
		t.Fatal(err)
	}

	if len(lights) != 2 {
		t.Fatalf("got %d lights, want 2", len(lights))
	}

	if lights[0].Name != "left" || lights[0].Address != "192.168.1.20" {
		t.Errorf("lights[0] = %+v", lights[0])
	}

	if lights[1].Name != "192.168.1.21:9124" || lights[1].Address != "192.168.1.21:9124" {
		t.Errorf("lights[1] = %+v", lights[1])
	}
}

func TestParseLightsErrors(t *testing.T) {
	for _, s := range []string{"=1.2.3.4", "a=", "a=1.2.3.4,A=1.2.3.5"} {
		if _, err := ParseLights(s); err == nil {
			t.Errorf("ParseLights(%q): expected error", s)
		}
	}
}

func TestParseLightsEmpty(t *testing.T) {
	lights, err := ParseLights(" , ")
	if err != nil || len(lights) != 0 {
		t.Errorf("ParseLights() = %v, %v, want no lights", lights, err)
	}
}

func TestRegisterFlags(t *testing.T) {
	t.Setenv("ELGATO_LIGHTS", "desk=192.168.1.20")

	var cfg Config

	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cfg.RegisterFlags(fs, "elgato-")

	if err := fs.Parse([]string{"-elgato-discovery-timeout", "5s"}); err != nil {
		t.Fatal(err)
	}

	if cfg.Lights != "desk=192.168.1.20" || cfg.DiscoveryTimeout != 5*time.Second {
		t.Errorf("unexpected config: %+v", cfg)
	}
}

func TestSetupStaticLights(t *testing.T) {
	discover := func(context.Context) ([]elgato.Device, error) {
		t.Fatal("discovery should not run when lights are configured")
		return nil, nil
	}

	registry, err := setup(context.Background(), Config{Lights: "desk=192.168.1.20"}, discover)
	if err != nil {
		t.Fatal(err)
	}

	if lights := registry.Lights(); len(lights) != 1 || lights[0].Name != "desk" {
		t.Errorf("unexpected lights: %+v", lights)
	}
}

func TestSetupDiscovers(t *testing.T) {
	addr := startLight(t, &fakeLight{displayName: "Desk"})

	var logs bytes.Buffer

	cfg := Config{Logger: log.New(&logs, "", 0)}

	registry, err := setup(context.Background(), cfg, staticDiscovery(device(t, "a", addr)))
	if err != nil {
		t.Fatal(err)
	}

	if lights := registry.Lights(); len(lights) != 1 || lights[0].Name != "Desk" {
		t.Errorf("unexpected lights: %+v", lights)
	}

	if !strings.Contains(logs.String(), `discovered light "Desk"`) {
		t.Errorf("unexpected logs: %q", logs.String())
	}
}

func TestSetupNoLightsOrDiscovery(t *testing.T) {
	if _, err := Setup(context.Background(), Config{}); err == nil {
		t.Error("expected an error")
	}
}

func TestSetupSandbox(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lights.json")

	data := `{"lights":[{"name":"Desk","product":"Key Light Air","on":true,"brightness":30,"temperature_kelvin":4000}]}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	discover := func(context.Context) ([]elgato.Device, error) {
		t.Fatal("sandbox setup called live discovery")
		return nil, nil
	}

	var logs bytes.Buffer

	registry, err := setup(context.Background(), Config{Sandbox: path, Logger: log.New(&logs, "", 0)}, discover)
	if err != nil {
		t.Fatal(err)
	}

	if !registry.Sandbox() || !registry.CanDiscover() {
		t.Fatal("sandbox registry is not marked sandboxed or discoverable")
	}

	if !strings.Contains(logs.String(), "no light network access") {
		t.Errorf("unexpected sandbox log: %q", logs.String())
	}

	lights, err := registry.Discover(context.Background())
	if err != nil || len(lights) != 1 || lights[0].Name != "Desk" {
		t.Fatalf("sandbox discovery = %+v, %v", lights, err)
	}

	state, err := lights[0].Client.State(context.Background())
	if err != nil || state.On != 1 || state.Brightness != 30 || state.Kelvin() != 4000 {
		t.Fatalf("initial sandbox state = %+v, %v", state, err)
	}

	state.Brightness = 75
	if _, err := lights[0].Client.SetState(context.Background(), state); err != nil {
		t.Fatal(err)
	}

	reset, err := NewSandbox(path)
	if err != nil {
		t.Fatal(err)
	}

	resetState, err := reset.Lights()[0].Client.State(context.Background())
	if err != nil || resetState.Brightness != 30 {
		t.Fatalf("sandbox did not reset to file state: %+v, %v", resetState, err)
	}
}

func TestSetupSandboxValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(path, []byte(`{"lights":[{"name":"Desk","brightness":101,"temperature_kelvin":4000}]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := setup(context.Background(), Config{Sandbox: path}, nil); err == nil || !strings.Contains(err.Error(), "brightness out of range") {
		t.Fatalf("invalid sandbox state error = %v", err)
	}

	if _, err := setup(context.Background(), Config{Sandbox: path, Lights: "desk=127.0.0.1"}, nil); err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("sandbox/config conflict error = %v", err)
	}
}
