package mcp

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"memory-mcp/internal/db"

	gomcp "github.com/mark3labs/mcp-go/mcp"
)

func testDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func callTool(t *testing.T, s *Server, name string, args map[string]any) *gomcp.CallToolResult {
	t.Helper()
	req := gomcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args

	var handler func(context.Context, gomcp.CallToolRequest) (*gomcp.CallToolResult, error)
	switch name {
	case "memory_store":
		handler = s.handleStore
	case "memory_search":
		handler = s.handleSearch
	case "memory_list":
		handler = s.handleList
	case "memory_delete":
		handler = s.handleDelete
	case "memory_get":
		handler = s.handleGet
	case "memory_timeline":
		handler = s.handleTimeline
	case "memory_context":
		handler = s.handleContext
	default:
		t.Fatalf("unknown tool: %s", name)
	}

	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestMCPStore(t *testing.T) {
	s := NewServer(testDB(t))
	result := callTool(t, s, "memory_store", map[string]any{
		"type":    "til",
		"content": "test content",
		"tags":    "go",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
}

func TestMCPSearch(t *testing.T) {
	d := testDB(t)
	d.Store(&db.Memory{Type: "til", Content: "database connection pooling", Tags: "db"})

	s := NewServer(d)
	result := callTool(t, s, "memory_search", map[string]any{
		"query": "database",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
}

func TestMCPList(t *testing.T) {
	d := testDB(t)
	d.Store(&db.Memory{Type: "til", Content: "a"})
	d.Store(&db.Memory{Type: "feedback", Content: "b"})

	s := NewServer(d)
	result := callTool(t, s, "memory_list", map[string]any{})
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
}

func TestMCPDelete(t *testing.T) {
	d := testDB(t)
	d.Store(&db.Memory{Type: "til", Content: "to delete"})

	s := NewServer(d)
	result := callTool(t, s, "memory_delete", map[string]any{
		"id": float64(1),
	})
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
}

func TestMCPStoreReportsRedacted(t *testing.T) {
	s := NewServer(testDB(t))
	result := callTool(t, s, "memory_store", map[string]any{
		"type":    "til",
		"content": "password=abcdef123",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
	text := result.Content[0].(gomcp.TextContent).Text
	if !strings.Contains(text, `"redacted":1`) {
		t.Errorf("result = %s, want redacted count", text)
	}
}

func resultText(r *gomcp.CallToolResult) string {
	return r.Content[0].(gomcp.TextContent).Text
}

func TestMCPSearchCompactDefault(t *testing.T) {
	d := testDB(t)
	d.Store(&db.Memory{Type: "til", Content: "database connection pooling " + strings.Repeat("x", 200), Project: "p"})
	s := NewServer(d)
	text := resultText(callTool(t, s, "memory_search", map[string]any{"query": "database"}))
	if !strings.HasPrefix(text, "- #1 til p ") || !strings.HasSuffix(text, "…") {
		t.Errorf("compact = %q", text)
	}
	full := resultText(callTool(t, s, "memory_search", map[string]any{"query": "database", "compact": false}))
	if !strings.Contains(full, `"content"`) {
		t.Errorf("full = %q", full)
	}
}

func TestMCPGet(t *testing.T) {
	d := testDB(t)
	d.Store(&db.Memory{Type: "til", Content: "alpha"})
	s := NewServer(d)
	text := resultText(callTool(t, s, "memory_get", map[string]any{"ids": "1,42"}))
	if !strings.Contains(text, "#1 [til]") || !strings.Contains(text, "alpha") || !strings.Contains(text, "#42 not found") {
		t.Errorf("get = %q", text)
	}
	many := strings.TrimSuffix(strings.Repeat("1,", 21), ",")
	if r := callTool(t, s, "memory_get", map[string]any{"ids": many}); !r.IsError {
		t.Error("expected error for >20 ids")
	}
}

func TestMCPTimeline(t *testing.T) {
	d := testDB(t)
	for _, c := range []string{"a", "b", "c"} {
		d.Store(&db.Memory{Type: "summary", Content: c, Project: "p"})
	}
	s := NewServer(d)
	text := resultText(callTool(t, s, "memory_timeline", map[string]any{"id": float64(2)}))
	lines := strings.Split(text, "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[1], "→ #2 ") {
		t.Errorf("timeline = %q", text)
	}
}
