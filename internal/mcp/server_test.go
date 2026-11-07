package mcp

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ggwhite/memory-mcp/internal/db"

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

func TestMCPStoreFeedbackRequiresProject(t *testing.T) {
	d := testDB(t)
	s := NewServer(d)
	result := callTool(t, s, "memory_store", map[string]any{
		"type":    "feedback",
		"content": "no project rule",
	})
	if !result.IsError {
		t.Fatalf("feedback without project should be rejected: %v", result.Content)
	}
	if text := result.Content[0].(gomcp.TextContent).Text; !strings.Contains(text, "global") {
		t.Errorf("error should mention global, got %q", text)
	}
	if ms, _ := d.List(db.ListOptions{}); len(ms) != 0 {
		t.Errorf("rejected feedback was stored: %+v", ms)
	}

	result = callTool(t, s, "memory_store", map[string]any{
		"type":    "summary",
		"content": "session summary",
	})
	if result.IsError {
		t.Fatalf("summary without project should succeed: %v", result.Content)
	}
}

func TestMCPStoreSupersedes(t *testing.T) {
	d := testDB(t)
	a, _ := d.Store(&db.Memory{Type: "feedback", Project: "p", Content: "舊規則"})
	s := NewServer(d)

	result := callTool(t, s, "memory_store", map[string]any{
		"type": "feedback", "project": "p", "content": "新規則",
		"supersedes": []any{float64(a)},
	})
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
	text := result.Content[0].(gomcp.TextContent).Text
	if !strings.Contains(text, `"deleted":[1]`) {
		t.Errorf("result = %s, want deleted ids", text)
	}
	if _, err := d.Get(a); err == nil {
		t.Errorf("#%d should be deleted", a)
	}

	result = callTool(t, s, "memory_store", map[string]any{
		"type": "til", "content": "x", "supersedes": []any{float64(999)},
	})
	if !result.IsError {
		t.Fatal("want error for missing superseded id")
	}
	if st, _ := d.Stats(); st.Total != 1 {
		t.Errorf("total = %d, want 1 (nothing stored)", st.Total)
	}
}

// supersedes 參數格式不合法時必須回錯誤，且不能先寫入新記憶、也不能刪舊記憶。
func TestMCPStoreSupersedesInvalid(t *testing.T) {
	bad := map[string]any{
		"string":        "12",
		"nonNumeric":    []any{"abc"},
		"fraction":      []any{1.5},
		"object":        map[string]any{"id": 1},
		"zero":          []any{float64(0)},
		"negative":      []any{float64(-3)},
		"mixedBad":      []any{float64(1), "x"},
		"floatScalar":   1.5,
		"nullElement":   []any{nil},
		"boolElement":   []any{true},
		"numericStrBad": []any{"1.5"},
	}
	for name, v := range bad {
		t.Run(name, func(t *testing.T) {
			d := testDB(t)
			if _, err := d.Store(&db.Memory{Type: "til", Content: "舊"}); err != nil {
				t.Fatal(err)
			}
			s := NewServer(d)
			result := callTool(t, s, "memory_store", map[string]any{
				"type": "til", "content": "新", "supersedes": v,
			})
			if !result.IsError {
				t.Fatalf("want error for supersedes=%#v, got %s", v, resultText(result))
			}
			if st, _ := d.Stats(); st.Total != 1 {
				t.Errorf("total = %d, want 1 (nothing stored)", st.Total)
			}
		})
	}
}

// supersedes 接受整數陣列、整數字串與單一整數。
func TestMCPStoreSupersedesAccepted(t *testing.T) {
	cases := map[string]func(a, b int64) any{
		"floatArray":  func(a, b int64) any { return []any{float64(a), float64(b)} },
		"intArray":    func(a, b int64) any { return []any{int(a), int(b)} },
		"stringArray": func(a, b int64) any { return []any{strconv.FormatInt(a, 10), float64(b)} },
		"typedInts":   func(a, b int64) any { return []int{int(a), int(b)} },
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			d := testDB(t)
			a, _ := d.Store(&db.Memory{Type: "til", Content: "舊 a"})
			b, _ := d.Store(&db.Memory{Type: "til", Content: "舊 b"})
			s := NewServer(d)
			result := callTool(t, s, "memory_store", map[string]any{
				"type": "til", "content": "新", "supersedes": mk(a, b),
			})
			if result.IsError {
				t.Fatalf("unexpected error: %s", resultText(result))
			}
			if st, _ := d.Stats(); st.Total != 1 {
				t.Errorf("total = %d, want 1", st.Total)
			}
		})
	}

	t.Run("single", func(t *testing.T) {
		d := testDB(t)
		a, _ := d.Store(&db.Memory{Type: "til", Content: "舊"})
		s := NewServer(d)
		result := callTool(t, s, "memory_store", map[string]any{
			"type": "til", "content": "新", "supersedes": float64(a),
		})
		if result.IsError {
			t.Fatalf("unexpected error: %s", resultText(result))
		}
		if _, err := d.Get(a); err == nil {
			t.Errorf("#%d should be deleted", a)
		}
	})
}

// 新存的記憶 created 為現在：until 設在遠古應被擋掉、since=7d 應保留，
// 證明兩個參數都有傳進 db.Search。
func TestMCPSearchSinceUntil(t *testing.T) {
	d := testDB(t)
	s := NewServer(d)
	d.Store(&db.Memory{Type: "til", Content: "gamma entry"})

	text := resultText(callTool(t, s, "memory_search", map[string]any{"query": "gamma", "since": "7d"}))
	if !strings.Contains(text, "gamma entry") {
		t.Errorf("since=7d should keep the new row:\n%s", text)
	}
	text = resultText(callTool(t, s, "memory_search", map[string]any{"query": "gamma", "until": "2020-01-10"}))
	if strings.Contains(text, "gamma entry") {
		t.Errorf("until=2020-01-10 should exclude the new row:\n%s", text)
	}
	text = resultText(callTool(t, s, "memory_search", map[string]any{"query": "gamma", "since": "2999-01-01"}))
	if strings.Contains(text, "gamma entry") {
		t.Errorf("future since should exclude the new row:\n%s", text)
	}
	if bad := callTool(t, s, "memory_search", map[string]any{"query": "gamma", "since": "nope"}); !bad.IsError {
		t.Error("bad since should be a tool error")
	}
}
