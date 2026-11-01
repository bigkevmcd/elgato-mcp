package main

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bigkevmcd/elgato-mcp/elgatomcp"
)

func TestSandboxServerIdentity(t *testing.T) {
	registry, err := elgatomcp.NewSandbox("data/sandbox.json")
	if err != nil {
		t.Fatal(err)
	}

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := newServer(registry).Connect(context.Background(), serverTransport, nil); err != nil {
		t.Fatal(err)
	}

	session, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Errorf("closing client session: %v", err)
		}
	})

	result := session.InitializeResult()
	if result.ServerInfo.Name != "elgato-sandbox" || !strings.Contains(result.Instructions, "SANDBOX") {
		t.Fatalf("sandbox identity = %+v", result)
	}
}
