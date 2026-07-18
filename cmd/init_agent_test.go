package cmd

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readJSON parses a config file written by init --agent.
func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("%s is not valid JSON: %v\n%s", path, err, data)
	}
	return out
}

// serverIn digs the rowshape entry out of a client config, failing if absent.
func serverIn(t *testing.T, cfg map[string]any, key string) map[string]any {
	t.Helper()
	servers, ok := cfg[key].(map[string]any)
	if !ok {
		t.Fatalf("config has no %q object: %v", key, cfg)
	}
	entry, ok := servers[mcpServerName].(map[string]any)
	if !ok {
		t.Fatalf("config does not register %q under %q: %v", mcpServerName, key, servers)
	}
	return entry
}

// mkdir creates a marker directory.
func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestInitAgentWritesPerClientConfig: each detected client gets a valid config at
// ITS OWN path, in ITS OWN format (PRD §8.3 item 1). The formats have quietly
// diverged — VS Code keys servers under `servers` and wants an explicit stdio
// type, everyone else uses `mcpServers` — and a file in the wrong shape is
// silently ignored by the client, which looks exactly like rowshape not working.
func TestInitAgentWritesPerClientConfig(t *testing.T) {
	cases := []struct {
		name      string
		marker    string // repo dir that indicates the client
		path      string // where its config must land
		key       string // the root key it must use
		stdioType bool   // whether the entry needs "type": "stdio"
	}{
		{"claude code", ".claude", ".mcp.json", "mcpServers", false},
		{"cursor", ".cursor", filepath.Join(".cursor", "mcp.json"), "mcpServers", false},
		{"vscode", ".vscode", filepath.Join(".vscode", "mcp.json"), "servers", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("CLAUDECODE", "") // don't let the ambient agent env leak into detection
			mkdir(t, filepath.Join(dir, c.marker))

			if err := runInitAgent(dir, false, io.Discard); err != nil {
				t.Fatalf("init --agent: %v", err)
			}

			cfg := readJSON(t, filepath.Join(dir, c.path))
			entry := serverIn(t, cfg, c.key)

			if entry["command"] != "rowshape" {
				t.Errorf("command = %v, want rowshape (bare name: these files get committed, an absolute path breaks the rest of the team)", entry["command"])
			}
			args, _ := entry["args"].([]any)
			if len(args) != 1 || args[0] != "mcp" {
				t.Errorf("args = %v, want [mcp] — the server is a subcommand of the same binary (PRD §8.2)", args)
			}
			if c.stdioType && entry["type"] != "stdio" {
				t.Errorf("%s needs an explicit transport type, got %v", c.name, entry["type"])
			}
		})
	}
}

// TestInitAgentMergesIntoExistingConfig: the repo already has other MCP servers
// and other settings. Registering rowshape must not cost the user any of them —
// this is the failure that would make `init --agent` untrustworthy, and it is
// silent until someone notices their github server vanished.
func TestInitAgentMergesIntoExistingConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDECODE", "")
	existing := `{
  "mcpServers": {
    "github": {"command": "gh-mcp", "args": ["serve"], "env": {"TOKEN": "${env:GH}"}}
  },
  "someOtherTopLevelKey": {"keep": "me"}
}`
	if err := os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := runInitAgent(dir, false, io.Discard); err != nil {
		t.Fatalf("init --agent: %v", err)
	}

	cfg := readJSON(t, filepath.Join(dir, ".mcp.json"))
	serverIn(t, cfg, "mcpServers") // rowshape landed

	servers := cfg["mcpServers"].(map[string]any)
	gh, ok := servers["github"].(map[string]any)
	if !ok {
		t.Fatal("init --agent destroyed the user's other MCP server")
	}
	if gh["command"] != "gh-mcp" {
		t.Errorf("the github server was altered: %v", gh)
	}
	if env, ok := gh["env"].(map[string]any); !ok || env["TOKEN"] != "${env:GH}" {
		t.Errorf("the github server's env was altered: %v", gh["env"])
	}
	if other, ok := cfg["someOtherTopLevelKey"].(map[string]any); !ok || other["keep"] != "me" {
		t.Errorf("an unrelated top-level key was dropped: %v", cfg["someOtherTopLevelKey"])
	}
}

// TestInitAgentIsIdempotent: this runs again on every upgrade, forever. A second
// run must not duplicate the entry, and — since our entry is already correct —
// must not touch the file at all.
func TestInitAgentIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDECODE", "")
	path := filepath.Join(dir, ".mcp.json")

	if err := runInitAgent(dir, false, io.Discard); err != nil {
		t.Fatalf("first init --agent: %v", err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := runInitAgent(dir, false, &out); err != nil {
		t.Fatalf("second init --agent: %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if string(first) != string(second) {
		t.Errorf("re-running init --agent rewrote the config:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
	if !strings.Contains(out.String(), "already registers") {
		t.Errorf("a no-op re-run should say so, got: %s", out.String())
	}

	// The entry exists exactly once — the merge replaced a key, it did not append.
	cfg := readJSON(t, path)
	servers := cfg["mcpServers"].(map[string]any)
	if len(servers) != 1 {
		t.Errorf("expected exactly 1 server after two runs, got %d: %v", len(servers), servers)
	}
}

// TestInitAgentUpdatesAStaleEntry: an entry left by an older rowshape (or edited
// into something wrong) gets corrected in place, without disturbing its neighbors.
// Idempotent must mean "converges on correct", not "never touches an existing key".
func TestInitAgentUpdatesAStaleEntry(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDECODE", "")
	stale := `{"mcpServers": {"rowshape": {"command": "/opt/old/rowshape", "args": ["serve"]}, "other": {"command": "x"}}}`
	if err := os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := runInitAgent(dir, false, io.Discard); err != nil {
		t.Fatalf("init --agent: %v", err)
	}

	entry := serverIn(t, readJSON(t, filepath.Join(dir, ".mcp.json")), "mcpServers")
	if entry["command"] != "rowshape" {
		t.Errorf("a stale command should be corrected, got %v", entry["command"])
	}
	args, _ := entry["args"].([]any)
	if len(args) != 1 || args[0] != "mcp" {
		t.Errorf("stale args should be corrected, got %v", args)
	}
	servers := readJSON(t, filepath.Join(dir, ".mcp.json"))["mcpServers"].(map[string]any)
	if _, ok := servers["other"]; !ok {
		t.Error("correcting our own entry dropped a neighbor")
	}
}

// TestInitAgentRefusesUnparseableConfig: a config we cannot parse is one we cannot
// safely rewrite — it may be JSONC, or hand-broken. Clobbering it would destroy
// the user's other servers, so we refuse, exit 3, and print the entry to paste.
func TestInitAgentRefusesUnparseableConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDECODE", "")
	jsonc := "{\n  // a comment makes this JSONC, not JSON\n  \"mcpServers\": {\"other\": {\"command\": \"x\"}}\n}"
	path := filepath.Join(dir, ".mcp.json")
	if err := os.WriteFile(path, []byte(jsonc), 0o644); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	err := runInitAgent(dir, false, &out)
	if err == nil {
		t.Fatal("init --agent should fail rather than clobber a config it cannot parse")
	}

	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != jsonc {
		t.Errorf("the unparseable config was modified:\n%s", after)
	}
	if !strings.Contains(out.String(), "by hand") || !strings.Contains(out.String(), `"command": "rowshape"`) {
		t.Errorf("a refusal must tell the user what to do instead, got: %s", out.String())
	}
}

// TestInitAgentDetectsMultipleClients: a Cursor user and a Claude Code user in one
// repo is ordinary. Both get wired; neither has to know the other exists.
func TestInitAgentDetectsMultipleClients(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDECODE", "")
	mkdir(t, filepath.Join(dir, ".claude"))
	mkdir(t, filepath.Join(dir, ".cursor"))

	if err := runInitAgent(dir, false, io.Discard); err != nil {
		t.Fatalf("init --agent: %v", err)
	}

	serverIn(t, readJSON(t, filepath.Join(dir, ".mcp.json")), "mcpServers")
	serverIn(t, readJSON(t, filepath.Join(dir, ".cursor", "mcp.json")), "mcpServers")

	// VS Code was not detected, so it gets no config — init writes for the clients
	// in use, it does not litter.
	if _, err := os.Stat(filepath.Join(dir, ".vscode", "mcp.json")); !os.IsNotExist(err) {
		t.Error("init --agent wrote a config for an undetected client")
	}
}

// TestInitAgentFallsBackToDotMcpJson: a bare repo with no client markers still gets
// wired. .mcp.json is the de-facto standard and is committable — a config waiting
// for the agent that shows up tomorrow beats no config at all.
func TestInitAgentFallsBackToDotMcpJson(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDECODE", "")

	if err := runInitAgent(dir, false, io.Discard); err != nil {
		t.Fatalf("init --agent: %v", err)
	}
	serverIn(t, readJSON(t, filepath.Join(dir, ".mcp.json")), "mcpServers")
}

// TestInitWithoutAgentWritesNoMCPConfig: --agent is opt-in. Plain `init` scaffolds
// the config and touches nothing an agent reads.
func TestInitWithoutAgentWritesNoMCPConfig(t *testing.T) {
	dir := t.TempDir()
	mkdir(t, filepath.Join(dir, ".claude"))

	if err := runInit(dir, false); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".mcp.json")); !os.IsNotExist(err) {
		t.Error("plain `init` should not write MCP config; --agent is opt-in")
	}
}

// TestInitAgentScaffoldsBaseConfig: --agent extends init, it does not replace it —
// the repo still gets its rowshape.toml.
func TestInitAgentScaffoldsBaseConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDECODE", "")
	writeMarker(t, filepath.Join(dir, "alembic.ini"))

	if err := runInitAgent(dir, false, io.Discard); err != nil {
		t.Fatalf("init --agent: %v", err)
	}
	if !strings.Contains(readConfig(t, dir), `runner = "alembic"`) {
		t.Error("init --agent should still scaffold the base config with the detected runner")
	}
}

// --- CR-T25: a wrong-shaped servers key must be refused, not clobbered ------
//
// writeMCPConfig refuses to touch a file it cannot parse, precisely because
// "overwriting it destroys the user's other servers". But the type assertion on
// the servers key discarded its failure, so a key holding an array, a string, or
// null silently became an empty map and was written back — the same destruction,
// through the one path that did not refuse.
func TestWriteMCPConfigRefusesWrongShapedServersKey(t *testing.T) {
	client := supportedMCPClients[0]

	for _, tc := range []struct{ name, body string }{
		{"array", `{"` + client.Key + `": ["not-an-object"]}`},
		{"string", `{"` + client.Key + `": "not-an-object"}`},
		{"number", `{"` + client.Key + `": 42}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, client.Path)
			writeFile(t, path, tc.body)

			_, err := writeMCPConfig(dir, client)
			if err == nil {
				t.Fatal("must refuse a servers key that is not an object")
			}
			// The refusal has to be actionable: it names the file and prints the
			// entry to add by hand, like the unparseable-JSON refusal above it.
			if !strings.Contains(err.Error(), client.Path) || !strings.Contains(err.Error(), mcpServerName) {
				t.Errorf("refusal should name the file and the entry to add, got: %v", err)
			}

			// The user's file must be byte-identical afterwards.
			after, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(after) != tc.body {
				t.Errorf("file was modified despite the refusal:\n before: %s\n after:  %s", tc.body, after)
			}
		})
	}
}

// TestWriteMCPConfigStillHandlesNullAndAbsent: refusing must not become
// refusing-everything. A missing key, and an explicit null, are both "nothing
// there yet" and must still be written.
func TestWriteMCPConfigStillHandlesNullAndAbsent(t *testing.T) {
	client := supportedMCPClients[0]
	for _, tc := range []struct{ name, body string }{
		{"absent", `{"other": {"keep": true}}`},
		{"null", `{"` + client.Key + `": null}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, client.Path)
			writeFile(t, path, tc.body)

			if _, err := writeMCPConfig(dir, client); err != nil {
				t.Fatalf("must write when there is nothing to clobber, got %v", err)
			}
			out, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(out), mcpServerName) {
				t.Errorf("rowshape entry was not written:\n%s", out)
			}
		})
	}
}
