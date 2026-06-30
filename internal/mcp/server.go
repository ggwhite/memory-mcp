package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"memory-mcp/internal/db"

	gomcp "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// Server MCP server，封裝 DB 操作為 MCP tools。
type Server struct {
	db *db.DB
}

// NewServer 建立 MCP server。
func NewServer(d *db.DB) *Server {
	return &Server{db: d}
}

func textResult(v any) *gomcp.CallToolResult {
	data, _ := json.Marshal(v)
	return gomcp.NewToolResultText(string(data))
}

func errResult(err error) *gomcp.CallToolResult {
	return gomcp.NewToolResultError(err.Error())
}

// handleStore 處理 memory_store tool 呼叫。
func (s *Server) handleStore(_ context.Context, req gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	id, err := s.db.Store(&db.Memory{
		Type:    req.GetString("type", ""),
		Content: req.GetString("content", ""),
		Tags:    req.GetString("tags", ""),
		Project: req.GetString("project", ""),
	})
	if err != nil {
		return errResult(err), nil
	}
	m, _ := s.db.Get(id)
	return textResult(map[string]any{
		"id":      id,
		"created": m.Created.Format("2006-01-02T15:04:05"),
	}), nil
}

// handleSearch 處理 memory_search tool 呼叫。
func (s *Server) handleSearch(_ context.Context, req gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	limit := req.GetInt("limit", 5)
	if limit <= 0 {
		limit = 5
	}
	results, err := s.db.Search(db.SearchOptions{
		Query: req.GetString("query", ""),
		Type:  req.GetString("type", ""),
		Limit: limit,
	})
	if err != nil {
		return errResult(err), nil
	}
	return textResult(results), nil
}

// handleList 處理 memory_list tool 呼叫。
func (s *Server) handleList(_ context.Context, req gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	limit := req.GetInt("limit", 10)
	if limit <= 0 {
		limit = 10
	}
	memories, err := s.db.List(db.ListOptions{
		Type:  req.GetString("type", ""),
		Limit: limit,
		Since: req.GetString("since", ""),
	})
	if err != nil {
		return errResult(err), nil
	}
	return textResult(memories), nil
}

// handleDelete 處理 memory_delete tool 呼叫。
func (s *Server) handleDelete(_ context.Context, req gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	id := int64(req.GetFloat("id", 0))
	if id <= 0 {
		return errResult(fmt.Errorf("id is required")), nil
	}
	if err := s.db.Delete(id); err != nil {
		return errResult(err), nil
	}
	return textResult(map[string]any{"deleted": true}), nil
}

// MCPServer 建立並回傳已註冊 tools 的 MCP server 實例。
func (s *Server) MCPServer() *mcpserver.MCPServer {
	srv := mcpserver.NewMCPServer(
		"memory-mcp", "1.0.0",
		mcpserver.WithToolCapabilities(true),
	)

	srv.AddTool(gomcp.NewTool("memory_store",
		gomcp.WithDescription("Store a new memory"),
		gomcp.WithString("type", gomcp.Required(), gomcp.Description("Memory type: feedback, til, summary, or knowledge")),
		gomcp.WithString("content", gomcp.Required(), gomcp.Description("Memory content")),
		gomcp.WithString("tags", gomcp.Description("Comma-separated tags")),
		gomcp.WithString("project", gomcp.Description("Project name")),
	), s.handleStore)

	srv.AddTool(gomcp.NewTool("memory_search",
		gomcp.WithDescription("Search memories using FTS5 full-text search"),
		gomcp.WithString("query", gomcp.Required(), gomcp.Description("Search query")),
		gomcp.WithString("type", gomcp.Description("Filter by memory type")),
		gomcp.WithNumber("limit", gomcp.Description("Max results (default 5)")),
	), s.handleSearch)

	srv.AddTool(gomcp.NewTool("memory_list",
		gomcp.WithDescription("List memories with optional filters"),
		gomcp.WithString("type", gomcp.Description("Filter by memory type")),
		gomcp.WithNumber("limit", gomcp.Description("Max results (default 10)")),
		gomcp.WithString("since", gomcp.Description("Show memories since (Nd format, e.g. 7d)")),
	), s.handleList)

	srv.AddTool(gomcp.NewTool("memory_delete",
		gomcp.WithDescription("Delete a memory by ID"),
		gomcp.WithNumber("id", gomcp.Required(), gomcp.Description("Memory ID to delete")),
	), s.handleDelete)

	return srv
}
