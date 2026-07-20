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
	db db.Store
}

// NewServer 建立 MCP server。d 可以是本機 *db.DB 或 httpapi.Client
// 等其他 db.Store 實作（例如 --remote 轉發到中央機器）。
func NewServer(d db.Store) *Server {
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
	m, err := s.db.Get(id)
	if err != nil {
		return textResult(map[string]any{"id": id}), nil
	}
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

// handleContext 產生 bounded 記憶摘要。
func (s *Server) handleContext(_ context.Context, req gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	limit := req.GetInt("limit", 20)
	if limit <= 0 {
		limit = 20
	}
	summary, err := s.db.Context(db.ContextOptions{
		Type:    req.GetString("type", ""),
		Project: req.GetString("project", ""),
		Limit:   limit,
	})
	if err != nil {
		return errResult(err), nil
	}
	return gomcp.NewToolResultText(summary), nil
}

// MCPServer 建立並回傳已註冊 tools 的 MCP server 實例。
func (s *Server) MCPServer() *mcpserver.MCPServer {
	srv := mcpserver.NewMCPServer(
		"memory-mcp", "1.0.0",
		mcpserver.WithToolCapabilities(true),
	)

	srv.AddTool(gomcp.NewTool("memory_store",
		gomcp.WithDescription("Persist a piece of knowledge across sessions. Use PROACTIVELY when: you fix a tricky bug (type=til), the user corrects your approach or states a preference (type=feedback), you finish a work session (type=summary), or you discover cross-project architectural knowledge (type=knowledge). Don't wait to be asked — store immediately when something is worth remembering."),
		gomcp.WithString("type", gomcp.Required(), gomcp.Description("feedback=user preferences/corrections, til=technical solutions, summary=session/work summaries, knowledge=cross-project insights")),
		gomcp.WithString("content", gomcp.Required(), gomcp.Description("Memory content — be specific and self-contained so it's useful without context")),
		gomcp.WithString("tags", gomcp.Description("Comma-separated tags for categorization")),
		gomcp.WithString("project", gomcp.Description("Project name for scoping")),
	), s.handleStore)

	srv.AddTool(gomcp.NewTool("memory_search",
		gomcp.WithDescription("Search past memories by keyword (FTS5). Use when: starting a new task (check for relevant past solutions), hitting a familiar-looking problem (search for prior fixes), needing to recall user preferences, or working on a project you've touched before."),
		gomcp.WithString("query", gomcp.Required(), gomcp.Description("Search keywords — supports CJK, minimum 3 characters")),
		gomcp.WithString("type", gomcp.Description("Filter by type: feedback, til, summary, knowledge")),
		gomcp.WithNumber("limit", gomcp.Description("Max results (default 5)")),
	), s.handleSearch)

	srv.AddTool(gomcp.NewTool("memory_list",
		gomcp.WithDescription("List recent memories chronologically. Use to review what was stored recently or browse by type."),
		gomcp.WithString("type", gomcp.Description("Filter by type: feedback, til, summary, knowledge")),
		gomcp.WithNumber("limit", gomcp.Description("Max results (default 10)")),
		gomcp.WithString("since", gomcp.Description("Time range in Nd format, e.g. 7d for last 7 days")),
	), s.handleList)

	srv.AddTool(gomcp.NewTool("memory_delete",
		gomcp.WithDescription("Delete a memory by ID. Use to remove outdated or incorrect memories."),
		gomcp.WithNumber("id", gomcp.Required(), gomcp.Description("Memory ID to delete")),
	), s.handleDelete)

	srv.AddTool(gomcp.NewTool("memory_context",
		gomcp.WithDescription("Get a bounded summary of recent memories as context. Use at the START of a session or when switching to a different project to load relevant background knowledge. Returns a concise markdown digest — cheaper than searching multiple times."),
		gomcp.WithString("type", gomcp.Description("Filter by type: feedback, til, summary, knowledge")),
		gomcp.WithString("project", gomcp.Description("Filter to a specific project")),
		gomcp.WithNumber("limit", gomcp.Description("Max memories to include (default 20)")),
	), s.handleContext)

	return srv
}
