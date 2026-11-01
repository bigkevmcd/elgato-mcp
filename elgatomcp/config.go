package elgatomcp

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/bigkevmcd/elgato-mcp/elgato"
)

// DefaultDiscoveryTimeout is how long discovery waits for lights to respond
// by default.
const DefaultDiscoveryTimeout = 3 * time.Second

// Config configures the lights the tools control.
type Config struct {
	// Lights is a comma-separated list of [name=]host[:port] entries, see
	// ParseLights.
	Lights string
	// Sandbox loads simulated light states from a JSON file instead of using
	// network discovery or real light clients.
	Sandbox string
	// DiscoveryTimeout is how long to wait for lights to respond to
	// discovery, 0 disables discovery.
	DiscoveryTimeout time.Duration
	// Logger receives messages about startup discovery, log.Default() is
	// used if nil.
	Logger *log.Logger
}

// RegisterFlags registers flags for the config on fs, with each flag name
// prefixed by prefix, e.g. "elgato-" gives -elgato-lights.
//
// The lights default to $ELGATO_LIGHTS.
func (c *Config) RegisterFlags(fs *flag.FlagSet, prefix string) {
	fs.StringVar(&c.Lights, prefix+"lights", os.Getenv("ELGATO_LIGHTS"),
		"comma-separated list of lights as [name=]host[:port] (defaults to $ELGATO_LIGHTS)")
	fs.StringVar(&c.Sandbox, prefix+"sandbox", "", "path to JSON data for an in-memory sandbox")
	fs.DurationVar(&c.DiscoveryTimeout, prefix+"discovery-timeout", DefaultDiscoveryTimeout,
		"how long to wait for lights to respond to discovery, 0 disables discovery")
}

// Setup creates a registry from the config.
//
// With no lights configured, the lights are discovered now so they're ready
// for the first tool call. Discovery failing isn't an error, as lights can be
// discovered later with the discover_lights tool.
func Setup(ctx context.Context, cfg Config) (*Registry, error) {
	var discover DiscoverFunc
	if cfg.DiscoveryTimeout > 0 {
		discover = func(ctx context.Context) ([]elgato.Device, error) {
			return elgato.Discover(ctx, cfg.DiscoveryTimeout)
		}
	}

	return setup(ctx, cfg, discover)
}

func setup(ctx context.Context, cfg Config, discover DiscoverFunc) (*Registry, error) {
	if strings.TrimSpace(cfg.Sandbox) != "" {
		if strings.TrimSpace(cfg.Lights) != "" {
			return nil, errors.New("sandbox cannot be combined with configured lights")
		}

		registry, err := NewSandbox(cfg.Sandbox)
		if err != nil {
			return nil, err
		}

		logger := cfg.Logger
		if logger == nil {
			logger = log.Default()
		}

		logger.Printf("sandbox mode: using in-memory lights; no light network access (path %q)", cfg.Sandbox)

		return registry, nil
	}

	lights, err := ParseLights(cfg.Lights)
	if err != nil {
		return nil, err
	}

	if len(lights) == 0 && discover == nil {
		return nil, errors.New("no lights configured and discovery is disabled, configure lights e.g. left=192.168.1.20,right=192.168.1.21")
	}

	registry := NewRegistry(lights, discover)
	if len(lights) > 0 {
		return registry, nil
	}

	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}

	found, err := registry.Discover(ctx)
	switch {
	case err != nil:
		logger.Printf("discovering lights: %v", err)
	case len(found) == 0:
		logger.Print("no lights found, they can be discovered later with the discover_lights tool")
	default:
		for _, l := range found {
			logger.Printf("discovered light %q at %s", l.Name, l.Address)
		}
	}

	return registry, nil
}

// ParseLights parses a comma-separated list of [name=]host[:port] entries.
// Lights without an explicit name are named after their host. An empty
// string configures no lights.
func ParseLights(s string) ([]Light, error) {
	var lights []Light

	seen := map[string]bool{}

	for entry := range strings.SplitSeq(s, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		name, addr, ok := strings.Cut(entry, "=")
		if !ok {
			name, addr = entry, entry
		}

		name, addr = strings.TrimSpace(name), strings.TrimSpace(addr)
		if name == "" || addr == "" {
			return nil, fmt.Errorf("invalid light %q, expected [name=]host[:port]", entry)
		}

		if seen[strings.ToLower(name)] {
			return nil, fmt.Errorf("duplicate light name %q", name)
		}

		seen[strings.ToLower(name)] = true
		lights = append(lights, Light{Name: name, Address: addr, Client: elgato.NewClient(addr, nil)})
	}

	return lights, nil
}
