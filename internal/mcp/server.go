package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"memory-mcp/internal/db"

	gomcp "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

const (
	searchSnippetRunes = 80
	getMaxIDs          = 20
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
	mem := &db.Memory{
		Type:    req.GetString("type", ""),
		Content: req.GetString("content", ""),
		Tags:    req.GetString("tags", ""),
		Project: req.GetString("project", ""),
	}
	id, err := s.db.Store(mem)
	if err != nil {
		return errResult(err), nil
	}
	out := map[string]any{"id": id}
	if mem.Redacted > 0 {
		out["redacted"] = mem.Redacted
		out["note"] = fmt.Sprintf("已遮蔽 %d 處敏感內容", mem.Redacted)
	}
	if m, err := s.db.Get(id); err == nil {
		out["created"] = m.Created.Format("2006-01-02T15:04:05")
	}
	return textResult(out), nil
}

// handleSearch 處理 memory_search tool 呼叫，compact（預設）只回索引行。
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
	if !req.GetBool("compact", true) {
		return textResult(results), nil
	}
	if len(results) == 0 {
		return gomcp.NewToolResultText("No results."), nil
	}
	lines := make([]string, len(results))
	for i, r := range results {
		lines[i] = db.CompactLine(r.Memory, searchSnippetRunes, true)
	}
	return gomcp.NewToolResultText(strings.Join(lines, "\n")), nil
}

// handleGet 依 ID 取回全文，找不到的標示 not found。
func (s *Server) handleGet(_ context.Context, req gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	ids, err := db.ParseIDs(req.GetString("ids", ""))
	if err != nil {
		return errResult(err), nil
	}
	if len(ids) == 0 {
		return errResult(fmt.Errorf("ids is required")), nil
	}
	if len(ids) > getMaxIDs {
		return errResult(fmt.Errorf("too many ids: %d (max %d)", len(ids), getMaxIDs)), nil
	}
	found, err := s.db.GetMany(ids)
	if err != nil {
		return errResult(err), nil
	}
	byID := make(map[int64]db.Memory, len(found))
	for _, m := range found {
		byID[m.ID] = m
	}
	parts := make([]string, len(ids))
	for i, id := range ids {
		if m, ok := byID[id]; ok {
			parts[i] = db.FormatFull(m)
		} else {
			parts[i] = fmt.Sprintf("#%d not found", id)
		}
	}
	return gomcp.NewToolResultText(strings.Join(parts, "\n\n")), nil
}

// handleTimeline 回傳同 project 前後記憶的索引，中心那筆以「→」標示。
func (s *Server) handleTimeline(_ context.Context, req gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	id := int64(req.GetFloat("id", 0))
	if id <= 0 {
		return errResult(fmt.Errorf("id is required")), nil
	}
	memories, err := s.db.Timeline(id, req.GetInt("before", 3), req.GetInt("after", 3))
	if err != nil {
		return errResult(err), nil
	}
	return gomcp.NewToolResultText(strings.Join(db.TimelineLines(memories, id, searchSnippetRunes), "\n")), nil
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
		Full:    req.GetBool("full", false),
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
		gomcp.WithDescription("Search past memories (FTS5 + semantic). Returns one index line per hit by default (id, type, project, date, 80-char snippet); call memory_get with the ids you need for full content. Use when: starting a new task, hitting a familiar-looking problem, needing to recall user preferences, or working on a project you've touched before."),
		gomcp.WithString("query", gomcp.Required(), gomcp.Description("Search keywords — supports CJK, minimum 3 characters")),
		gomcp.WithString("type", gomcp.Description("Filter by type: feedback, til, summary, knowledge")),
		gomcp.WithNumber("limit", gomcp.Description("Max results (default 5)")),
		gomcp.WithBoolean("compact", gomcp.Description("true (default) = index lines only; false = full JSON results")),
	), s.handleSearch)

	srv.AddTool(gomcp.NewTool("memory_get",
		gomcp.WithDescription("Fetch full content of memories by id, after memory_search / memory_context gave you the ids."),
		gomcp.WithString("ids", gomcp.Required(), gomcp.Description("Comma-separated ids, e.g. \"1030,1027\" (max 20)")),
	), s.handleGet)

	srv.AddTool(gomcp.NewTool("memory_timeline",
		gomcp.WithDescription("Show memories stored before and after a given memory in the same project, as index lines. Use to follow a chain of session summaries."),
		gomcp.WithNumber("id", gomcp.Required(), gomcp.Description("Center memory id")),
		gomcp.WithNumber("before", gomcp.Description("How many earlier memories (default 3, max 10)")),
		gomcp.WithNumber("after", gomcp.Description("How many later memories (default 3, max 10)")),
	), s.handleTimeline)

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
		gomcp.WithDescription("Get an index of recent memories (one line each, 120-char snippet). With project and no type: global feedback + that project's latest summaries + other memories. Use at the START of a session or when switching projects; then call memory_get for full content."),
		gomcp.WithString("type", gomcp.Description("Filter by type: feedback, til, summary, knowledge")),
		gomcp.WithString("project", gomcp.Description("Project name (lowercase basename of the working directory)")),
		gomcp.WithNumber("limit", gomcp.Description("Max memories when not in project mode (default 20)")),
		gomcp.WithBoolean("full", gomcp.Description("true = full content grouped by type (legacy format)")),
	), s.handleContext)

	return srv
}
