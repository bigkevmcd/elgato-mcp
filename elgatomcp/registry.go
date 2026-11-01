package elgatomcp

import (
	"context"
	"errors"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/bigkevmcd/elgato-mcp/elgato"
)

// DiscoverFunc finds lights on the network.
type DiscoverFunc func(ctx context.Context) ([]elgato.Device, error)

// Registry is the set of lights the server controls. It starts with the
// configured lights, and discovered lights are added to it.
type Registry struct {
	discover DiscoverFunc
	sandbox  bool

	mu     sync.RWMutex
	lights []Light
}

// NewRegistry creates a registry containing lights. If discover is nil,
// discovery is disabled.
func NewRegistry(lights []Light, discover DiscoverFunc) *Registry {
	return &Registry{lights: slices.Clone(lights), discover: discover}
}

// Lights returns the known lights.
func (r *Registry) Lights() []Light {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return slices.Clone(r.lights)
}

// CanDiscover reports whether discovery is enabled.
func (r *Registry) CanDiscover() bool {
	return r.sandbox || r.discover != nil
}

// Sandbox reports whether this registry only uses in-memory light state.
func (r *Registry) Sandbox() bool { return r.sandbox }

// Discover refreshes known lights and returns the result. Network-discovered
// lights are added to the registry.
//
// Discovered lights are named after the name set in the Elgato Control
// Center app, falling back to the DNS-SD instance name.
func (r *Registry) Discover(ctx context.Context) ([]Light, error) {
	if r.sandbox {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		return r.Lights(), nil
	}

	if r.discover == nil {
		return nil, errors.New("discovery is disabled")
	}

	devices, err := r.discover(ctx)
	if err != nil {
		return nil, err
	}

	found := make([]Light, len(devices))

	var wg sync.WaitGroup
	for i, d := range devices {
		wg.Go(func() {
			l := Light{Name: d.Instance, Address: d.Addr(), Client: elgato.NewClient(d.Addr(), nil)}
			if info, err := l.Client.AccessoryInfo(ctx); err == nil && info.DisplayName != "" {
				l.Name = info.DisplayName
			}

			found[i] = l
		})
	}

	wg.Wait()

	r.mu.Lock()
	defer r.mu.Unlock()

	for i, l := range found {
		if j := slices.IndexFunc(r.lights, func(existing Light) bool { return sameDevice(existing, devices[i]) }); j >= 0 {
			found[i] = r.lights[j]
			continue
		}

		if r.nameTaken(l.Name) {
			l.Name += " (" + devices[i].IP.String() + ")"
		}

		r.lights = append(r.lights, l)
		found[i] = l
	}

	return found, nil
}

func (r *Registry) nameTaken(name string) bool {
	return slices.ContainsFunc(r.lights, func(l Light) bool { return strings.EqualFold(l.Name, name) })
}

// sameDevice reports whether the light is configured with either the IP or
// the hostname of the discovered device.
func sameDevice(l Light, d elgato.Device) bool {
	addr := elgato.NormalizeAddr(l.Address)
	port := strconv.Itoa(d.Port)

	return strings.EqualFold(addr, d.Addr()) ||
		(d.Host != "" && strings.EqualFold(addr, net.JoinHostPort(d.Host, port)))
}
