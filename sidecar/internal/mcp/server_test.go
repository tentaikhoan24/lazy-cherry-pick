package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// roundtrip feeds one or more JSON-RPC request lines through the server and
// returns the decoded response lines (notifications produce no output).
func roundtrip(t *testing.T, defaultRepo string, requests ...string) []map[string]any {
	t.Helper()
	s := NewServer(defaultRepo)
	in := strings.NewReader(strings.Join(requests, "\n") + "\n")
	var out bytes.Buffer
	var errOut bytes.Buffer
	if err := s.Serve(context.Background(), in, &out, &errOut); err != nil {
		t.Fatalf("Serve error: %v", err)
	}
	var responses []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad response line %q: %v", line, err)
		}
		responses = append(responses, m)
	}
	return responses
}

func TestInitialize(t *testing.T) {
	resp := roundtrip(t, "",
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{}}}`)
	if len(resp) != 1 {
		t.Fatalf("expected 1 response, got %d", len(resp))
	}
	result, ok := resp[0]["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result in initialize response: %v", resp[0])
	}
	if result["protocolVersion"] != protocolVersion {
		t.Errorf("protocolVersion = %v, want %v", result["protocolVersion"], protocolVersion)
	}
	caps, _ := result["capabilities"].(map[string]any)
	if _, hasTools := caps["tools"]; !hasTools {
		t.Errorf("capabilities missing tools: %v", caps)
	}
	instructions, _ := result["instructions"].(string)
	if instructions == "" {
		t.Errorf("expected non-empty instructions")
	}
}

func TestNotificationProducesNoResponse(t *testing.T) {
	resp := roundtrip(t, "",
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if len(resp) != 0 {
		t.Fatalf("notification should produce no response, got %d: %v", len(resp), resp)
	}
}

func TestToolsList(t *testing.T) {
	resp := roundtrip(t, "",
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	result, ok := resp[0]["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %v", resp[0])
	}
	tools, ok := result["tools"].([]any)
	if !ok {
		t.Fatalf("tools not an array: %v", result["tools"])
	}
	if len(tools) < 20 {
		t.Errorf("expected >=20 tools, got %d", len(tools))
	}
	// Every tool must have name, description, inputSchema.
	seen := map[string]bool{}
	for _, ti := range tools {
		td := ti.(map[string]any)
		name, _ := td["name"].(string)
		if name == "" {
			t.Errorf("tool with empty name: %v", td)
		}
		if td["description"] == nil || td["inputSchema"] == nil {
			t.Errorf("tool %q missing description/inputSchema", name)
		}
		seen[name] = true
	}
	for _, must := range []string{
		"list_commits", "cherry_pick", "resolve_conflict_file", "continue_cherry_pick",
		"get_status", "list_remotes", "get_file_diff", "get_staged_diff",
		"fetch", "pull", "push", "create_branch", "resolve_conflict_side",
	} {
		if !seen[must] {
			t.Errorf("missing expected tool %q", must)
		}
	}
}

func TestUnknownToolIsToolError(t *testing.T) {
	resp := roundtrip(t, "",
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"does_not_exist","arguments":{}}}`)
	result, ok := resp[0]["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result (tool error), got %v", resp[0])
	}
	if result["isError"] != true {
		t.Errorf("expected isError=true for unknown tool, got %v", result["isError"])
	}
}

func TestUnknownMethodIsProtocolError(t *testing.T) {
	resp := roundtrip(t, "",
		`{"jsonrpc":"2.0","id":4,"method":"no/such/method"}`)
	if resp[0]["error"] == nil {
		t.Errorf("expected protocol error for unknown method, got %v", resp[0])
	}
}

func TestResolveRepoFallback(t *testing.T) {
	s := NewServer("/some/default/path")
	got, err := s.resolveRepo("")
	if err != nil || got != "/some/default/path" {
		t.Errorf("resolveRepo('') = %q, %v; want default path", got, err)
	}
	got, err = s.resolveRepo("/explicit")
	if err != nil || got != "/explicit" {
		t.Errorf("resolveRepo('/explicit') = %q, %v; want explicit", got, err)
	}
	s2 := NewServer("")
	if _, err := s2.resolveRepo(""); err == nil {
		t.Errorf("resolveRepo('') with no default should error")
	}
}

func TestConflictMarkerGuard(t *testing.T) {
	cases := []struct {
		content string
		marker  string
		want    bool
	}{
		{"<<<<<<< HEAD\nfoo", "<<<<<<<", true},
		{"line1\n=======\nline2", "=======", true},
		{"clean merged content\nno markers", "<<<<<<<", false},
		{"a ======= inline not at col0", "=======", false},
	}
	for _, c := range cases {
		if got := containsLineMarker(c.content, c.marker); got != c.want {
			t.Errorf("containsLineMarker(%q, %q) = %v, want %v", c.content, c.marker, got, c.want)
		}
	}
}
