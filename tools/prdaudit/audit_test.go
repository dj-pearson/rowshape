package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPassingStoriesArtifactsExist audits prd.json: every artifact claimed by a
// story marked `passes: true` must actually be on disk.
//
// Why this is a real check and not bookkeeping: prd.json IS the loop's source of
// truth, and P0-T6's own note records a story that claimed passes:true with
// "golangci-lint 0 issues" when there were 25. A false green has happened here
// before. Running this audit for the first time found 13 stale paths — five from
// architecture that moved (internal/engine/postgres/* became internal/profile/*,
// internal/extrapolate became internal/estimate, findings/perf.go became
// rsperf.go), one from a decision that changed the implementation
// (testcontainers.go, dropped for the docker CLI per D-005), and four written in
// this session by a story that named a test file and then put the tests in an
// existing one.
//
// Skipped shapes are declared, not silently ignored: an artifact containing a
// glob or brace expansion, or ending in a parenthetical annotation like
// "cmd/pull.go (wiring)", is a description rather than a path.
func TestPassingStoriesArtifactsExist(t *testing.T) {
	root := filepath.Join("..", "..")
	raw, err := os.ReadFile(filepath.Join(root, "prd.json"))
	if err != nil {
		t.Fatalf("read prd.json: %v", err)
	}
	var doc struct {
		Phases []struct {
			ID    string `json:"id"`
			Tasks []struct {
				ID        string   `json:"id"`
				Passes    bool     `json:"passes"`
				Artifacts []string `json:"artifacts"`
			} `json:"tasks"`
		} `json:"phases"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("prd.json is not valid JSON: %v", err)
	}

	descriptive := func(a string) bool {
		return strings.ContainsAny(a, "*?[{") || strings.HasSuffix(a, ")")
	}

	var checked, skipped int
	for _, ph := range doc.Phases {
		for _, task := range ph.Tasks {
			if !task.Passes {
				continue
			}
			for _, a := range task.Artifacts {
				if descriptive(a) {
					skipped++
					continue
				}
				checked++
				if _, err := os.Stat(filepath.Join(root, a)); err != nil {
					t.Errorf("%s is marked passes:true but claims artifact %q, which does not exist. "+
						"Either the file moved (update the story) or the work is not actually done.",
						task.ID, a)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no artifacts were checked; this audit would pass over nothing")
	}
	t.Logf("verified %d artifacts of passing stories (%d descriptive entries skipped)", checked, skipped)
}

// TestPRDIsValidJSON is the cheapest possible guard on the loop's source of
// truth. It is here because this session corrupted prd.json once, by
// hand-escaping a note through a shell heredoc so a backslash-n reached the file
// as a real newline inside a JSON string — and the commit landed before anything
// noticed.
func TestPRDIsValidJSON(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "prd.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("prd.json is not valid JSON: %v", err)
	}
}
