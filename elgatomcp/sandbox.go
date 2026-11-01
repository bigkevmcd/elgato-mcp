package elgatomcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/bigkevmcd/elgato-mcp/elgato"
)

type sandboxFile struct {
	Lights []sandboxLight `json:"lights"`
}

type sandboxLight struct {
	Name              string `json:"name"`
	Product           string `json:"product,omitempty"`
	On                bool   `json:"on"`
	Brightness        int    `json:"brightness"`
	TemperatureKelvin int    `json:"temperature_kelvin"`
}

type memoryLight struct {
	mu    sync.Mutex
	info  elgato.AccessoryInfo
	state elgato.State
}

func (light *memoryLight) AccessoryInfo(ctx context.Context) (*elgato.AccessoryInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	info := light.info

	return &info, nil
}

func (light *memoryLight) State(ctx context.Context) (elgato.State, error) {
	if err := ctx.Err(); err != nil {
		return elgato.State{}, err
	}

	light.mu.Lock()
	defer light.mu.Unlock()

	return light.state, nil
}

func (light *memoryLight) SetState(ctx context.Context, state elgato.State) (elgato.State, error) {
	if err := ctx.Err(); err != nil {
		return elgato.State{}, err
	}

	light.mu.Lock()
	defer light.mu.Unlock()

	light.state = state

	return light.state, nil
}

// NewSandbox loads simulated lights from path. State changes are held in
// memory and are reset when a new sandbox is created.
func NewSandbox(path string) (*Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read sandbox data: %w", err)
	}

	var seed sandboxFile
	if err := json.Unmarshal(data, &seed); err != nil {
		return nil, fmt.Errorf("decode sandbox data %q: %w", path, err)
	}

	lights := make([]Light, 0, len(seed.Lights))

	seen := make(map[string]bool, len(seed.Lights))
	for _, entry := range seed.Lights {
		if strings.TrimSpace(entry.Name) == "" {
			return nil, errors.New("sandbox light name must not be empty")
		}

		name := strings.TrimSpace(entry.Name)

		key := strings.ToLower(name)
		if seen[key] {
			return nil, fmt.Errorf("duplicate sandbox light name %q", name)
		}

		seen[key] = true

		if entry.Brightness < elgato.MinBrightness || entry.Brightness > elgato.MaxBrightness {
			return nil, fmt.Errorf("sandbox light %q brightness out of range %d-%d", name, elgato.MinBrightness, elgato.MaxBrightness)
		}

		if entry.TemperatureKelvin < elgato.MinKelvin || entry.TemperatureKelvin > elgato.MaxKelvin {
			return nil, fmt.Errorf("sandbox light %q temperature_kelvin out of range %d-%d", name, elgato.MinKelvin, elgato.MaxKelvin)
		}

		on := 0
		if entry.On {
			on = 1
		}

		product := entry.Product
		if product == "" {
			product = "Elgato Key Light"
		}

		lights = append(lights, Light{
			Name:    name,
			Address: "sandbox:" + name,
			Client: &memoryLight{
				info:  elgato.AccessoryInfo{ProductName: product, DisplayName: name},
				state: elgato.State{On: on, Brightness: entry.Brightness, Temperature: elgato.KelvinToTemperature(entry.TemperatureKelvin)},
			},
		})
	}

	registry := NewRegistry(lights, nil)
	registry.sandbox = true

	return registry, nil
}
