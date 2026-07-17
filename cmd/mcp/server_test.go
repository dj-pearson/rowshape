package mcp

import (
	"context"
	"sort"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rowshape/rowshape/internal/fixture"
)

// TestServerHandshakeAndTools boots the server, completes an initialize
// handshake with an in-process reference client, and asserts the server exposes
// EXACTLY the four tools named in PRD §8.2 — no more, no fewer.
func TestServerHandshakeAndTools(t *testing.T) {
	ctx := context.Background()

	server := NewServer()
	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "0"}, nil)

	st, ct := sdk.NewInMemoryTransports()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect (handshake): %v", err)
	}
	defer func() { _ = cs.Close() }()

	// The handshake exposes the server's identity and instructions.
	init := cs.InitializeResult()
	if init == nil || init.ServerInfo == nil || init.ServerInfo.Name != serverName {
		t.Fatalf("handshake did not report the server identity: %+v", init)
	}
	if init.Instructions == "" {
		t.Error("server should advertise instructions (incl. fixture version compatibility)")
	}

	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	var got []string
	for _, tool := range res.Tools {
		got = append(got, tool.Name)
	}
	sort.Strings(got)

	want := append([]string(nil), ToolNames...)
	sort.Strings(want)

	if len(got) != len(want) {
		t.Fatalf("server exposes %d tools %v, want exactly %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("tool %d = %q, want %q (the set is closed to PRD §8.2's four)", i, got[i], want[i])
		}
	}
}

// TestInstructionsAdvertiseFixtureVersion: the server advertises the fixture
// format major it understands, so a peer knows the compatibility boundary
// (RFC §12).
func TestInstructionsAdvertiseFixtureVersion(t *testing.T) {
	if !containsVersion(instructions, fixture.FormatVersion) {
		t.Errorf("instructions must advertise rowshape_fixture major %q, got: %s", fixture.FormatVersion, instructions)
	}
}

func containsVersion(s, v string) bool {
	// version appears quoted in the instructions.
	return len(s) > 0 && (contains(s, `"`+v+`"`) || contains(s, v))
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
