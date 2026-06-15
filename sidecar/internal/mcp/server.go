// Package mcp implements a Model Context Protocol (MCP) server over stdio,
// exposing the sidecar's git operations as tools that AI clients (Claude
// Desktop, Cursor, VS Code) can call.
//
// Protocol: MCP is JSON-RPC 2.0 over newline-delimited stdio — the same
// transport the sidecar already speaks for the Tauri app. The difference is
// the method set (initialize / tools/list / tools/call) and the response
// envelope (results wrapped in a `content` array). We hand-roll it to keep the
// sidecar dependency-free (stdlib only), matching the existing design.
//
// This runs ONLY when the binary is started with `--mcp`. The normal NDJSON
// path used by the Tauri app is untouched — see main.go.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// MCP protocol version this server implements. Clients send their own in
// `initialize`; we echo a compatible one back.
const protocolVersion = "2024-11-05"
const serverName = "lazy-cherry-pick"
const serverVersion = "0.17.0"

// serverInstructions is returned in the `initialize` response as a hint to
// the model: an overview of the workflow and the conflict-resolution loop, so
// a client doesn't need docs/MCP.md pasted into context to use this server
// effectively.
const serverInstructions = `This server exposes git operations (read history, sync with remotes, cherry-pick commits, resolve conflicts, push) on a local repository.

Repo resolution: pass an absolute "repo" path on any tool, or omit it to use the server's configured default repo (LCP_DEFAULT_REPO).

Suggested workflow:
1. Explore with list_branches, list_commits, compare_branches, get_commit_detail, get_file_diff to find the commits you want.
2. get_status shows the current branch, dirty state, and ahead/behind vs upstream — check it before operations that need a clean tree or before push/pull. list_remotes shows configured remote names.
3. Before cherry-picking, use the read-only tools find_already_applied (skip duplicates) and dry_run_pick (preview conflicts).
4. cherry_pick (WRITE) applies commits onto a target branch, auto-skipping already-applied ones. If a commit conflicts it returns status "needs_human_resolution" with the conflicting files and a hint — this is a normal pause, not a failure.
5. To resolve a conflict: get_conflict_content reads each conflicted file including its <<<<<<<.../=======/>>>>>>> markers. Propose a fully-merged version and show it to the user for approval BEFORE calling resolve_conflict_file (WRITE) — it refuses content that still has conflict markers. For simple "take one side entirely" cases, resolve_conflict_side (WRITE) is faster. get_staged_diff lets you review a staged resolution before continuing. Repeat for every conflicted file, then call continue_cherry_pick (WRITE) to resume. If the original cherry_pick had more commits queued, call cherry_pick again with the remaining SHAs.
6. abort_cherry_pick (WRITE) rolls back to a clean working tree at any point.
7. fetch (WRITE, network) updates remote-tracking refs; pull (WRITE) fast-forwards a local branch from its remote; push (WRITE, network) publishes a local branch to a remote — push affects shared state other people see, so always confirm the remote and branch with the user first. create_branch (WRITE) makes a new local branch without checking it out.

All WRITE tools modify the user's repository (and push/fetch also touch the remote) — always confirm with the user before calling them.`

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// ── JSON-RPC envelopes ───────────────────────────────────────────────────────

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any         `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// ── MCP-specific shapes ──────────────────────────────────────────────────────

type initializeResult struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ServerInfo      serverInfo     `json:"serverInfo"`
	Instructions    string         `json:"instructions,omitempty"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// toolDef is one entry in the tools/list response.
type toolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type toolsListResult struct {
	Tools []toolDef `json:"tools"`
}

// contentBlock is one item in a tools/call result's content array.
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// callToolResult is the tools/call response envelope. structuredContent carries
// the machine-readable payload (parsed JSON) alongside the human-readable text.
type callToolResult struct {
	Content           []contentBlock `json:"content"`
	StructuredContent any            `json:"structuredContent,omitempty"`
	IsError           bool           `json:"isError,omitempty"`
}

type callToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ── Server ───────────────────────────────────────────────────────────────────

// Server holds the registered tools and the default repo resolution config.
type Server struct {
	tools       map[string]*tool
	order       []string // preserves registration order for tools/list
	defaultRepo string   // from LCP_DEFAULT_REPO env, used when a tool omits `repo`
}

// NewServer builds a server with all git tools registered. defaultRepo (may be
// empty) is used as a fallback when a tool call omits the `repo` argument.
func NewServer(defaultRepo string) *Server {
	s := &Server{
		tools:       map[string]*tool{},
		defaultRepo: defaultRepo,
	}
	registerTools(s)
	return s
}

// Serve runs the stdio read/dispatch/write loop until EOF.
func (s *Server) Serve(ctx context.Context, in io.Reader, out, errOut io.Writer) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	enc := json.NewEncoder(out)

	for sc.Scan() {
		line := bytes.TrimPrefix(sc.Bytes(), utf8BOM)
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			_ = enc.Encode(response{
				JSONRPC: "2.0",
				Error:   &rpcError{Code: -32700, Message: "Parse error: " + err.Error()},
			})
			continue
		}
		s.dispatch(ctx, &req, enc, errOut)
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintln(errOut, "mcp: stdin scan error:", err)
		return err
	}
	return nil
}

func (s *Server) dispatch(ctx context.Context, req *request, enc *json.Encoder, errOut io.Writer) {
	// Notifications (no id) expect no response.
	isNotification := len(req.ID) == 0

	switch req.Method {
	case "initialize":
		s.reply(enc, req.ID, initializeResult{
			ProtocolVersion: protocolVersion,
			Capabilities: map[string]any{
				"tools": map[string]any{},
			},
			ServerInfo:   serverInfo{Name: serverName, Version: serverVersion},
			Instructions: serverInstructions,
		})

	case "notifications/initialized", "notifications/cancelled":
		// Client acknowledgements — nothing to send back.
		return

	case "ping":
		s.reply(enc, req.ID, map[string]any{})

	case "tools/list":
		s.reply(enc, req.ID, toolsListResult{Tools: s.toolDefs()})

	case "tools/call":
		s.handleToolCall(ctx, req, enc, errOut)

	default:
		if isNotification {
			return
		}
		s.replyError(enc, req.ID, -32601, "Method not found: "+req.Method)
	}
}

func (s *Server) handleToolCall(ctx context.Context, req *request, enc *json.Encoder, errOut io.Writer) {
	var p callToolParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		s.replyError(enc, req.ID, -32602, "Invalid params: "+err.Error())
		return
	}
	t, ok := s.tools[p.Name]
	if !ok {
		// Per MCP spec, an unknown tool is reported as a tool error (isError),
		// not a protocol error, so the model can recover.
		s.reply(enc, req.ID, callToolResult{
			Content: []contentBlock{{Type: "text", Text: "Unknown tool: " + p.Name}},
			IsError: true,
		})
		return
	}

	result, err := t.run(ctx, s, p.Arguments)
	if err != nil {
		fmt.Fprintf(errOut, "[MCP] tool %s error: %v\n", p.Name, err)
		s.reply(enc, req.ID, callToolResult{
			Content: []contentBlock{{Type: "text", Text: "Error: " + err.Error()}},
			IsError: true,
		})
		return
	}
	s.reply(enc, req.ID, result)
}

func (s *Server) reply(enc *json.Encoder, id json.RawMessage, result any) {
	_ = enc.Encode(response{JSONRPC: "2.0", ID: id, Result: result})
}

func (s *Server) replyError(enc *json.Encoder, id json.RawMessage, code int, msg string) {
	_ = enc.Encode(response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}})
}

func (s *Server) toolDefs() []toolDef {
	defs := make([]toolDef, 0, len(s.order))
	for _, name := range s.order {
		t := s.tools[name]
		defs = append(defs, toolDef{
			Name:        t.name,
			Description: t.description,
			InputSchema: t.inputSchema,
		})
	}
	return defs
}
