# Context 注入與檢索優化 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把記憶注入從「每則 prompt 注入 20 筆全文」改成「SessionStart 注入一次專案索引」，並加上漸進揭露檢索、敏感內容過濾與 SessionEnd 自動 summary。

**Architecture:** DB 層新增 `Sanitize`、`GetMany`、`Timeline`、專案三段式 `Context`，`Store` interface 與 `httpapi` 同步擴充，讓 `--remote` 可用。新增 `internal/hook`（讀 hook stdin、算 project）與 `internal/summarize`（解析 transcript、呼叫 `claude -p`、存 summary）。CLI 新增 `get`、`timeline`、`summarize`，`context` 加 `--hook`／`--full`。

**Tech Stack:** Go 1.26、`modernc.org/sqlite`、`github.com/mark3labs/mcp-go v0.55.1`、`github.com/spf13/cobra`。

**Spec:** `docs/superpowers/specs/2026-09-28-context-optimization-design.md`

## Global Constraints

- 單一 Go binary，NEVER 新增第三方依賴。
- hook 模式（`context --hook`、`summarize --hook`）一律 exit 0，錯誤只寫 log 或丟棄，NEVER 讓 Claude Code 的 hook 失敗。
- 防遞迴環境變數：`MEMORY_MCP_SUMMARIZING=1`。
- project 名稱 = `strings.ToLower(filepath.Base(cwd))`。
- context 每筆內容取前 120 rune；search compact 取前 80 rune；context 總量上限 6,000 rune。
- `memory_get` 一次最多 20 個 id；timeline before／after 上限 10，預設 3。
- 自動 summary：`claude -p --model haiku --no-session-persistence`，timeout 3 分鐘，transcript 保留最後 60,000 rune，user 文字訊息少於 3 則跳過，存成 `type=summary`、`tags=auto-summary`。
- log 檔：`~/.local/share/memory-mcp/summarize.log`（與 DB 同目錄）。
- `ImportBatch` NEVER 呼叫 Sanitize。
- 程式碼註解用繁體中文，風格對齊既有檔案（每個 exported 函式一行說明）。
- 每個 task 結束跑 `go test ./...` 與 `go vet ./...`，全綠才 commit。
- commit message 結尾加：
  ```
  Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01G93hEBJmLcYAfngHResbZt
  ```

## Review Focus

1. **金鑰已被前一條規則換成 `[REDACTED]` 後，key=value 規則再次命中**：`token=sk-ant-…` 應只算 1 處，內容為 `token=[REDACTED]`。→ Task 1 測試 `token_with_anthropic_key`。
2. **中文敘述含關鍵字但沒有值**：「後台操作員改密碼、輪換 token」「secret: 這是秘密」不應被遮蔽。→ Task 1 測試 `chinese_prose_untouched`、`short_cjk_value`。
3. **在終端機手動跑 `context --hook`（stdin 是 TTY）**：不應卡住等輸入，應改用目前目錄。→ Task 6 以 `hook.Read` 對 TTY 的判斷處理，測試 `TestReadEmptyInput`。
4. **專案沒有任何記憶時 SessionStart 注入**：應完全不輸出，不能輸出只有標題的空殼。→ Task 6 測試 `TestProjectContextEmpty`。
5. **session 中已手動存 summary**：自動 summary 不應重複存。時間比較要處理 transcript 的 RFC3339（UTC、含毫秒）與 DB 的 `2006-01-02T15:04:05`（UTC、無時區）。→ Task 7 測試 `TestRunSkipsWhenManualSummaryExists`。

---

## File Structure

| 檔案 | 動作 | 職責 |
|---|---|---|
| `internal/db/sanitize.go` | Create | `Sanitize`：`<private>` 與金鑰格式遮蔽 |
| `internal/db/sanitize_test.go` | Create | Sanitize 規則測試 |
| `internal/db/format.go` | Create | `Snippet`、`CompactLine`、`FormatFull`、`EstimateTokens`、`ParseIDs` |
| `internal/db/format_test.go` | Create | 格式函式測試 |
| `internal/db/db.go` | Modify | `Memory.Redacted`、`Store` interface、Store／Update 呼叫 Sanitize、`queryMemories`、`GetMany`、`Timeline`、`Context` 改寫 |
| `internal/db/context_test.go` | Create | Context 三段式／截斷／Full 測試 |
| `internal/db/db_test.go` | Modify | Redacted、GetMany、Timeline 測試 |
| `internal/httpapi/client.go` | Modify | Store 回寫 Redacted、GetMany、Timeline、Context full |
| `internal/httpapi/server.go` | Modify | 對應 endpoint |
| `internal/httpapi/server_test.go` | Create | client↔server round trip 測試 |
| `internal/mcp/server.go` | Modify | search compact、memory_get、memory_timeline、context full、store redacted |
| `internal/mcp/server_test.go` | Modify | 新 tool 測試 |
| `internal/hook/hook.go` | Create | `Input`、`Read`、`Project`、`SummarizingEnv` |
| `internal/hook/hook_test.go` | Create | hook 測試 |
| `internal/summarize/transcript.go` | Create | `ParseTranscript` |
| `internal/summarize/summarize.go` | Create | `BuildPrompt`、`Run`、`ClaudeRunner` |
| `internal/summarize/summarize_test.go` | Create | 解析與 Run 測試 |
| `internal/cli/commands.go` | Modify | store 顯示遮蔽、search `--compact`、`get`、`timeline`、context `--hook`／`--full`、`summarize` |
| `README.md` | Modify | 新指令與 hook 設定 |
| `~/dotfiles/claude/settings.json` | Modify | hook 設定（dotfiles repo） |
| `~/dotfiles/agents/core.md` | Modify | memory 規則（dotfiles repo） |

---

### Task 1: Sanitize 函式

**Files:**
- Create: `internal/db/sanitize.go`
- Test: `internal/db/sanitize_test.go`

**Interfaces:**
- Produces: `func Sanitize(content string) (string, int)`、`const RedactedMark = "[REDACTED]"`、`var ErrEmptyAfterSanitize error`

- [ ] **Step 1: Write the failing test**

`internal/db/sanitize_test.go`：

```go
package db

import "testing"

func TestSanitize(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		want  string
		count int
	}{
		{"plain", "SQLite trigram 支援 CJK", "SQLite trigram 支援 CJK", 0},
		{"private_block", "前 <private>密碼是 abc</private> 後", "前  後", 1},
		{"private_multiline_case", "a <PRIVATE>x\ny\n</Private> b <private>z</private>", "a  b", 2},
		{"pem", "key:\n-----BEGIN RSA PRIVATE KEY-----\nMIIabc\n-----END RSA PRIVATE KEY-----\nend", "key:\n[REDACTED]\nend", 1},
		{"aws", "id AKIAABCDEFGHIJKLMNOP here", "id [REDACTED] here", 1},
		{"anthropic", "k sk-ant-api03-abcdefghijklmnopqrstuv", "k [REDACTED]", 1},
		{"openai", "k sk-abcdefghijklmnopqrstuvwx", "k [REDACTED]", 1},
		{"github", "ghp_abcdefghijklmnopqrstuvwxyz0123456789", "[REDACTED]", 1},
		{"gitlab", "glpat-abcdefghij1234567890", "[REDACTED]", 1},
		{"jwt", "Bearer eyJhbGciOiJIUzI1.eyJzdWIiOiIxMjM0.SflKxwRJSMeKKF2QT4f", "Bearer [REDACTED]", 1},
		{"password_eq", "password=abcdef123 ok", "password=[REDACTED] ok", 1},
		{"api_key_colon_quoted", `api_key: "zzzzzz99"`, `api_key: "[REDACTED]"`, 1},
		{"token_with_anthropic_key", "token=sk-ant-api03-abcdefghijklmnopqrstuv", "token=[REDACTED]", 1},
		{"chinese_prose_untouched", "後台操作員改密碼、輪換 token、providerpassword 不動", "後台操作員改密碼、輪換 token、providerpassword 不動", 0},
		{"short_cjk_value", "secret: 這是秘密", "secret: 這是秘密", 0},
		{"short_value", "pwd=abc", "pwd=abc", 0},
		{"trim", "  <private>x</private> keep  ", "keep", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, n := Sanitize(c.in)
			if got != c.want {
				t.Errorf("content = %q, want %q", got, c.want)
			}
			if n != c.count {
				t.Errorf("count = %d, want %d", n, c.count)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/db -run TestSanitize -v`
Expected: FAIL，`undefined: Sanitize`

- [ ] **Step 3: Write minimal implementation**

`internal/db/sanitize.go`：

```go
package db

import (
	"errors"
	"regexp"
	"strings"
)

// RedactedMark 取代敏感內容的標記。
const RedactedMark = "[REDACTED]"

// ErrEmptyAfterSanitize 過濾後內容為空，不寫入。
var ErrEmptyAfterSanitize = errors.New("content empty after sanitize")

var privateBlock = regexp.MustCompile(`(?is)<private>.*?</private>`)

// secretPatterns 順序有意義：sk-ant- 必須在 sk- 之前。
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]{20,}`),
	regexp.MustCompile(`sk-[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{36,}`),
	regexp.MustCompile(`glpat-[A-Za-z0-9_-]{20,}`),
	regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`),
}

var keyValue = regexp.MustCompile(`(?i)(password|passwd|pwd|secret|token|api[_-]?key)(\s*[:=]\s*["']?)([^\s"',;]{6,})`)

// Sanitize 刪除 <private> 區段並遮蔽常見金鑰格式，回傳過濾後內容（已 TrimSpace）與處理數。
func Sanitize(content string) (string, int) {
	n := 0
	out := privateBlock.ReplaceAllStringFunc(content, func(string) string {
		n++
		return ""
	})
	for _, re := range secretPatterns {
		out = re.ReplaceAllStringFunc(out, func(string) string {
			n++
			return RedactedMark
		})
	}
	out = keyValue.ReplaceAllStringFunc(out, func(m string) string {
		sub := keyValue.FindStringSubmatch(m)
		if sub[3] == RedactedMark {
			return m
		}
		n++
		return sub[1] + sub[2] + RedactedMark
	})
	return strings.TrimSpace(out), n
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/db -run TestSanitize -v`
Expected: PASS（全部子測試）

- [ ] **Step 5: Commit**

```bash
git add internal/db/sanitize.go internal/db/sanitize_test.go
git commit -m "feat: add Sanitize for private blocks and secret patterns"
```

---

### Task 2: Store／Update 套用 Sanitize 並回報遮蔽數

**Files:**
- Modify: `internal/db/db.go`（`Memory` struct、`Store`、`Update`）
- Modify: `internal/httpapi/server.go`（`POST /v1/store`）
- Modify: `internal/httpapi/client.go`（`Store`）
- Modify: `internal/mcp/server.go`（`handleStore`）
- Modify: `internal/cli/commands.go`（`storeCmd`）
- Test: `internal/db/db_test.go`、`internal/httpapi/server_test.go`（新檔）、`internal/mcp/server_test.go`

**Interfaces:**
- Consumes: `Sanitize`、`ErrEmptyAfterSanitize`（Task 1）
- Produces: `Memory.Redacted int`（json `redacted,omitempty`）；`DB.Store` 回傳後 `mem.Content` 為過濾後內容、`mem.Redacted` 為遮蔽數；`httpapi.Client.Store` 同樣回寫 `mem.Redacted`。`Update` 只過濾不回報（簽名不變）。

- [ ] **Step 1: Write the failing tests**

在 `internal/db/db_test.go` 末尾加：

```go
func TestStoreSanitizes(t *testing.T) {
	d := testDB(t)
	mem := &Memory{Type: "til", Content: "password=abcdef123 <private>x</private>"}
	id, err := d.Store(mem)
	if err != nil {
		t.Fatal(err)
	}
	if mem.Redacted != 2 {
		t.Errorf("Redacted = %d, want 2", mem.Redacted)
	}
	got, _ := d.Get(id)
	if got.Content != "password=[REDACTED]" {
		t.Errorf("content = %q", got.Content)
	}
}

func TestStoreEmptyAfterSanitize(t *testing.T) {
	d := testDB(t)
	_, err := d.Store(&Memory{Type: "til", Content: "<private>only</private>"})
	if err != ErrEmptyAfterSanitize {
		t.Fatalf("err = %v, want ErrEmptyAfterSanitize", err)
	}
}

func TestUpdateSanitizes(t *testing.T) {
	d := testDB(t)
	id, _ := d.Store(&Memory{Type: "til", Content: "a"})
	if err := d.Update(id, "token=abcdef123"); err != nil {
		t.Fatal(err)
	}
	got, _ := d.Get(id)
	if got.Content != "token=[REDACTED]" {
		t.Errorf("content = %q", got.Content)
	}
}
```

建立 `internal/httpapi/server_test.go`：

```go
package httpapi

import (
	"net/http/httptest"
	"path/filepath"
	"testing"

	"memory-mcp/internal/db"
)

func testClient(t *testing.T) (*Client, *db.DB) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(NewServer(d).Handler())
	t.Cleanup(func() {
		ts.Close()
		d.Close()
	})
	return NewClient(ts.URL), d
}

func TestClientStoreRedacted(t *testing.T) {
	c, _ := testClient(t)
	mem := &db.Memory{Type: "til", Content: "password=abcdef123"}
	id, err := c.Store(mem)
	if err != nil {
		t.Fatal(err)
	}
	if mem.Redacted != 1 {
		t.Errorf("Redacted = %d, want 1", mem.Redacted)
	}
	got, err := c.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "password=[REDACTED]" {
		t.Errorf("content = %q", got.Content)
	}
}
```

在 `internal/mcp/server_test.go` 末尾加（需在 import 加 `"strings"`）：

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/db ./internal/httpapi ./internal/mcp`
Expected: FAIL，`mem.Redacted undefined`

- [ ] **Step 3: Implement**

`internal/db/db.go` 的 `Memory` struct 加欄位：

```go
type Memory struct {
	ID       int64     `json:"id"`
	Type     string    `json:"type"`
	Content  string    `json:"content"`
	Tags     string    `json:"tags"`
	Project  string    `json:"project"`
	Created  time.Time `json:"created"`
	Updated  time.Time `json:"updated"`
	Redacted int       `json:"redacted,omitempty"`
}
```

`Store` 開頭加：

```go
// Store 儲存一筆記憶（寫入前先 Sanitize，mem.Content／mem.Redacted 會被回寫），回傳 auto-increment ID。
func (d *DB) Store(mem *Memory) (int64, error) {
	clean, n := Sanitize(mem.Content)
	if clean == "" {
		return 0, ErrEmptyAfterSanitize
	}
	mem.Content = clean
	mem.Redacted = n
	res, err := d.db.Exec(
	// ...以下不變
```

`Update` 開頭加：

```go
// Update 更新指定記憶的內容（寫入前先 Sanitize）。
func (d *DB) Update(id int64, content string) error {
	content, _ = Sanitize(content)
	if content == "" {
		return ErrEmptyAfterSanitize
	}
	res, err := d.db.Exec(
	// ...以下不變
```

`internal/httpapi/server.go` 的 `POST /v1/store` 最後一行改成：

```go
		writeJSON(w, http.StatusOK, map[string]int64{"id": id, "redacted": int64(mem.Redacted)})
```

`internal/httpapi/client.go` 的 `Store` 改成：

```go
// Store 見 db.Store。
func (c *Client) Store(mem *db.Memory) (int64, error) {
	var out struct {
		ID       int64 `json:"id"`
		Redacted int   `json:"redacted"`
	}
	if err := c.do(http.MethodPost, "/v1/store", nil, mem, &out); err != nil {
		return 0, err
	}
	mem.Redacted = out.Redacted
	return out.ID, nil
}
```

`internal/mcp/server.go` 的 `handleStore` 改成：

```go
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
```

`internal/cli/commands.go` 的 `storeCmd` RunE 改成：

```go
		mem := &db.Memory{Type: typ, Content: args[0], Tags: tags, Project: project}
		id, err := d.Store(mem)
		if err != nil {
			return err
		}
		if mem.Redacted > 0 {
			fmt.Fprintf(os.Stderr, "已遮蔽 %d 處敏感內容\n", mem.Redacted)
		}
		if jsonFlag {
			return printJSON(map[string]any{"id": id, "redacted": mem.Redacted})
		}
		fmt.Printf("Stored memory #%d\n", id)
		return nil
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... && go vet ./...`
Expected: 全部 PASS

- [ ] **Step 5: Commit**

```bash
git add internal/db/db.go internal/db/db_test.go internal/httpapi internal/mcp internal/cli/commands.go
git commit -m "feat: sanitize content on store/update and report redaction count"
```

---

### Task 3: 格式輔助函式

**Files:**
- Create: `internal/db/format.go`
- Test: `internal/db/format_test.go`
- Modify: `internal/cli/commands.go`（刪除 `formatMemory`，改用 `db.FormatFull`）

**Interfaces:**
- Produces:
  - `func Snippet(s string, n int) string`：壓掉所有空白為單一空格，取前 n rune，超過加 `…`
  - `func CompactLine(m Memory, n int, withProject bool) string`：`- #id type [project] YYYY-MM-DD snippet`（`withProject` 為 true 且 project 非空才印 project）
  - `func FormatFull(m Memory) string`：與舊 `cli.formatMemory` 輸出完全相同
  - `func EstimateTokens(s string) int`：rune ≥ U+2E80 算 1，其他每 4 個算 1（無條件進位）
  - `func ParseIDs(s string) ([]int64, error)`：逗號或空白分隔

- [ ] **Step 1: Write the failing test**

`internal/db/format_test.go`：

```go
package db

import (
	"testing"
	"time"
)

func TestSnippet(t *testing.T) {
	if got := Snippet("a\n b\t c", 10); got != "a b c" {
		t.Errorf("got %q", got)
	}
	if got := Snippet("一二三四五", 3); got != "一二三…" {
		t.Errorf("got %q", got)
	}
	if got := Snippet("一二三", 3); got != "一二三" {
		t.Errorf("got %q", got)
	}
}

func TestCompactLine(t *testing.T) {
	m := Memory{ID: 7, Type: "til", Project: "kairos", Content: "hello world",
		Created: time.Date(2026, 9, 27, 1, 0, 0, 0, time.UTC)}
	if got := CompactLine(m, 5, true); got != "- #7 til kairos 2026-09-27 hello…" {
		t.Errorf("got %q", got)
	}
	if got := CompactLine(m, 80, false); got != "- #7 til 2026-09-27 hello world" {
		t.Errorf("got %q", got)
	}
	m.Project = ""
	if got := CompactLine(m, 80, true); got != "- #7 til 2026-09-27 hello world" {
		t.Errorf("got %q", got)
	}
}

func TestFormatFull(t *testing.T) {
	m := Memory{ID: 3, Type: "feedback", Tags: "a,b", Project: "p", Content: "body",
		Created: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)}
	want := "#3 [feedback] 2026-01-02  tags:a,b  project:p\n  body"
	if got := FormatFull(m); got != want {
		t.Errorf("got %q", got)
	}
}

func TestEstimateTokens(t *testing.T) {
	if got := EstimateTokens("中文ab"); got != 3 {
		t.Errorf("got %d, want 3", got)
	}
	if got := EstimateTokens("abcdefgh"); got != 2 {
		t.Errorf("got %d, want 2", got)
	}
}

func TestParseIDs(t *testing.T) {
	ids, err := ParseIDs("1030, 1027 5")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 3 || ids[0] != 1030 || ids[1] != 1027 || ids[2] != 5 {
		t.Errorf("got %v", ids)
	}
	if _, err := ParseIDs("1,x"); err == nil {
		t.Error("expected error for non-numeric id")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/db -run 'TestSnippet|TestCompactLine|TestFormatFull|TestEstimateTokens|TestParseIDs' -v`
Expected: FAIL，`undefined: Snippet`

- [ ] **Step 3: Write minimal implementation**

`internal/db/format.go`：

```go
package db

import (
	"fmt"
	"strconv"
	"strings"
)

// Snippet 把空白壓成單一空格，取前 n 個 rune，超過加「…」。
func Snippet(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// CompactLine 一筆記憶的單行索引格式。
func CompactLine(m Memory, n int, withProject bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "- #%d %s ", m.ID, m.Type)
	if withProject && m.Project != "" {
		b.WriteString(m.Project + " ")
	}
	b.WriteString(m.Created.Format("2006-01-02") + " ")
	b.WriteString(Snippet(m.Content, n))
	return b.String()
}

// FormatFull 一筆記憶的全文格式。
func FormatFull(m Memory) string {
	line := fmt.Sprintf("#%d [%s] %s", m.ID, m.Type, m.Created.Format("2006-01-02"))
	if m.Tags != "" {
		line += fmt.Sprintf("  tags:%s", m.Tags)
	}
	if m.Project != "" {
		line += fmt.Sprintf("  project:%s", m.Project)
	}
	return line + "\n  " + m.Content
}

// EstimateTokens 粗估 token 數：CJK rune 算 1，其他每 4 個 rune 算 1。
func EstimateTokens(s string) int {
	cjk, other := 0, 0
	for _, r := range s {
		if r >= 0x2E80 {
			cjk++
		} else {
			other++
		}
	}
	return cjk + (other+3)/4
}

// ParseIDs 解析逗號或空白分隔的記憶 ID。
func ParseIDs(s string) ([]int64, error) {
	fields := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' })
	ids := make([]int64, 0, len(fields))
	for _, f := range fields {
		id, err := strconv.ParseInt(f, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid id %q: %w", f, err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}
```

`internal/cli/commands.go`：刪除 `formatMemory` 函式，檔內所有 `formatMemory(` 改成 `db.FormatFull(`（`searchCmd`、`listCmd` 各一處）。

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... && go vet ./...`
Expected: 全部 PASS

- [ ] **Step 5: Commit**

```bash
git add internal/db/format.go internal/db/format_test.go internal/cli/commands.go
git commit -m "feat: add compact/full formatting helpers and token estimate"
```

---

### Task 4: GetMany 與 Timeline（DB＋Store interface＋HTTP）

**Files:**
- Modify: `internal/db/db.go`（`Store` interface、新增 `queryMemories`、`GetMany`、`Timeline`）
- Modify: `internal/httpapi/server.go`、`internal/httpapi/client.go`
- Test: `internal/db/db_test.go`、`internal/httpapi/server_test.go`

**Interfaces:**
- Produces:
  - `Store` interface 新增 `GetMany(ids []int64) ([]Memory, error)`、`Timeline(id int64, before, after int) ([]Memory, error)`
  - `GetMany`：依傳入順序回傳找到的記憶，找不到的略過，不報錯；空 slice 回 `nil, nil`
  - `Timeline`：同 project（空字串也算一組）依 `created, id` 排序，回傳 `[前 before 筆（舊→新）, 中心, 後 after 筆（舊→新）]`；before／after 小於 0 視為 0，大於 10 視為 10；中心不存在回錯誤
  - `func (d *DB) queryMemories(query string, args ...any) ([]Memory, error)`：`query` 必須 SELECT `id, type, content, tags, project, created, updated`
  - HTTP：`GET /v1/memories?ids=1,2,3`、`GET /v1/memories/{id}/timeline?before=N&after=N`

- [ ] **Step 1: Write the failing tests**

`internal/db/db_test.go` 末尾加：

```go
func TestGetMany(t *testing.T) {
	d := testDB(t)
	a, _ := d.Store(&Memory{Type: "til", Content: "a"})
	b, _ := d.Store(&Memory{Type: "til", Content: "b"})
	got, err := d.GetMany([]int64{b, 999, a})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != b || got[1].ID != a {
		t.Fatalf("got %+v", got)
	}
	if got, _ := d.GetMany(nil); got != nil {
		t.Errorf("empty ids: got %+v", got)
	}
}

func TestTimeline(t *testing.T) {
	d := testDB(t)
	var p []int64
	for i := 0; i < 5; i++ {
		id, _ := d.Store(&Memory{Type: "summary", Content: fmt.Sprintf("p%d", i), Project: "p"})
		p = append(p, id)
		d.Store(&Memory{Type: "summary", Content: fmt.Sprintf("q%d", i), Project: "q"})
	}

	got, err := d.Timeline(p[2], 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if ids := memIDs(got); !equalIDs(ids, []int64{p[1], p[2], p[3]}) {
		t.Errorf("middle: got %v", ids)
	}

	got, _ = d.Timeline(p[0], 3, 2)
	if ids := memIDs(got); !equalIDs(ids, []int64{p[0], p[1], p[2]}) {
		t.Errorf("first: got %v", ids)
	}

	got, _ = d.Timeline(p[4], 20, 3)
	if ids := memIDs(got); !equalIDs(ids, p) {
		t.Errorf("last with clamp: got %v", ids)
	}

	if _, err := d.Timeline(9999, 3, 3); err == nil {
		t.Error("expected error for missing center")
	}
}

func TestTimelineEmptyProject(t *testing.T) {
	d := testDB(t)
	a, _ := d.Store(&Memory{Type: "til", Content: "a"})
	d.Store(&Memory{Type: "til", Content: "x", Project: "other"})
	b, _ := d.Store(&Memory{Type: "til", Content: "b"})
	got, _ := d.Timeline(a, 3, 3)
	if ids := memIDs(got); !equalIDs(ids, []int64{a, b}) {
		t.Errorf("got %v", ids)
	}
}

func memIDs(ms []Memory) []int64 {
	out := make([]int64, len(ms))
	for i, m := range ms {
		out[i] = m.ID
	}
	return out
}

func equalIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
```

（`db_test.go` import 加 `"fmt"`。）

`internal/httpapi/server_test.go` 末尾加：

```go
func TestClientGetManyAndTimeline(t *testing.T) {
	c, d := testClient(t)
	a, _ := d.Store(&db.Memory{Type: "til", Content: "a", Project: "p"})
	b, _ := d.Store(&db.Memory{Type: "til", Content: "b", Project: "p"})

	got, err := c.GetMany([]int64{b, a})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != b {
		t.Fatalf("GetMany got %+v", got)
	}

	tl, err := c.Timeline(a, 3, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(tl) != 2 || tl[0].ID != a || tl[1].ID != b {
		t.Fatalf("Timeline got %+v", tl)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/db ./internal/httpapi`
Expected: FAIL，`d.GetMany undefined`

- [ ] **Step 3: Implement**

`internal/db/db.go` 的 `Store` interface 在 `Get` 後加兩行：

```go
	GetMany(ids []int64) ([]Memory, error)
	Timeline(id int64, before, after int) ([]Memory, error)
```

`internal/db/db.go` 在 `Get` 之後加：

```go
const selectMemoryCols = `SELECT id, type, content, tags, project, created, updated FROM memories`

// queryMemories 執行 SELECT selectMemoryCols 查詢並掃描成 []Memory。
func (d *DB) queryMemories(query string, args ...any) ([]Memory, error) {
	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Memory
	for rows.Next() {
		var m Memory
		var created, updated string
		if err := rows.Scan(&m.ID, &m.Type, &m.Content, &m.Tags, &m.Project, &created, &updated); err != nil {
			return nil, err
		}
		m.Created, _ = time.Parse(timeLayout, created)
		m.Updated, _ = time.Parse(timeLayout, updated)
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetMany 依傳入順序取得多筆記憶，找不到的 ID 略過。
func (d *DB) GetMany(ids []int64) ([]Memory, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	found, err := d.queryMemories(selectMemoryCols+` WHERE id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("get many: %w", err)
	}
	byID := make(map[int64]Memory, len(found))
	for _, m := range found {
		byID[m.ID] = m
	}
	var out []Memory
	for _, id := range ids {
		if m, ok := byID[id]; ok {
			out = append(out, m)
		}
	}
	return out, nil
}

const timelineMax = 10

func clampTimeline(n int) int {
	return min(max(n, 0), timelineMax)
}

// Timeline 回傳同 project 依時間排序、以 id 為中心的前後記憶（含中心，舊到新）。
func (d *DB) Timeline(id int64, before, after int) ([]Memory, error) {
	center, err := d.Get(id)
	if err != nil {
		return nil, fmt.Errorf("timeline: %w", err)
	}
	created := center.Created.Format(timeLayout)

	prev, err := d.queryMemories(selectMemoryCols+
		` WHERE project = ? AND (created < ? OR (created = ? AND id < ?)) ORDER BY created DESC, id DESC LIMIT ?`,
		center.Project, created, created, id, clampTimeline(before))
	if err != nil {
		return nil, fmt.Errorf("timeline before: %w", err)
	}
	next, err := d.queryMemories(selectMemoryCols+
		` WHERE project = ? AND (created > ? OR (created = ? AND id > ?)) ORDER BY created ASC, id ASC LIMIT ?`,
		center.Project, created, created, id, clampTimeline(after))
	if err != nil {
		return nil, fmt.Errorf("timeline after: %w", err)
	}

	out := make([]Memory, 0, len(prev)+1+len(next))
	for i := len(prev) - 1; i >= 0; i-- {
		out = append(out, prev[i])
	}
	out = append(out, *center)
	return append(out, next...), nil
}
```

`internal/httpapi/server.go` 在 `GET /v1/memories/{id}` 前加：

```go
	mux.HandleFunc("GET /v1/memories", func(w http.ResponseWriter, r *http.Request) {
		ids, err := db.ParseIDs(r.URL.Query().Get("ids"))
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		memories, err := s.store.GetMany(ids)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, memories)
	})

	mux.HandleFunc("GET /v1/memories/{id}/timeline", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		q := r.URL.Query()
		before, _ := strconv.Atoi(q.Get("before"))
		after, _ := strconv.Atoi(q.Get("after"))
		memories, err := s.store.Timeline(id, before, after)
		if err != nil {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, http.StatusOK, memories)
	})
```

`internal/httpapi/client.go` 在 `Get` 後加（import 加 `"strings"`）：

```go
// GetMany 見 db.Store。
func (c *Client) GetMany(ids []int64) ([]db.Memory, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	q := url.Values{}
	q.Set("ids", strings.Join(parts, ","))
	var memories []db.Memory
	if err := c.do(http.MethodGet, "/v1/memories", q, nil, &memories); err != nil {
		return nil, err
	}
	return memories, nil
}

// Timeline 見 db.Store。
func (c *Client) Timeline(id int64, before, after int) ([]db.Memory, error) {
	q := url.Values{}
	q.Set("before", strconv.Itoa(before))
	q.Set("after", strconv.Itoa(after))
	var memories []db.Memory
	if err := c.do(http.MethodGet, "/v1/memories/"+strconv.FormatInt(id, 10)+"/timeline", q, nil, &memories); err != nil {
		return nil, err
	}
	return memories, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... && go vet ./...`
Expected: 全部 PASS

- [ ] **Step 5: Commit**

```bash
git add internal/db internal/httpapi
git commit -m "feat: add GetMany and Timeline to Store and REST API"
```

---

### Task 5: 漸進揭露 MCP tools 與 CLI

**Files:**
- Modify: `internal/mcp/server.go`（`handleSearch`、新增 `handleGet`、`handleTimeline`、`MCPServer` 註冊）
- Modify: `internal/cli/commands.go`（`searchCmd --compact`、新增 `getCmd`、`timelineCmd`）
- Test: `internal/mcp/server_test.go`

**Interfaces:**
- Consumes: `CompactLine`、`FormatFull`、`ParseIDs`（Task 3）；`GetMany`、`Timeline`（Task 4）
- Produces: MCP tools `memory_get(ids string)`、`memory_timeline(id number, before number, after number)`；`memory_search` 新參數 `compact bool`（預設 true）；CLI `get <id>...`、`timeline <id> [--before N] [--after N]`、`search --compact`
- 常數：`const getMaxIDs = 20`、`const searchSnippetRunes = 80`

- [ ] **Step 1: Write the failing tests**

`internal/mcp/server_test.go` 的 `callTool` switch 加：

```go
	case "memory_get":
		handler = s.handleGet
	case "memory_timeline":
		handler = s.handleTimeline
	case "memory_context":
		handler = s.handleContext
```

末尾加：

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/mcp`
Expected: FAIL，`s.handleGet undefined`

- [ ] **Step 3: Implement**

`internal/mcp/server.go` 加 import `"strings"`，`handleSearch` 改成：

```go
const (
	searchSnippetRunes = 80
	getMaxIDs          = 20
)

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
	return gomcp.NewToolResultText(timelineText(memories, id)), nil
}

func timelineText(memories []db.Memory, center int64) string {
	lines := make([]string, len(memories))
	for i, m := range memories {
		line := db.CompactLine(m, searchSnippetRunes, false)
		if m.ID == center {
			line = "→" + strings.TrimPrefix(line, "-")
		}
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}
```

`MCPServer()` 裡 `memory_search` 的定義改成：

```go
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
```

`internal/cli/commands.go`：

`init()` 加：

```go
	searchCmd.Flags().Bool("compact", false, "print one index line per result")

	timelineCmd.Flags().Int("before", 3, "earlier memories to show (max 10)")
	timelineCmd.Flags().Int("after", 3, "later memories to show (max 10)")
```

`rootCmd.AddCommand(...)` 加入 `getCmd, timelineCmd`。

`searchCmd` 的非 JSON 輸出段改成：

```go
		if compact, _ := cmd.Flags().GetBool("compact"); compact {
			for _, r := range results {
				fmt.Println(db.CompactLine(r.Memory, 80, true))
			}
			return nil
		}
		for i, r := range results {
			if i > 0 {
				fmt.Println()
			}
			fmt.Println(db.FormatFull(r.Memory))
		}
		return nil
```

新增指令：

```go
var getCmd = &cobra.Command{
	Use:   "get <id>...",
	Short: "Show full content of memories by id",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ids, err := db.ParseIDs(strings.Join(args, " "))
		if err != nil {
			return err
		}
		d, err := openStore()
		if err != nil {
			return err
		}
		defer d.Close()

		memories, err := d.GetMany(ids)
		if err != nil {
			return err
		}
		if jsonFlag {
			return printJSON(memories)
		}
		found := make(map[int64]bool, len(memories))
		for i, m := range memories {
			found[m.ID] = true
			if i > 0 {
				fmt.Println()
			}
			fmt.Println(db.FormatFull(m))
		}
		for _, id := range ids {
			if !found[id] {
				fmt.Fprintf(os.Stderr, "#%d not found\n", id)
			}
		}
		return nil
	},
}

var timelineCmd = &cobra.Command{
	Use:   "timeline <id>",
	Short: "Show memories before and after a memory in the same project",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid id: %w", err)
		}
		before, _ := cmd.Flags().GetInt("before")
		after, _ := cmd.Flags().GetInt("after")

		d, err := openStore()
		if err != nil {
			return err
		}
		defer d.Close()

		memories, err := d.Timeline(id, before, after)
		if err != nil {
			return err
		}
		if jsonFlag {
			return printJSON(memories)
		}
		for _, m := range memories {
			line := db.CompactLine(m, 80, false)
			if m.ID == id {
				line = "→" + strings.TrimPrefix(line, "-")
			}
			fmt.Println(line)
		}
		return nil
	},
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... && go vet ./...`
Expected: 全部 PASS

- [ ] **Step 5: Commit**

```bash
git add internal/mcp internal/cli/commands.go
git commit -m "feat: add compact search, memory_get and memory_timeline"
```

---

### Task 6: 專案三段式 Context 與 `context --hook`

**Files:**
- Create: `internal/hook/hook.go`、`internal/hook/hook_test.go`
- Modify: `internal/db/db.go`（`ContextOptions.Full`、`Context` 改寫、新增 `projectContext`）
- Create: `internal/db/context_test.go`
- Modify: `internal/httpapi/client.go`、`internal/httpapi/server.go`（`full` 參數）
- Modify: `internal/mcp/server.go`（`memory_context` 加 `full`）
- Modify: `internal/cli/commands.go`（`contextCmd` 加 `--hook`、`--full`）

**Interfaces:**
- Consumes: `CompactLine`、`EstimateTokens`（Task 3）、`queryMemories`、`selectMemoryCols`（Task 4）
- Produces:
  - `package hook`：`const SummarizingEnv = "MEMORY_MCP_SUMMARIZING"`、`type Input struct { SessionID, TranscriptPath, Cwd, Reason, Source string }`（json tag：`session_id`、`transcript_path`、`cwd`、`reason`、`source`）、`func Read(r io.Reader) Input`（解析失敗回零值）、`func ReadStdin() Input`（stdin 是 TTY 時不讀，回零值）、`func Project(cwd string) string`（cwd 空字串時用 `os.Getwd()`）
  - `ContextOptions.Full bool`
  - `const contextSnippetRunes = 120`、`const contextBudgetRunes = 6000`
  - `Context`：`Project != "" && Type == ""` → 三段式；全部段落為空回 `""`。其他情況：`Full` → 舊分組全文格式；否則 `## Memories (N entries)` + `CompactLine(m, 120, true)` 各行 + 結尾兩行

- [ ] **Step 1: Write the failing tests**

`internal/hook/hook_test.go`：

```go
package hook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRead(t *testing.T) {
	in := Read(strings.NewReader(`{"session_id":"s1","transcript_path":"/t.jsonl","cwd":"/Users/white/Tyche/Kairos","reason":"exit"}`))
	if in.SessionID != "s1" || in.TranscriptPath != "/t.jsonl" || in.Cwd != "/Users/white/Tyche/Kairos" || in.Reason != "exit" {
		t.Errorf("got %+v", in)
	}
}

func TestReadEmptyInput(t *testing.T) {
	if in := Read(strings.NewReader("")); in != (Input{}) {
		t.Errorf("got %+v", in)
	}
	if in := Read(strings.NewReader("not json")); in != (Input{}) {
		t.Errorf("got %+v", in)
	}
}

func TestProject(t *testing.T) {
	if got := Project("/Users/white/Tyche/Kairos"); got != "kairos" {
		t.Errorf("got %q", got)
	}
	if got := Project("/Users/white/Documents/ggw.studio"); got != "ggw.studio" {
		t.Errorf("got %q", got)
	}
	wd, _ := os.Getwd()
	if got := Project(""); got != strings.ToLower(filepath.Base(wd)) {
		t.Errorf("got %q", got)
	}
}
```

`internal/db/context_test.go`：

```go
package db

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestProjectContextSections(t *testing.T) {
	d := testDB(t)
	d.Store(&Memory{Type: "feedback", Content: "global pref"})
	d.Store(&Memory{Type: "feedback", Content: "global tagged", Project: "global"})
	d.Store(&Memory{Type: "feedback", Content: "other project pref", Project: "yuna"})
	for i := 0; i < 4; i++ {
		d.Store(&Memory{Type: "summary", Content: "sum", Project: "kairos"})
	}
	d.Store(&Memory{Type: "til", Content: "kairos til", Project: "kairos"})
	d.Store(&Memory{Type: "til", Content: "yuna til", Project: "yuna"})

	out, err := d.Context(ContextOptions{Project: "kairos"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"### 全域 feedback", "global pref", "global tagged", "### kairos summary", "### kairos 其他記憶", "kairos til", "memory_get", "tokens)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	for _, bad := range []string{"other project pref", "yuna til"} {
		if strings.Contains(out, bad) {
			t.Errorf("unexpected %q in:\n%s", bad, out)
		}
	}
	if n := strings.Count(out, " summary "); n != 3 {
		t.Errorf("summary lines = %d, want 3", n)
	}
}

func TestProjectContextEmpty(t *testing.T) {
	d := testDB(t)
	d.Store(&Memory{Type: "til", Content: "x", Project: "other"})
	out, err := d.Context(ContextOptions{Project: "nothing"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "" {
		t.Errorf("want empty, got %q", out)
	}
}

func TestFitBudget(t *testing.T) {
	header := "## h\n\n"
	sections := []contextSection{
		{"### a", []string{"a1", "a2"}},
		{"### b", []string{strings.Repeat("長", 40), strings.Repeat("長", 40)}},
	}
	body := fitBudget(header, sections, 70)
	if n := utf8.RuneCountInString(body); n > 70 {
		t.Errorf("runes = %d, want <= 70:\n%s", n, body)
	}
	if !strings.Contains(body, "a1") || !strings.Contains(body, "### b") {
		t.Errorf("should drop from the last section first:\n%s", body)
	}
	if strings.Count(body, strings.Repeat("長", 40)) != 1 {
		t.Errorf("want exactly one b line left:\n%s", body)
	}
}

func TestContextFullAndCompact(t *testing.T) {
	d := testDB(t)
	d.Store(&Memory{Type: "til", Content: strings.Repeat("a", 300), Project: "p"})

	compact, _ := d.Context(ContextOptions{Limit: 5})
	if !strings.Contains(compact, "- #1 til p ") || !strings.Contains(compact, "…") {
		t.Errorf("compact = %q", compact)
	}
	full, _ := d.Context(ContextOptions{Limit: 5, Full: true})
	if !strings.Contains(full, strings.Repeat("a", 300)) {
		t.Errorf("full should contain whole content")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/hook ./internal/db`
Expected: FAIL，`no Go files` / `undefined: contextSection`

- [ ] **Step 3: Implement**

`internal/hook/hook.go`：

```go
// Package hook 讀取 Claude Code hook 的 stdin JSON 並推算 project 名稱。
package hook

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// SummarizingEnv 自動 summary 呼叫 claude -p 時設定，讓 hook 跳過避免遞迴。
const SummarizingEnv = "MEMORY_MCP_SUMMARIZING"

// Input Claude Code hook 傳入的欄位（只取用到的）。
type Input struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Cwd            string `json:"cwd"`
	Reason         string `json:"reason"`
	Source         string `json:"source"`
}

// Read 解析 hook JSON，失敗回零值。
func Read(r io.Reader) Input {
	var in Input
	if err := json.NewDecoder(r).Decode(&in); err != nil {
		return Input{}
	}
	return in
}

// ReadStdin 讀 stdin 的 hook JSON；stdin 是終端機時不讀，直接回零值。
func ReadStdin() Input {
	fi, err := os.Stdin.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice != 0 {
		return Input{}
	}
	return Read(os.Stdin)
}

// Project 以工作目錄 basename 小寫作為 project；cwd 為空時用目前目錄。
func Project(cwd string) string {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	return strings.ToLower(filepath.Base(cwd))
}
```

`internal/db/db.go`：

`ContextOptions` 改成：

```go
// ContextOptions 摘要查詢參數。Project 有值且 Type 為空時輸出專案三段式索引。
type ContextOptions struct {
	Type    string
	Project string
	Limit   int
	Full    bool
}
```

整個 `Context` 函式換成：

```go
const (
	contextSnippetRunes = 120
	contextBudgetRunes  = 6000
	contextFooter       = "全文用 memory_get(ids)，前後脈絡用 memory_timeline(id)。"
)

// Context 產生 bounded 的記憶摘要。
func (d *DB) Context(opts ContextOptions) (string, error) {
	if opts.Project != "" && opts.Type == "" {
		return d.projectContext(opts.Project)
	}

	query := selectMemoryCols
	var args []any
	var where []string
	if opts.Type != "" {
		where = append(where, "type = ?")
		args = append(args, opts.Type)
	}
	if opts.Project != "" {
		where = append(where, "project = ?")
		args = append(args, opts.Project)
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	query += ` ORDER BY created DESC, id DESC LIMIT ?`
	args = append(args, limit)

	memories, err := d.queryMemories(query, args...)
	if err != nil {
		return "", fmt.Errorf("context: %w", err)
	}
	if len(memories) == 0 {
		return "No memories stored yet.", nil
	}
	if opts.Full {
		return fullContext(memories), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "## Memories (%d entries)\n\n", len(memories))
	for _, m := range memories {
		b.WriteString(CompactLine(m, contextSnippetRunes, true))
		b.WriteByte('\n')
	}
	return withFooter(b.String()), nil
}

// fullContext 舊版依 type 分組的全文格式。
func fullContext(memories []Memory) string {
	grouped := make(map[string][]string)
	var typeOrder []string
	for _, m := range memories {
		line := fmt.Sprintf("- #%d %s", m.ID, m.Content)
		if m.Tags != "" {
			line += fmt.Sprintf(" [%s]", m.Tags)
		}
		if _, ok := grouped[m.Type]; !ok {
			typeOrder = append(typeOrder, m.Type)
		}
		grouped[m.Type] = append(grouped[m.Type], line)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## Memories (%d entries)\n\n", len(memories))
	for _, typ := range typeOrder {
		items := grouped[typ]
		fmt.Fprintf(&b, "### %s (%d)\n", typ, len(items))
		for _, item := range items {
			b.WriteString(item)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	return b.String()
}

type contextSection struct {
	title string
	lines []string
}

// projectContext 專案三段式索引：全域 feedback、專案 summary、專案其他記憶。
func (d *DB) projectContext(project string) (string, error) {
	global, err := d.queryMemories(selectMemoryCols+
		` WHERE type = 'feedback' AND project IN ('', 'global') ORDER BY created DESC, id DESC LIMIT 10`)
	if err != nil {
		return "", fmt.Errorf("context global: %w", err)
	}
	summaries, err := d.queryMemories(selectMemoryCols+
		` WHERE type = 'summary' AND project = ? ORDER BY created DESC, id DESC LIMIT 3`, project)
	if err != nil {
		return "", fmt.Errorf("context summary: %w", err)
	}
	others, err := d.queryMemories(selectMemoryCols+
		` WHERE type IN ('til', 'knowledge', 'feedback') AND project = ? ORDER BY created DESC, id DESC LIMIT 10`, project)
	if err != nil {
		return "", fmt.Errorf("context others: %w", err)
	}

	seen := make(map[int64]bool)
	toLines := func(ms []Memory) []string {
		var lines []string
		for _, m := range ms {
			if seen[m.ID] {
				continue
			}
			seen[m.ID] = true
			lines = append(lines, CompactLine(m, contextSnippetRunes, false))
		}
		return lines
	}
	sections := []contextSection{
		{"### 全域 feedback", toLines(global)},
		{fmt.Sprintf("### %s summary", project), toLines(summaries)},
		{fmt.Sprintf("### %s 其他記憶", project), toLines(others)},
	}

	header := fmt.Sprintf("## Memory context（project: %s）\n\n", project)
	if renderSections(header, sections) == header {
		return "", nil
	}
	return withFooter(fitBudget(header, sections, contextBudgetRunes)), nil
}

// fitBudget 從最後一段尾端逐行刪除，直到內容不超過 budget rune。
func fitBudget(header string, sections []contextSection, budget int) string {
	body := renderSections(header, sections)
	for utf8.RuneCountInString(body) > budget && dropLastLine(sections) {
		body = renderSections(header, sections)
	}
	return body
}

func renderSections(header string, sections []contextSection) string {
	var b strings.Builder
	b.WriteString(header)
	for _, s := range sections {
		if len(s.lines) == 0 {
			continue
		}
		b.WriteString(s.title + "\n")
		for _, l := range s.lines {
			b.WriteString(l + "\n")
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// dropLastLine 從最後一個非空段落刪一行，沒有可刪時回 false。
func dropLastLine(sections []contextSection) bool {
	for i := len(sections) - 1; i >= 0; i-- {
		if n := len(sections[i].lines); n > 0 {
			sections[i].lines = sections[i].lines[:n-1]
			return true
		}
	}
	return false
}

func withFooter(body string) string {
	s := body + contextFooter + "\n"
	return s + fmt.Sprintf("(約 %d tokens)\n", EstimateTokens(s))
}
```

`db.go` import 加 `"unicode/utf8"`。

`internal/httpapi/client.go` 的 `Context` 在 `setIfNonEmpty(q, "project", opts.Project)` 後加：

```go
	if opts.Full {
		q.Set("full", "1")
	}
```

`internal/httpapi/server.go` 的 `GET /v1/context` 改成：

```go
		summary, err := s.store.Context(db.ContextOptions{
			Type: q.Get("type"), Project: q.Get("project"), Limit: limit, Full: q.Get("full") == "1",
		})
```

`internal/mcp/server.go`：`handleContext` 的 `ContextOptions` 加 `Full: req.GetBool("full", false),`；`memory_context` 定義改成：

```go
	srv.AddTool(gomcp.NewTool("memory_context",
		gomcp.WithDescription("Get an index of recent memories (one line each, 120-char snippet). With project and no type: global feedback + that project's latest summaries + other memories. Use at the START of a session or when switching projects; then call memory_get for full content."),
		gomcp.WithString("type", gomcp.Description("Filter by type: feedback, til, summary, knowledge")),
		gomcp.WithString("project", gomcp.Description("Project name (lowercase basename of the working directory)")),
		gomcp.WithNumber("limit", gomcp.Description("Max memories when not in project mode (default 20)")),
		gomcp.WithBoolean("full", gomcp.Description("true = full content grouped by type (legacy format)")),
	), s.handleContext)
```

`internal/cli/commands.go`：

import 加 `"memory-mcp/internal/hook"`。`init()` 加：

```go
	contextCmd.Flags().Bool("full", false, "print full content instead of index lines")
	contextCmd.Flags().Bool("hook", false, "Claude Code SessionStart hook mode: read hook JSON from stdin, use cwd basename as project, never fail")
```

`contextCmd` RunE 改成：

```go
	RunE: func(cmd *cobra.Command, args []string) error {
		if isHook, _ := cmd.Flags().GetBool("hook"); isHook {
			runContextHook()
			return nil
		}
		typ, _ := cmd.Flags().GetString("type")
		project, _ := cmd.Flags().GetString("project")
		limit, _ := cmd.Flags().GetInt("limit")
		full, _ := cmd.Flags().GetBool("full")

		d, err := openStore()
		if err != nil {
			return err
		}
		defer d.Close()

		summary, err := d.Context(db.ContextOptions{
			Type: typ, Project: project, Limit: limit, Full: full,
		})
		if err != nil {
			return err
		}
		fmt.Print(summary)
		return nil
	},
```

新增：

```go
// runContextHook SessionStart hook：任何錯誤都靜默，不輸出也不回傳錯誤。
func runContextHook() {
	if os.Getenv(hook.SummarizingEnv) == "1" {
		return
	}
	in := hook.ReadStdin()
	d, err := openStore()
	if err != nil {
		return
	}
	defer d.Close()
	out, err := d.Context(db.ContextOptions{Project: hook.Project(in.Cwd)})
	if err != nil {
		return
	}
	fmt.Print(out)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... && go vet ./...`
Expected: 全部 PASS

- [ ] **Step 5: 手動確認輸出**

Run: `go build -o /tmp/memctx . && echo '{"cwd":"/Users/white/github/memory-mcp"}' | /tmp/memctx context --hook`
Expected: 看到 `## Memory context（project: memory-mcp）`、全域 feedback 段、`memory-mcp` 段、結尾 token 估計，且總量明顯小於舊輸出。
Run: `/tmp/memctx context --hook < /dev/null; echo "exit=$?"`
Expected: `exit=0`

- [ ] **Step 6: Commit**

```bash
git add internal/hook internal/db internal/httpapi internal/mcp internal/cli/commands.go
git commit -m "feat: project-scoped compact context and SessionStart hook mode"
```

---

### Task 7: summarize 套件（transcript 解析與 Run）

**Files:**
- Create: `internal/summarize/transcript.go`
- Create: `internal/summarize/summarize.go`
- Test: `internal/summarize/summarize_test.go`

**Interfaces:**
- Consumes: `hook.Input`、`hook.Project`、`hook.SummarizingEnv`（Task 6）；`db.Store`、`db.ListOptions`
- Produces:
  - `type Transcript struct { Start time.Time; UserTurns int; Text string }`
  - `func ParseTranscript(r io.Reader, maxRunes int) (*Transcript, error)`
  - `func BuildPrompt(project string, date time.Time, transcript string) string`
  - `type Runner func(ctx context.Context, prompt string) (string, error)`
  - `type Deps struct { Store db.Store; Run Runner; Logf func(format string, args ...any) }`
  - `func Run(ctx context.Context, in hook.Input, deps Deps) error`
  - `func ClaudeRunner(ctx context.Context, prompt string) (string, error)`
  - 常數：`MaxTranscriptRunes = 60000`、`MinUserTurns = 3`、`AutoTag = "auto-summary"`

transcript 格式（Claude Code JSONL，每行一個物件）：
- `type` 為 `user`／`assistant` 才處理；`isMeta` 或 `isSidechain` 為 true 的跳過。
- `message.content` 為字串 → 文字；為陣列 → 只取 `type=="text"` 的 `text`（`tool_result`、`tool_use`、`thinking` 都略過）。
- `timestamp` 為 RFC3339（例如 `2026-09-26T01:49:28.355Z`），第一個可解析的當作 `Start`。
- 單行可能很長（tool 輸出），用 `bufio.Reader.ReadBytes('\n')`，NEVER 用預設 buffer 的 `bufio.Scanner`。

- [ ] **Step 1: Write the failing tests**

`internal/summarize/summarize_test.go`：

```go
package summarize

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"memory-mcp/internal/db"
	"memory-mcp/internal/hook"
)

const fixture = `{"type":"mode","sessionId":"s"}
{"type":"user","isMeta":true,"timestamp":"2000-01-01T01:00:00.000Z","message":{"role":"user","content":"<local-command-caveat>ignore</local-command-caveat>"}}
{"type":"user","timestamp":"2000-01-01T01:00:01.000Z","message":{"role":"user","content":"第一個問題"}}
{"type":"assistant","timestamp":"2000-01-01T01:00:02.000Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"secret thoughts"},{"type":"text","text":"第一個回答"}]}}
{"type":"assistant","timestamp":"2000-01-01T01:00:03.000Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{}}]}}
{"type":"user","timestamp":"2000-01-01T01:00:04.000Z","message":{"role":"user","content":[{"type":"tool_result","content":"TOOL OUTPUT"}]}}
{"type":"user","timestamp":"2000-01-01T01:00:05.000Z","message":{"role":"user","content":[{"type":"text","text":"第二個問題"}]}}
{"type":"user","isSidechain":true,"timestamp":"2000-01-01T01:00:06.000Z","message":{"role":"user","content":"subagent prompt"}}
{"type":"user","timestamp":"2000-01-01T01:00:07.000Z","message":{"role":"user","content":"第三個問題"}}
`

func TestParseTranscript(t *testing.T) {
	tr, err := ParseTranscript(strings.NewReader(fixture), MaxTranscriptRunes)
	if err != nil {
		t.Fatal(err)
	}
	if tr.UserTurns != 3 {
		t.Errorf("UserTurns = %d, want 3", tr.UserTurns)
	}
	want := time.Date(2000, 1, 1, 1, 0, 0, 0, time.UTC)
	if !tr.Start.Equal(want) {
		t.Errorf("Start = %v, want %v", tr.Start, want)
	}
	for _, s := range []string{"[user] 第一個問題", "[assistant] 第一個回答", "[user] 第二個問題", "[user] 第三個問題"} {
		if !strings.Contains(tr.Text, s) {
			t.Errorf("missing %q in %q", s, tr.Text)
		}
	}
	for _, s := range []string{"TOOL OUTPUT", "secret thoughts", "ignore", "subagent prompt"} {
		if strings.Contains(tr.Text, s) {
			t.Errorf("unexpected %q in %q", s, tr.Text)
		}
	}
}

func TestParseTranscriptTruncatesFromFront(t *testing.T) {
	tr, _ := ParseTranscript(strings.NewReader(fixture), 10)
	if n := len([]rune(tr.Text)); n != 10 {
		t.Errorf("runes = %d, want 10", n)
	}
	if !strings.HasSuffix(tr.Text, "第三個問題") {
		t.Errorf("should keep tail, got %q", tr.Text)
	}
}

func setup(t *testing.T, transcript string) (hook.Input, *db.DB) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "t.jsonl")
	if err := os.WriteFile(path, []byte(transcript), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(dir, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return hook.Input{SessionID: "s", TranscriptPath: path, Cwd: "/x/Kairos"}, d
}

func deps(d db.Store, out string, called *int) Deps {
	return Deps{
		Store: d,
		Run: func(_ context.Context, prompt string) (string, error) {
			*called++
			return out, nil
		},
		Logf: func(string, ...any) {},
	}
}

func TestRunStoresSummary(t *testing.T) {
	in, d := setup(t, fixture)
	called := 0
	if err := Run(context.Background(), in, deps(d, "  2026-09-28 做了 X\n", &called)); err != nil {
		t.Fatal(err)
	}
	ms, _ := d.List(db.ListOptions{Type: "summary"})
	if called != 1 || len(ms) != 1 {
		t.Fatalf("called=%d stored=%d", called, len(ms))
	}
	if ms[0].Project != "kairos" || ms[0].Tags != AutoTag || ms[0].Content != "2026-09-28 做了 X" {
		t.Errorf("got %+v", ms[0])
	}
}

func TestRunSkipsShortSession(t *testing.T) {
	short := strings.Join(strings.Split(fixture, "\n")[:4], "\n") + "\n"
	in, d := setup(t, short)
	called := 0
	Run(context.Background(), in, deps(d, "x", &called))
	if called != 0 {
		t.Error("runner should not be called for short session")
	}
}

func TestRunSkipsWhenManualSummaryExists(t *testing.T) {
	in, d := setup(t, fixture)
	d.Store(&db.Memory{Type: "summary", Content: "manual", Project: "kairos"})
	called := 0
	Run(context.Background(), in, deps(d, "x", &called))
	if called != 0 {
		t.Error("runner should not be called when manual summary exists")
	}
}

func TestRunIgnoresOlderSummary(t *testing.T) {
	old := strings.ReplaceAll(fixture, "2000-01-01", "2099-01-01")
	in, d := setup(t, old)
	d.Store(&db.Memory{Type: "summary", Content: "older", Project: "kairos"})
	called := 0
	Run(context.Background(), in, deps(d, "new summary", &called))
	if called != 1 {
		t.Error("summary stored before session start should not block")
	}
}

func TestRunSkipOutput(t *testing.T) {
	in, d := setup(t, fixture)
	called := 0
	Run(context.Background(), in, deps(d, "SKIP", &called))
	ms, _ := d.List(db.ListOptions{Type: "summary"})
	if len(ms) != 0 {
		t.Error("SKIP output should not be stored")
	}
}

func TestBuildPrompt(t *testing.T) {
	p := BuildPrompt("kairos", time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC), "[user] hi")
	for _, s := range []string{"project: kairos", "2026-09-28", "繁體中文", "SKIP", "<transcript>\n[user] hi\n</transcript>"} {
		if !strings.Contains(p, s) {
			t.Errorf("missing %q", s)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/summarize`
Expected: FAIL，`no Go files` 或 `undefined: ParseTranscript`

- [ ] **Step 3: Implement**

`internal/summarize/transcript.go`：

```go
// Package summarize 在 session 結束時把 Claude Code transcript 摘要成 summary 記憶。
package summarize

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
)

// Transcript 從 JSONL 抽出的對話文字。
type Transcript struct {
	Start     time.Time
	UserTurns int
	Text      string
}

type transcriptLine struct {
	Type        string `json:"type"`
	IsMeta      bool   `json:"isMeta"`
	IsSidechain bool   `json:"isSidechain"`
	Timestamp   string `json:"timestamp"`
	Message     struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ParseTranscript 只保留 user／assistant 的文字（排除 tool、thinking、meta、sidechain），超過 maxRunes 時保留尾端。
func ParseTranscript(r io.Reader, maxRunes int) (*Transcript, error) {
	br := bufio.NewReader(r)
	tr := &Transcript{}
	var parts []string
	for {
		raw, err := br.ReadBytes('\n')
		if len(raw) > 0 {
			var l transcriptLine
			if json.Unmarshal(raw, &l) == nil {
				if tr.Start.IsZero() && l.Timestamp != "" {
					if ts, perr := time.Parse(time.RFC3339Nano, l.Timestamp); perr == nil {
						tr.Start = ts.UTC()
					}
				}
				if text := lineText(l); text != "" {
					parts = append(parts, "["+l.Type+"] "+text)
					if l.Type == "user" {
						tr.UserTurns++
					}
				}
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	text := []rune(strings.Join(parts, "\n\n"))
	if len(text) > maxRunes {
		text = text[len(text)-maxRunes:]
	}
	tr.Text = string(text)
	return tr, nil
}

func lineText(l transcriptLine) string {
	if (l.Type != "user" && l.Type != "assistant") || l.IsMeta || l.IsSidechain || len(l.Message.Content) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(l.Message.Content, &s) == nil {
		return strings.TrimSpace(s)
	}
	var blocks []contentBlock
	if json.Unmarshal(l.Message.Content, &blocks) != nil {
		return ""
	}
	var texts []string
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			texts = append(texts, strings.TrimSpace(b.Text))
		}
	}
	return strings.Join(texts, "\n")
}
```

`internal/summarize/summarize.go`：

```go
package summarize

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"memory-mcp/internal/db"
	"memory-mcp/internal/hook"
)

const (
	MaxTranscriptRunes = 60000
	MinUserTurns       = 3
	AutoTag            = "auto-summary"
	skipToken          = "SKIP"
)

// Runner 把 prompt 交給 LLM 並回傳輸出。
type Runner func(ctx context.Context, prompt string) (string, error)

// Deps Run 的外部依賴，測試時可替換。
type Deps struct {
	Store db.Store
	Run   Runner
	Logf  func(format string, args ...any)
}

// BuildPrompt 組出摘要 prompt。
func BuildPrompt(project string, date time.Time, transcript string) string {
	return fmt.Sprintf(`以下是一段 Claude Code 對話紀錄（project: %s，日期: %s）。
用繁體中文寫一段工作摘要，600 字以內，開頭寫日期，包含：
1. 做了什麼
2. 關鍵決定與理由
3. 還沒做完的部分
保留具體識別資訊（檔案路徑、MR 編號、指令、主機名）。
不要寫出密碼、金鑰、token 的值。
如果這段對話只是閒聊、沒有實際工作內容，只輸出 SKIP。

<transcript>
%s
</transcript>
`, project, date.Format("2006-01-02"), transcript)
}

// Run 解析 transcript，符合條件時產生並儲存 summary。跳過時回傳 nil。
func Run(ctx context.Context, in hook.Input, deps Deps) error {
	project := hook.Project(in.Cwd)
	logf := func(format string, args ...any) {
		deps.Logf(in.SessionID+" "+project+" "+format, args...)
	}

	f, err := os.Open(in.TranscriptPath)
	if err != nil {
		logf("open transcript: %v", err)
		return err
	}
	tr, err := ParseTranscript(f, MaxTranscriptRunes)
	f.Close()
	if err != nil {
		logf("parse transcript: %v", err)
		return err
	}
	if tr.UserTurns < MinUserTurns {
		logf("skip: %d user turns", tr.UserTurns)
		return nil
	}

	recent, err := deps.Store.List(db.ListOptions{Type: "summary", Limit: 50})
	if err != nil {
		logf("list summaries: %v", err)
		return err
	}
	start := tr.Start.Truncate(time.Second)
	for _, m := range recent {
		if m.Project == project && !m.Created.Before(start) {
			logf("skip: manual summary #%d exists", m.ID)
			return nil
		}
	}

	date := tr.Start
	if date.IsZero() {
		date = time.Now()
	}
	out, err := deps.Run(ctx, BuildPrompt(project, date.Local(), tr.Text))
	if err != nil {
		logf("runner: %v", err)
		return err
	}
	out = strings.TrimSpace(out)
	if out == "" || out == skipToken {
		logf("skip: model returned %q", out)
		return nil
	}

	id, err := deps.Store.Store(&db.Memory{Type: "summary", Content: out, Tags: AutoTag, Project: project})
	if err != nil {
		logf("store: %v", err)
		return err
	}
	logf("stored #%d", id)
	return nil
}

// ClaudeRunner 以 claude -p（haiku、不保存 session）產生摘要，並設定防遞迴環境變數。
func ClaudeRunner(ctx context.Context, prompt string) (string, error) {
	cmd := exec.CommandContext(ctx, "claude", "-p", "--model", "haiku", "--no-session-persistence")
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Env = append(os.Environ(), hook.SummarizingEnv+"=1")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("claude -p: %w", err)
	}
	return string(out), nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... && go vet ./...`
Expected: 全部 PASS

- [ ] **Step 5: Commit**

```bash
git add internal/summarize
git commit -m "feat: add transcript parsing and auto-summary runner"
```

---

### Task 8: `summarize` CLI（hook 前景＋背景 worker）

**Files:**
- Modify: `internal/cli/commands.go`（新增 `summarizeCmd`、`runSummarizeHook`、`runSummarizeWorker`、`summarizeLogf`）

**Interfaces:**
- Consumes: `hook.Read`、`hook.ReadStdin`、`hook.SummarizingEnv`（Task 6）；`summarize.Run`、`summarize.Deps`、`summarize.ClaudeRunner`（Task 7）
- Produces: `memory-mcp summarize --hook`（前景：存暫存檔、fork `summarize --worker <file>` 後立即返回）與 `memory-mcp summarize --worker <file>`（背景：讀檔刪檔、執行 `summarize.Run`，timeout 3 分鐘）

- [ ] **Step 1: Implement**

`internal/cli/commands.go` import 加 `"io"`、`"os/exec"`、`"syscall"`、`"time"`、`"memory-mcp/internal/summarize"`。

`init()` 加：

```go
	summarizeCmd.Flags().Bool("hook", false, "Claude Code SessionEnd hook mode: spawn a background worker and return immediately")
	summarizeCmd.Flags().String("worker", "", "internal: run summary from saved hook input file")
	summarizeCmd.Flags().MarkHidden("worker")
```

`rootCmd.AddCommand(...)` 加入 `summarizeCmd`。

新增：

```go
var summarizeCmd = &cobra.Command{
	Use:   "summarize",
	Short: "Auto-summarize a Claude Code session into a summary memory (SessionEnd hook)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if file, _ := cmd.Flags().GetString("worker"); file != "" {
			runSummarizeWorker(file)
			return nil
		}
		if isHook, _ := cmd.Flags().GetBool("hook"); isHook {
			runSummarizeHook()
			return nil
		}
		return fmt.Errorf("use --hook")
	},
}

// summarizeLogf 追加一行到 DB 同目錄的 summarize.log。
func summarizeLogf(format string, args ...any) {
	dir := filepath.Dir(defaultDBPath())
	if dbPath != "" {
		dir = filepath.Dir(dbPath)
	}
	f, err := os.OpenFile(filepath.Join(dir, "summarize.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(format, args...))
}

// runSummarizeHook 前景：把 hook JSON 存成暫存檔，fork 背景 worker 後立即返回。
func runSummarizeHook() {
	if os.Getenv(hook.SummarizingEnv) == "1" {
		return
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil || len(data) == 0 {
		summarizeLogf("- - hook: empty stdin")
		return
	}
	tmp, err := os.CreateTemp("", "memory-mcp-summarize-*.json")
	if err != nil {
		summarizeLogf("- - hook: temp file: %v", err)
		return
	}
	tmp.Write(data)
	tmp.Close()

	exe, err := os.Executable()
	if err != nil {
		summarizeLogf("- - hook: executable: %v", err)
		os.Remove(tmp.Name())
		return
	}
	workerArgs := []string{"summarize", "--worker", tmp.Name()}
	if dbPath != "" {
		workerArgs = append(workerArgs, "--db", dbPath)
	}
	if remoteFlag != "" {
		workerArgs = append(workerArgs, "--remote", remoteFlag)
	}
	c := exec.Command(exe, workerArgs...)
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := c.Start(); err != nil {
		summarizeLogf("- - hook: start worker: %v", err)
		os.Remove(tmp.Name())
		return
	}
	c.Process.Release()
}

// runSummarizeWorker 背景：讀暫存檔後刪除，執行摘要並寫 log。
func runSummarizeWorker(file string) {
	data, err := os.ReadFile(file)
	os.Remove(file)
	if err != nil {
		summarizeLogf("- - worker: read input: %v", err)
		return
	}
	in := hook.Read(strings.NewReader(string(data)))

	d, err := openStore()
	if err != nil {
		summarizeLogf("%s - worker: open store: %v", in.SessionID, err)
		return
	}
	defer d.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	summarize.Run(ctx, in, summarize.Deps{
		Store: d,
		Run:   summarize.ClaudeRunner,
		Logf:  summarizeLogf,
	})
}
```

`exec.Command` 的 Stdin／Stdout／Stderr 保持 nil（Go 會接到 `/dev/null`），NEVER 繼承 hook 的 pipe，否則 Claude Code 會等 worker 結束。

- [ ] **Step 2: Build and vet**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: 全部成功

- [ ] **Step 3: 手動驗證 hook 立即返回與防遞迴**

Run:
```bash
go build -o /tmp/memsum .
T=$(ls -t ~/.claude/projects/-Users-white-github-memory-mcp/*.jsonl | head -1)
time (echo "{\"session_id\":\"manual-test\",\"transcript_path\":\"$T\",\"cwd\":\"/tmp/memsum-e2e\"}" | /tmp/memsum --db /tmp/memsum-e2e.db summarize --hook)
```
Expected: `real` 小於 0.5 秒，exit 0。

Run: 等 90 秒後 `tail -3 /tmp/summarize.log; /tmp/memsum --db /tmp/memsum-e2e.db list --type summary`
Expected: log 有 `manual-test memsum-e2e stored #1`（或合理的 skip 原因），list 出現一筆 `tags:auto-summary project:memsum-e2e` 的繁中摘要。

Run: `echo '{}' | MEMORY_MCP_SUMMARIZING=1 /tmp/memsum --db /tmp/memsum-e2e.db summarize --hook; echo "exit=$?"`
Expected: `exit=0`，log 沒有新行。

Run: `rm -f /tmp/memsum-e2e.db* /tmp/summarize.log /tmp/memsum /tmp/memctx`

- [ ] **Step 4: Commit**

```bash
git add internal/cli/commands.go
git commit -m "feat: add summarize command for SessionEnd auto-summary"
```

---

### Task 9: hook 設定、文件與端到端驗證

**Files:**
- Modify: `README.md`
- Modify: `~/dotfiles/claude/settings.json`（`~/.claude/settings.json` 是它的 symlink）
- Modify: `~/dotfiles/agents/core.md`、`~/dotfiles/agents/AGENTS.md`（兩檔都有 memory 規則段落）

- [ ] **Step 1: 更新 README**

在 `README.md` 的 `## CLI` 程式碼區塊 `# List / manage` 段加：

```bash
memory-mcp search --compact "trigram"   # 一行一筆索引
memory-mcp get 12 34                    # 取全文
memory-mcp timeline 12 --before 3 --after 3
```

在 `### Claude Code (MCP Server)` 段後加：

````markdown
### Claude Code hooks

```json
"SessionStart": [{"matcher": "^(startup|resume|clear|compact)$", "hooks": [
  {"type": "command", "command": "/path/to/memory-mcp context --hook", "timeout": 10}
]}],
"SessionEnd": [{"hooks": [
  {"type": "command", "command": "/path/to/memory-mcp summarize --hook", "timeout": 10}
]}]
```

- `context --hook`：以工作目錄 basename（小寫）當 project，注入全域 feedback、專案最近 3 筆 summary、其他 10 筆記憶的索引（上限 6,000 字）。
- `summarize --hook`：背景呼叫 `claude -p --model haiku` 摘要本次 session，存成 `summary`（tag `auto-summary`）。session 中已手動存 summary、或 user 訊息少於 3 則時跳過。log 在 DB 同目錄的 `summarize.log`。
- 存入內容會刪除 `<private>…</private>` 並遮蔽常見金鑰格式。
````

- [ ] **Step 2: Build binary 並 commit repo**

Run: `make build && go test ./... && git add README.md && git commit -m "docs: document hooks, get, timeline and sanitize"`
Expected: 成功

- [ ] **Step 3: 更新 hook 設定**

`~/dotfiles/claude/settings.json`：

- 刪除整個 `"UserPromptSubmit"` 區塊（它只有 memory-mcp 這一個 hook）。
- `"SessionStart"` 陣列加一個元素：

```json
{"matcher":"^(startup|resume|clear|compact)$","hooks":[{"type":"command","command":"~/github/memory-mcp/bin/memory-mcp context --hook","timeout":10}]}
```

- 新增：

```json
"SessionEnd": [{"hooks":[{"type":"command","command":"~/github/memory-mcp/bin/memory-mcp summarize --hook","timeout":10}]}]
```

Run: `python3 -m json.tool ~/dotfiles/claude/settings.json > /dev/null && echo ok`
Expected: `ok`

- [ ] **Step 4: 更新 agent 規則**

`~/dotfiles/agents/core.md` 與 `~/dotfiles/agents/AGENTS.md` 的 memory 段落：

- 把「有 MCP tool（`mcp__memory__*`）可用時優先用 tool（`memory_store` / `memory_search` / `memory_list` / `memory_context`）」改成「有 MCP tool（`mcp__memory__*`）可用時優先用 tool（`memory_store` / `memory_search` / `memory_get` / `memory_timeline` / `memory_list` / `memory_context`）。`memory_search` 預設只回索引行，需要全文一律再呼叫 `memory_get(ids)`；追 summary 前後脈絡用 `memory_timeline(id)`」。
- 在「**收尾一定要存 `type: summary`**」那行後加子項：「Claude Code 有 SessionEnd 自動 summary（tag `auto-summary`），但 session 中手動存過 summary 就不會產生；關鍵決定、未完成事項一律手動存，NEVER 依賴自動 summary。」
- CLI fallback 清單加：`- 取全文：\`memory-mcp get <id>...\``。

- [ ] **Step 5: Commit dotfiles**

```bash
cd ~/dotfiles
git add claude/settings.json agents/core.md agents/AGENTS.md
git commit -m "feat(memory): SessionStart/SessionEnd hooks, memory_get/timeline rules"
```

- [ ] **Step 6: 端到端驗證（spec 手動四項）**

1. 在 `~/github/memory-mcp` 開新 Claude Code session，送一則 prompt。Expected：system context 出現 `## Memory context（project: memory-mcp）`，只有全域 feedback 與 memory-mcp 記憶，結尾有 token 估計；送第二則 prompt 時沒有再注入。
2. 同一 session 對話 3 則以上後 `/exit`，等 90 秒。Run：`~/github/memory-mcp/bin/memory-mcp list --type summary -n 1; tail -3 ~/.local/share/memory-mcp/summarize.log`。Expected：出現 `tags:auto-summary project:memory-mcp` 的一筆，log 最後一行是 `stored #N`。
3. 開新 session，手動請 agent 存一筆 memory-mcp 的 summary 後 `/exit`。Expected：log 最後一行是 `skip: manual summary #N exists`。
4. Run：`~/github/memory-mcp/bin/memory-mcp store -t til --project memory-mcp "password=abcdef123 <private>x</private>"`。Expected：stderr `已遮蔽 2 處敏感內容`；`memory-mcp get <id>` 內容為 `password=[REDACTED]`。驗證完用 `memory-mcp delete <id>` 刪掉這筆測試記憶。

任何一項不符，回到對應 task 修正，NEVER 在這一步直接改 code 而不補測試。
