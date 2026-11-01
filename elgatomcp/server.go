// Package elgatomcp exposes Elgato lights as MCP tools.
package elgatomcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bigkevmcd/elgato-mcp/elgato"
)

// LightClient is implemented by live and in-memory light clients.
type LightClient interface {
	AccessoryInfo(context.Context) (*elgato.AccessoryInfo, error)
	State(context.Context) (elgato.State, error)
	SetState(context.Context, elgato.State) (elgato.State, error)
}

// Light is a named light that the server controls.
type Light struct {
	Name    string
	Address string
	Client  LightClient
}

// LightStatus is the reported state of a light.
type LightStatus struct {
	Name              string `json:"name"`
	Address           string `json:"address"`
	Product           string `json:"product,omitempty"`
	On                bool   `json:"on"`
	Brightness        int    `json:"brightness" jsonschema:"brightness percentage 0-100"`
	TemperatureKelvin int    `json:"temperature_kelvin" jsonschema:"colour temperature in Kelvin"`
	Error             string `json:"error,omitempty" jsonschema:"set if the light could not be reached or updated"`
}

// LightsOutput is the result of the tools.
type LightsOutput struct {
	Lights []LightStatus `json:"lights"`
}

type listLightsInput struct{}

type discoverLightsInput struct{}

type setLightsInput struct {
	Lights            []string `json:"lights,omitempty" jsonschema:"names of the lights to change; all lights if omitted"`
	On                *bool    `json:"on,omitempty" jsonschema:"turn the lights on (true) or off (false)"`
	Brightness        *int     `json:"brightness,omitempty" jsonschema:"brightness percentage, 0-100"`
	TemperatureKelvin *int     `json:"temperature_kelvin,omitempty" jsonschema:"colour temperature in Kelvin, 2900 (warm) to 7000 (cool)"`
}

// AddTools adds the tools for controlling the lights in the registry to s.
// The discover_lights tool is only added if the registry can discover lights.
func AddTools(s *mcp.Server, registry *Registry) {
	h := &handlers{registry: registry}

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_lights",
		Description: "List the configured Elgato lights with their current power, brightness and colour temperature.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, h.listLights)

	mcp.AddTool(s, &mcp.Tool{
		Name: "set_lights",
		Description: "Change the power, brightness and/or colour temperature of one or more Elgato lights. " +
			"Only the provided settings are changed. Returns the resulting state of each light.",
	}, h.setLights)

	if registry.CanDiscover() {
		mcp.AddTool(s, &mcp.Tool{
			Name: "discover_lights",
			Description: "Search the local network for Elgato lights and add any new ones to the lights that can be controlled. " +
				"Returns the state of every light found.",
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
		}, h.discoverLights)
	}
}

type handlers struct {
	registry *Registry
}

func (h *handlers) listLights(ctx context.Context, _ *mcp.CallToolRequest, _ listLightsInput) (*mcp.CallToolResult, LightsOutput, error) {
	lights := h.registry.Lights()
	if len(lights) == 0 && h.registry.CanDiscover() {
		var err error
		if lights, err = h.registry.Discover(ctx); err != nil {
			return nil, LightsOutput{}, fmt.Errorf("discovering lights: %w", err)
		}
	}

	return nil, LightsOutput{Lights: statuses(ctx, lights)}, nil
}

func (h *handlers) discoverLights(ctx context.Context, _ *mcp.CallToolRequest, _ discoverLightsInput) (*mcp.CallToolResult, LightsOutput, error) {
	lights, err := h.registry.Discover(ctx)
	if err != nil {
		return nil, LightsOutput{}, fmt.Errorf("discovering lights: %w", err)
	}

	return nil, LightsOutput{Lights: statuses(ctx, lights)}, nil
}

func statuses(ctx context.Context, lights []Light) []LightStatus {
	return forEach(lights, func(l Light) LightStatus {
		st := LightStatus{Name: l.Name, Address: l.Address}

		state, err := l.Client.State(ctx)
		if err != nil {
			st.Error = err.Error()
			return st
		}

		if info, err := l.Client.AccessoryInfo(ctx); err == nil {
			st.Product = info.ProductName
		}

		return withState(st, state)
	})
}

func (h *handlers) setLights(ctx context.Context, _ *mcp.CallToolRequest, in setLightsInput) (*mcp.CallToolResult, LightsOutput, error) {
	if in.On == nil && in.Brightness == nil && in.TemperatureKelvin == nil {
		return nil, LightsOutput{}, errors.New("at least one of on, brightness or temperature_kelvin must be provided")
	}

	if b := in.Brightness; b != nil && (*b < elgato.MinBrightness || *b > elgato.MaxBrightness) {
		return nil, LightsOutput{}, fmt.Errorf("brightness %d out of range %d-%d", *b, elgato.MinBrightness, elgato.MaxBrightness)
	}

	if k := in.TemperatureKelvin; k != nil && (*k < elgato.MinKelvin || *k > elgato.MaxKelvin) {
		return nil, LightsOutput{}, fmt.Errorf("temperature_kelvin %d out of range %d-%d", *k, elgato.MinKelvin, elgato.MaxKelvin)
	}

	targets, err := h.selectLights(in.Lights)
	if err != nil {
		return nil, LightsOutput{}, err
	}

	out := forEach(targets, func(l Light) LightStatus {
		st := LightStatus{Name: l.Name, Address: l.Address}
		// The device expects a complete state, so merge the changes into the
		// current state.
		state, err := l.Client.State(ctx)
		if err != nil {
			st.Error = err.Error()
			return st
		}

		if in.On != nil {
			state.On = 0
			if *in.On {
				state.On = 1
			}
		}

		if in.Brightness != nil {
			state.Brightness = *in.Brightness
		}

		if in.TemperatureKelvin != nil {
			state.Temperature = elgato.KelvinToTemperature(*in.TemperatureKelvin)
		}

		state, err = l.Client.SetState(ctx, state)
		if err != nil {
			st.Error = err.Error()
			return st
		}

		return withState(st, state)
	})

	return nil, LightsOutput{Lights: out}, nil
}

func (h *handlers) selectLights(names []string) ([]Light, error) {
	lights := h.registry.Lights()
	if len(lights) == 0 {
		return nil, errors.New("no lights are known, configure them with -lights or use discover_lights")
	}

	if len(names) == 0 {
		return lights, nil
	}

	var selected []Light

	for _, name := range names {
		found := false

		for _, l := range lights {
			if strings.EqualFold(l.Name, name) {
				selected = append(selected, l)
				found = true

				break
			}
		}

		if !found {
			return nil, fmt.Errorf("unknown light %q, known lights: %s", name, strings.Join(lightNames(lights), ", "))
		}
	}

	return selected, nil
}

func lightNames(lights []Light) []string {
	names := make([]string, len(lights))
	for i, l := range lights {
		names[i] = l.Name
	}

	return names
}

func withState(st LightStatus, s elgato.State) LightStatus {
	st.On = s.On == 1
	st.Brightness = s.Brightness
	st.TemperatureKelvin = s.Kelvin()

	return st
}

// forEach calls f concurrently for each light, preserving the order of the
// results.
func forEach(lights []Light, f func(Light) LightStatus) []LightStatus {
	out := make([]LightStatus, len(lights))

	var wg sync.WaitGroup
	for i, l := range lights {
		wg.Go(func() { out[i] = f(l) })
	}

	wg.Wait()

	return out
}
