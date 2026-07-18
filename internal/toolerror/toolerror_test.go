package toolerror

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// CR-T17. internal/toolerror had no test file at all. It carries the exit-3
// contract that the GitHub Action and MCP clients branch on, and unlike
// internal/verdict nothing pinned its JSON shape — so a field rename was a
// silent breaking change to a public contract, catchable only by a consumer
// noticing their parser had stopped working.

// TestToolErrorJSONShape is the golden: these field names ARE the contract
// (INV-VERDICT-STABLE). Renaming or removing one breaks every consumer, so this
// compares the whole object rather than spot-checking keys — a spot check would
// not notice a field that quietly disappeared.
func TestToolErrorJSONShape(t *testing.T) {
	var buf bytes.Buffer
	if err := New(TargetUnavailable, "could not create a disposable database", "check the admin connection").WriteJSON(&buf); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("payload is not valid JSON: %v\n%s", err, buf.String())
	}
	want := map[string]any{
		"rowshape": "1",
		"error":    "tool_error",
		"category": "target_unavailable",
		"message":  "could not create a disposable database",
		"hint":     "check the admin connection",
	}
	if len(got) != len(want) {
		t.Errorf("payload has %d fields, want %d — a field was added or removed, which is a "+
			"contract change (INV-VERDICT-STABLE):\ngot:  %v\nwant: %v", len(got), len(want), got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("field %q = %v, want %v", k, got[k], v)
		}
	}
}

// TestToolErrorIsNeverAVerdict: the whole point of the struct. A consumer must
// be able to tell "the tool could not run" from "the migration is unsafe"
// without heuristics, so the payload carries `error` and must NOT carry
// `verdict` (PRD §10).
func TestToolErrorIsNeverAVerdict(t *testing.T) {
	var buf bytes.Buffer
	if err := New(Internal, "boom", "").WriteJSON(&buf); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["verdict"]; ok {
		t.Error("a tool error must never carry a `verdict` field")
	}
	if got["error"] != Kind {
		t.Errorf(`payload["error"] = %v, want %q`, got["error"], Kind)
	}
	for _, forbidden := range []string{"PASS", "WARN", "FAIL"} {
		if strings.Contains(buf.String(), forbidden) {
			t.Errorf("tool-error payload contains verdict word %q:\n%s", forbidden, buf.String())
		}
	}
}

// TestHintOmittedWhenEmpty pins the omitempty: an absent hint must not appear as
// an empty string, or consumers rendering "hint: " get a blank line.
func TestHintOmittedWhenEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := New(BadUsage, "no target given", "").WriteJSON(&buf); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if _, present := got["hint"]; present {
		t.Errorf("empty hint must be omitted, got %v", got)
	}
}

// TestEveryCategoryIsStable pins the wire values of every category. These
// strings are what an agent branches on to decide what to do — retry the
// environment, fix the fixture, install the runner — so changing one silently
// re-routes that decision.
func TestEveryCategoryIsStable(t *testing.T) {
	want := map[Category]string{
		TargetUnavailable: "target_unavailable",
		ConnectFailed:     "connect_failed",
		RunnerNotFound:    "runner_not_found",
		FixtureParse:      "fixture_parse",
		UnknownVersion:    "unknown_version",
		BadUsage:          "bad_usage",
		Internal:          "internal",
	}
	for cat, wire := range want {
		if string(cat) != wire {
			t.Errorf("category wire value = %q, want %q", string(cat), wire)
		}
		// Every category must also survive the round trip into the payload.
		var buf bytes.Buffer
		if err := New(cat, "m", "").WriteJSON(&buf); err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got["category"] != wire {
			t.Errorf("category %q serialized as %v", wire, got["category"])
		}
	}
	if Kind != "tool_error" || Contract != "1" {
		t.Errorf("Kind/Contract changed: %q/%q — both are on the wire", Kind, Contract)
	}
}

// TestExitCodeIsAlwaysThree: exit 3 is the contract for every category, not just
// the common ones (PRD §10). A tool error is never PASS/FAIL/WARN.
func TestExitCodeIsAlwaysThree(t *testing.T) {
	for _, cat := range []Category{
		TargetUnavailable, ConnectFailed, RunnerNotFound,
		FixtureParse, UnknownVersion, BadUsage, Internal,
	} {
		if got := New(cat, "m", "").ExitCode(); got != 3 {
			t.Errorf("ExitCode() for %q = %d, want 3", cat, got)
		}
	}
}

// TestHumanRendersTheSameStruct: one struct, two renderings — the human form is
// a rendering of the JSON, never a separate code path (INV-VERDICT-SHAPE).
func TestHumanRendersTheSameStruct(t *testing.T) {
	e := New(RunnerNotFound, "could not detect a runner", "pass --runner")
	human := e.Human()
	for _, want := range []string{"runner_not_found", "could not detect a runner", "pass --runner"} {
		if !strings.Contains(human, want) {
			t.Errorf("human rendering missing %q:\n%s", want, human)
		}
	}

	// A hintless error must not render a dangling "hint:" line.
	if h := New(Internal, "boom", "").Human(); strings.Contains(h, "hint:") {
		t.Errorf("no hint should mean no hint line, got %q", h)
	}
}

// TestToolErrorImplementsError: it flows up the call stack like any error, and
// the message names the category so a log line is self-describing.
func TestToolErrorImplementsError(t *testing.T) {
	var err error = New(FixtureParse, "bad yaml", "")
	if !strings.Contains(err.Error(), "fixture_parse") || !strings.Contains(err.Error(), "bad yaml") {
		t.Errorf("Error() = %q, want it to name both the category and the message", err.Error())
	}
}
