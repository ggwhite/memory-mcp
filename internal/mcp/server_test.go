package mcp

import (
	"context"
	"path/filepath"
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
