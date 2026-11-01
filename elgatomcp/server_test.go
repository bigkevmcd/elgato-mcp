package elgatomcp

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bigkevmcd/elgato-mcp/elgato"
)

// fakeLight emulates the HTTP API of an Elgato Key Light Air.
type fakeLight struct {
	mu          sync.Mutex
	state       elgato.State
	displayName string
}

func (f *fakeLight) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	switch {
	case r.URL.Path == "/elgato/accessory-info" && r.Method == http.MethodGet:
		if err := json.NewEncoder(w).Encode(elgato.AccessoryInfo{ProductName: "Elgato Key Light Air", DisplayName: f.displayName}); err != nil {
			http.Error(w, "encoding response", http.StatusInternalServerError)
		}

		return
	case r.URL.Path == "/elgato/lights" && r.Method == http.MethodPut:
		var doc struct{ Lights []elgato.State }
		if err := json.NewDecoder(r.Body).Decode(&doc); err != nil || len(doc.Lights) != 1 {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		f.state = doc.Lights[0]
	case r.URL.Path == "/elgato/lights" && r.Method == http.MethodGet:
	default:
		http.NotFound(w, r)
		return
	}

	if err := json.NewEncoder(w).Encode(map[string]any{"numberOfLights": 1, "lights": []elgato.State{f.state}}); err != nil {
		http.Error(w, "encoding response", http.StatusInternalServerError)
	}
}

// startLight serves the fake light and returns its address.
func startLight(t *testing.T, fl *fakeLight) string {
	t.Helper()

	ts := httptest.NewServer(fl)
	t.Cleanup(ts.Close)

	return strings.TrimPrefix(ts.URL, "http://")
}

func configuredLights(t *testing.T, lights map[string]*fakeLight) []Light {
	t.Helper()

	var configured []Light

	for name, fl := range lights {
		addr := startLight(t, fl)
		configured = append(configured, Light{Name: name, Address: addr, Client: elgato.NewClient(addr, nil)})
	}

	return configured
}

func newTestSession(t *testing.T, lights map[string]*fakeLight) *mcp.ClientSession {
	t.Helper()
	return connect(t, NewRegistry(configuredLights(t, lights), nil))
}

func connect(t *testing.T, registry *Registry) *mcp.ClientSession {
	t.Helper()

	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	s := mcp.NewServer(&mcp.Implementation{Name: "test"}, nil)
	AddTools(s, registry)

	if _, err := s.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}

	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := cs.Close(); err != nil {
			t.Errorf("closing client session: %v", err)
		}
	})

	return cs
}

func callTool(t *testing.T, cs *mcp.ClientSession, name string, args any) (LightsOutput, *mcp.CallToolResult) {
	t.Helper()

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}

	var out LightsOutput

	if !res.IsError {
		b, err := json.Marshal(res.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}

		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatal(err)
		}
	}

	return out, res
}

func TestListLights(t *testing.T) {
	cs := newTestSession(t, map[string]*fakeLight{
		"desk": {state: elgato.State{On: 1, Brightness: 40, Temperature: 200}},
	})

	out, _ := callTool(t, cs, "list_lights", map[string]any{})

	if len(out.Lights) != 1 {
		t.Fatalf("got %d lights, want 1", len(out.Lights))
	}

	got := out.Lights[0]
	if got.Name != "desk" || !got.On || got.Brightness != 40 || got.TemperatureKelvin != 5000 || got.Product != "Elgato Key Light Air" {
		t.Errorf("unexpected light status: %+v", got)
	}
}

func TestSetLightsMergesState(t *testing.T) {
	desk := &fakeLight{state: elgato.State{On: 0, Brightness: 40, Temperature: 200}}
	cs := newTestSession(t, map[string]*fakeLight{"desk": desk})

	out, res := callTool(t, cs, "set_lights", map[string]any{"lights": []string{"DESK"}, "on": true})
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}

	want := elgato.State{On: 1, Brightness: 40, Temperature: 200}
	if desk.state != want {
		t.Errorf("light state = %+v, want %+v", desk.state, want)
	}

	if len(out.Lights) != 1 || !out.Lights[0].On {
		t.Errorf("unexpected output: %+v", out)
	}
}

func TestSetLightsAll(t *testing.T) {
	left := &fakeLight{state: elgato.State{On: 1, Brightness: 10, Temperature: 200}}
	right := &fakeLight{state: elgato.State{On: 0, Brightness: 90, Temperature: 300}}
	cs := newTestSession(t, map[string]*fakeLight{"left": left, "right": right})

	_, res := callTool(t, cs, "set_lights", map[string]any{"brightness": 55, "temperature_kelvin": 4000})
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}

	for name, fl := range map[string]*fakeLight{"left": left, "right": right} {
		if fl.state.Brightness != 55 || fl.state.Temperature != 250 {
			t.Errorf("%s: state = %+v, want brightness 55 and temperature 250", name, fl.state)
		}
	}

	if left.state.On != 1 || right.state.On != 0 {
		t.Errorf("power changed unexpectedly: left %d, right %d", left.state.On, right.state.On)
	}
}

func TestSetLightsErrors(t *testing.T) {
	cs := newTestSession(t, map[string]*fakeLight{"desk": {}})

	tests := map[string]map[string]any{
		"no changes":         {},
		"unknown light":      {"lights": []string{"ceiling"}, "on": true},
		"brightness too big": {"brightness": 101},
		"kelvin too low":     {"temperature_kelvin": 2000},
	}
	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			if _, res := callTool(t, cs, "set_lights", args); !res.IsError {
				t.Error("expected an error result")
			}
		})
	}
}

func TestUnreachableLight(t *testing.T) {
	ts := httptest.NewServer(http.NotFoundHandler())
	ts.Close()
	addr := strings.TrimPrefix(ts.URL, "http://")
	cs := connect(t, NewRegistry([]Light{{Name: "gone", Address: addr, Client: elgato.NewClient(addr, nil)}}, nil))

	out, res := callTool(t, cs, "list_lights", map[string]any{})
	if res.IsError || len(out.Lights) != 1 || out.Lights[0].Error == "" {
		t.Errorf("expected per-light error, got %+v", out)
	}
}

// device returns a discovered device for a light served at addr.
func device(t *testing.T, instance, addr string) elgato.Device {
	t.Helper()

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}

	p, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}

	return elgato.Device{Instance: instance, IP: net.ParseIP(host), Port: p}
}

func staticDiscovery(devices ...elgato.Device) DiscoverFunc {
	return func(context.Context) ([]elgato.Device, error) { return devices, nil }
}

func TestDiscoverLights(t *testing.T) {
	named := &fakeLight{displayName: "Desk", state: elgato.State{On: 1, Brightness: 30, Temperature: 200}}
	unnamed := &fakeLight{}
	namedAddr, unnamedAddr := startLight(t, named), startLight(t, unnamed)
	registry := NewRegistry(nil, staticDiscovery(
		device(t, "Elgato Key Light Air 1A2B", namedAddr),
		device(t, "Elgato Key Light Air 3C4D", unnamedAddr),
	))
	cs := connect(t, registry)

	out, res := callTool(t, cs, "discover_lights", map[string]any{})
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}

	if len(out.Lights) != 2 {
		t.Fatalf("got %d lights, want 2: %+v", len(out.Lights), out)
	}

	if got := out.Lights[0]; got.Name != "Desk" || got.Address != namedAddr || !got.On || got.Brightness != 30 {
		t.Error("unexpected named light:", got)
	}

	if got := out.Lights[1]; got.Name != "Elgato Key Light Air 3C4D" {
		t.Errorf("unnamed light should use the instance name, got %+v", got)
	}

	// Discovered lights can be controlled.
	if _, res := callTool(t, cs, "set_lights", map[string]any{"lights": []string{"desk"}, "on": false}); res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}

	if named.state.On != 0 {
		t.Error("light was not turned off")
	}
}

func TestDiscoverKnownAndDuplicateLights(t *testing.T) {
	configured := &fakeLight{displayName: "Desk"}
	other := &fakeLight{displayName: "Desk"}
	configuredAddr, otherAddr := startLight(t, configured), startLight(t, other)
	registry := NewRegistry(
		[]Light{{Name: "left", Address: configuredAddr, Client: elgato.NewClient(configuredAddr, nil)}},
		staticDiscovery(device(t, "a", configuredAddr), device(t, "b", otherAddr)),
	)

	// Discovering twice must not add the lights again.
	for range 2 {
		found, err := registry.Discover(context.Background())
		if err != nil {
			t.Fatal(err)
		}

		if len(found) != 2 || found[0].Name != "left" || found[1].Name != "Desk" {
			t.Error("unexpected discovered lights:", found)
		}
	}

	if lights := registry.Lights(); len(lights) != 2 {
		t.Errorf("got %d lights in the registry, want 2", len(lights))
	}

	// A different light with a name that's already taken gets a unique name.
	third := &fakeLight{displayName: "LEFT"}
	thirdAddr := startLight(t, third)
	registry.discover = staticDiscovery(device(t, "c", thirdAddr))

	found, err := registry.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if want := "LEFT (127.0.0.1)"; len(found) != 1 || found[0].Name != want {
		t.Error("got", found, "want a light named", want)
	}
}

func TestListLightsDiscoversWhenEmpty(t *testing.T) {
	addr := startLight(t, &fakeLight{displayName: "Desk"})
	cs := connect(t, NewRegistry(nil, staticDiscovery(device(t, "a", addr))))

	out, _ := callTool(t, cs, "list_lights", map[string]any{})

	if len(out.Lights) != 1 || out.Lights[0].Name != "Desk" {
		t.Error("unexpected lights:", out)
	}
}

func TestDiscoveryDisabled(t *testing.T) {
	cs := connect(t, NewRegistry(nil, nil))

	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, tool := range tools.Tools {
		if tool.Name == "discover_lights" {
			t.Error("discover_lights should not be available")
		}
	}

	if _, res := callTool(t, cs, "set_lights", map[string]any{"on": true}); !res.IsError {
		t.Error("expected an error with no lights")
	}
}

func TestSandboxTools(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lights.json")

	data := `{"lights":[{"name":"Desk","on":false,"brightness":30,"temperature_kelvin":4000}]}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	registry, err := NewSandbox(path)
	if err != nil {
		t.Fatal(err)
	}

	cs := connect(t, registry)

	out, res := callTool(t, cs, "discover_lights", map[string]any{})
	if res.IsError || len(out.Lights) != 1 || out.Lights[0].Name != "Desk" {
		t.Fatalf("sandbox discovery = %+v, %v", out, res)
	}

	_, res = callTool(t, cs, "set_lights", map[string]any{"lights": []string{"desk"}, "on": true, "brightness": 70})
	if res.IsError {
		t.Fatalf("sandbox set_lights: %+v", res)
	}

	out, res = callTool(t, cs, "list_lights", map[string]any{})
	if res.IsError || len(out.Lights) != 1 || !out.Lights[0].On || out.Lights[0].Brightness != 70 {
		t.Fatalf("sandbox list_lights after update = %+v, %v", out, res)
	}
}
