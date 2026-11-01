// Command elgato-mcp is an MCP server for controlling Elgato Key Light Air
// lights over stdio.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bigkevmcd/elgato-mcp/elgatomcp"
)

const version = "0.1.0"

func main() {
	var cfg elgatomcp.Config
	cfg.RegisterFlags(flag.CommandLine, "")
	flag.Parse()

	var sandboxSet, lightsSet bool

	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "sandbox":
			sandboxSet = true
		case "lights":
			lightsSet = true
		default:
			return
		}
	})

	if sandboxSet && strings.TrimSpace(cfg.Sandbox) == "" {
		log.Fatal("-sandbox requires a data file path")
	}

	if sandboxSet && lightsSet {
		log.Fatal("-sandbox cannot be combined with -lights")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Logs go to stderr, as stdout is the MCP transport.
	registry, err := elgatomcp.Setup(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}

	if err := newServer(registry).Run(ctx, &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}

// newServer creates an MCP server exposing the lights in the registry.
func newServer(registry *elgatomcp.Registry) *mcp.Server {
	name := "elgato-mcp"
	options := &mcp.ServerOptions{}

	if registry.Sandbox() {
		name = "elgato-sandbox"
		options.Instructions = "SANDBOX: all tools operate on in-memory lights and never contact real devices. Restart the process to reset light state. Discovery returns only the lights in the sandbox data file."
	}

	s := mcp.NewServer(&mcp.Implementation{Name: name, Version: version}, options)
	elgatomcp.AddTools(s, registry)

	return s
}
