package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/andreim/d9s/internal/db"
)

// rpcResponse is as much of a JSON-RPC reply as the test needs to look at.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   json.RawMessage `json:"error"`
}

// TestStdioProtocol drives the server exactly as `claude mcp add` will: raw
// newline-delimited JSON-RPC into stdin, replies out of stdout. It asserts the
// handshake, the tool surface and a real tool call — and that every byte on
// stdout is a protocol message, because one stray Println there corrupts the
// session for a real client.
func TestStdioProtocol(t *testing.T) {
	// Anything the server writes to the process's own stdout, rather than to
	// the stream it was handed, is the bug this guards against.
	strayStdout := captureStdout(t)

	stdinRead, stdinWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	var stderr bytes.Buffer

	srv, opener := newTestServer(t, Options{
		Stdin:  stdinRead,
		Stdout: stdoutWrite,
		Stderr: &stderr,
	})
	opener.driver("prod-pg").tables = []db.Table{{Name: "users", Detail: "~1200 rows"}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan error, 1)
	go func() { served <- srv.Run(ctx) }()

	frames := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"raw-test","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_tables","arguments":{"connection":"prod-pg"}}}`,
	}
	for _, frame := range frames {
		if _, err := io.WriteString(stdinWrite, frame+"\n"); err != nil {
			t.Fatalf("writing a frame: %v", err)
		}
	}

	responses := readResponses(t, stdoutRead, 3)

	// Closing stdin is how a client disconnects; the server must then stop.
	_ = stdinWrite.Close()
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("Run returned %v after the client disconnected, want a clean stop", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the server did not stop after the client closed stdin")
	}

	checkInitialize(t, responses["1"])
	checkToolList(t, responses["2"])
	checkToolCall(t, responses["3"])

	if !strings.Contains(stderr.String(), "serving") {
		t.Errorf("diagnostics did not reach stderr:\n%s", stderr.String())
	}
	if stray := strayStdout(); stray != "" {
		t.Errorf("the server wrote %q to the process stdout, which would corrupt the protocol stream", stray)
	}
}

// readResponses reads newline-delimited frames until it has seen want replies,
// failing if any line is not a JSON-RPC message.
func readResponses(t *testing.T, r io.Reader, want int) map[string]rpcResponse {
	t.Helper()
	out := map[string]rpcResponse{}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for len(out) < want && scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		var resp rpcResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Fatalf("stdout carried a line that is not a JSON-RPC message: %q (%v)", line, err)
		}
		if resp.JSONRPC != "2.0" {
			t.Fatalf("stdout carried a non-protocol message: %q", line)
		}
		if resp.Error != nil {
			t.Fatalf("the server answered with an error: %s", resp.Error)
		}
		if len(resp.ID) > 0 {
			out[strings.Trim(string(resp.ID), `"`)] = resp
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("reading stdout: %v", err)
	}
	if len(out) < want {
		t.Fatalf("read %d replies, want %d", len(out), want)
	}
	return out
}

func checkInitialize(t *testing.T, resp rpcResponse) {
	t.Helper()
	var result struct {
		ProtocolVersion string `json:"protocolVersion"`
		Instructions    string `json:"instructions"`
		ServerInfo      struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
		Capabilities struct {
			Tools *struct{} `json:"tools"`
		} `json:"capabilities"`
	}
	decode(t, resp.Result, &result)
	if result.ProtocolVersion == "" {
		t.Error("initialize did not negotiate a protocol version")
	}
	if result.ServerInfo.Name != "d9s" {
		t.Errorf("the server introduced itself as %q, want d9s", result.ServerInfo.Name)
	}
	if result.Capabilities.Tools == nil {
		t.Error("the server did not advertise the tools capability")
	}
	if !strings.Contains(result.Instructions, "read-only") {
		t.Errorf("the instructions do not state the read-only contract: %q", result.Instructions)
	}
}

func checkToolList(t *testing.T, resp rpcResponse) {
	t.Helper()
	var result struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			InputSchema struct {
				Type       string                     `json:"type"`
				Properties map[string]json.RawMessage `json:"properties"`
				Required   []string                   `json:"required"`
			} `json:"inputSchema"`
		} `json:"tools"`
	}
	decode(t, resp.Result, &result)

	want := []string{"describe_table", "list_connections", "list_databases", "list_tables", "query"}
	var got []string
	for _, tool := range result.Tools {
		got = append(got, tool.Name)
		if tool.Description == "" {
			t.Errorf("tool %s has no description for an agent to act on", tool.Name)
		}
		if tool.InputSchema.Type != "object" {
			t.Errorf("tool %s has input schema type %q, want object", tool.Name, tool.InputSchema.Type)
		}
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("tools/list returned %v, want %v", got, want)
	}

	for _, tool := range result.Tools {
		switch tool.Name {
		case "list_connections":
			if len(tool.InputSchema.Properties) != 0 {
				t.Errorf("list_connections should take no arguments, got %v", tool.InputSchema.Properties)
			}
		case "query":
			if !slices.Contains(tool.InputSchema.Required, "connection") || !slices.Contains(tool.InputSchema.Required, "sql") {
				t.Errorf("query requires %v, want connection and sql", tool.InputSchema.Required)
			}
			if _, ok := tool.InputSchema.Properties["database"]; !ok {
				t.Error("query does not offer an optional database argument")
			}
			if slices.Contains(tool.InputSchema.Required, "database") {
				t.Error("query's database argument should be optional")
			}
		case "describe_table":
			if !slices.Contains(tool.InputSchema.Required, "table") {
				t.Errorf("describe_table requires %v, want table among them", tool.InputSchema.Required)
			}
		}
	}
}

func checkToolCall(t *testing.T, resp rpcResponse) {
	t.Helper()
	var result struct {
		IsError bool `json:"isError"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	decode(t, resp.Result, &result)
	if result.IsError {
		t.Fatalf("list_tables failed: %+v", result.Content)
	}
	if len(result.Content) == 0 {
		t.Fatal("list_tables returned no content")
	}
	if result.Content[0].Type != "text" {
		t.Errorf("content type is %q, want text", result.Content[0].Type)
	}
	if !strings.Contains(result.Content[0].Text, "users") {
		t.Errorf("list_tables did not return the table:\n%s", result.Content[0].Text)
	}
}

func decode(t *testing.T, raw json.RawMessage, into any) {
	t.Helper()
	if len(raw) == 0 {
		t.Fatal("the reply carried no result")
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("decoding %s: %v", raw, err)
	}
}

// captureStdout redirects the process's own stdout for the test and returns a
// function reporting what was written to it.
func captureStdout(t *testing.T) func() string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout capture pipe: %v", err)
	}
	saved := os.Stdout
	os.Stdout = w
	var once bool
	var captured string
	read := func() string {
		if once {
			return captured
		}
		once = true
		os.Stdout = saved
		_ = w.Close()
		out, err := io.ReadAll(r)
		if err != nil {
			t.Errorf("reading the captured stdout: %v", err)
		}
		captured = string(out)
		return captured
	}
	t.Cleanup(func() { read() })
	return read
}
