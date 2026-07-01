# memory-mcp Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a CLI-first persistent memory tool with SQLite FTS5 search and optional stdio MCP server for AI coding agents.

**Architecture:** Single Go binary with three internal packages: `db` (SQLite connection, CRUD, FTS5 search), `cli` (cobra subcommands + output formatting), `mcp` (stdio JSON-RPC server). The `db` package owns all data access; `cli` and `mcp` are thin layers that delegate to `db`.

**Tech Stack:** Go, `modernc.org/sqlite` (pure Go SQLite), `github.com/spf13/cobra` (CLI), `github.com/mark3labs/mcp-go` (MCP protocol)

## Global Constraints

- Pure Go SQLite: `modernc.org/sqlite` — no CGO, no C compiler
- Module name: `memory-mcp`
- DB default path: `~/.local/share/memory-mcp/memory.db`
- DB path precedence: `--db` flag > `MEMORY_MCP_DB` env var > default
- Memory types: `feedback`, `til`, `summary`, `knowledge` — enforced by CHECK constraint
- CLI output: human-readable default, `--json` for JSON
- GoDoc on exported identifiers, comments 繁體中文
- `go vet` clean

## File Structure

| File | Responsibility |
|------|---------------|
| `go.mod` | Module definition + dependencies |
| `main.go` | CLI entry point |
| `internal/db/db.go` | SQLite connection, schema migration, CRUD, List, Stats, Export, Import |
| `internal/db/db_test.go` | DB layer tests |
| `internal/db/search.go` | FTS5 search query + ranking |
| `internal/db/search_test.go` | Search tests |
| `internal/cli/commands.go` | Cobra subcommands + output formatting |
| `internal/mcp/server.go` | stdio MCP server with 4 tools |
| `internal/mcp/server_test.go` | MCP server handler tests |

## Task Dependencies

```
Task 1 ──→ Task 2 ──→ Task 3
                  └──→ Task 4  (parallel with Task 3)
```

---

### Task 1: DB Layer — Schema, Connection, CRUD

**Files:**
- Create: `go.mod`
- Create: `internal/db/db.go`
- Create: `internal/db/db_test.go`

**Interfaces:**
- Consumes: nothing
- Produces:

```go
type Memory struct {
    ID      int64     `json:"id"`
    Type    string    `json:"type"`
    Content string    `json:"content"`
    Tags    string    `json:"tags"`
    Project string    `json:"project"`
    Created time.Time `json:"created"`
    Updated time.Time `json:"updated"`
}

type ListOptions struct {
    Type  string
    Limit int
    Since string // "Nd" format, e.g. "7d"; empty = no filter
}

type Stats struct {
    Total    int            `json:"total"`
    ByType   map[string]int `json:"by_type"`
    Earliest string         `json:"earliest"`
    Latest   string         `json:"latest"`
}

type DB struct { /* unexported sql.DB */ }

func Open(path string) (*DB, error)
func (d *DB) Close() error
func (d *DB) Store(mem *Memory) (int64, error)
func (d *DB) Get(id int64) (*Memory, error)
func (d *DB) Update(id int64, content string) error
func (d *DB) Delete(id int64) error
func (d *DB) List(opts ListOptions) ([]Memory, error)
func (d *DB) Stats() (*Stats, error)
func (d *DB) ExportAll() ([]Memory, error)
func (d *DB) ImportBatch(memories []Memory) (int, error)
```

- [ ] **Step 1: Initialize Go module and install SQLite dependency**

```bash
cd ~/github/memory-mcp
go mod init memory-mcp
go get modernc.org/sqlite
```

- [ ] **Step 2: Write failing tests for Open, Store, Get**

Create `internal/db/db_test.go`:

```go
package db

import (
	"path/filepath"
	"testing"
)

func testDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestOpenClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "test.db")
	d, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestStore(t *testing.T) {
	d := testDB(t)
	id, err := d.Store(&Memory{
		Type:    "til",
		Content: "test content",
		Tags:    "go,testing",
		Project: "memory-mcp",
	})
	if err != nil {
		t.Fatal(err)
	}
	if id <= 0 {
		t.Fatalf("id = %d, want > 0", id)
	}
}

func TestStoreInvalidType(t *testing.T) {
	d := testDB(t)
	_, err := d.Store(&Memory{Type: "invalid", Content: "test"})
	if err == nil {
		t.Fatal("expected error for invalid type")
	}
}

func TestGet(t *testing.T) {
	d := testDB(t)
	id, _ := d.Store(&Memory{
		Type:    "feedback",
		Content: "use real DB for tests",
		Tags:    "testing",
		Project: "kairos",
	})

	m, err := d.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if m.Content != "use real DB for tests" {
		t.Fatalf("content = %q, want %q", m.Content, "use real DB for tests")
	}
	if m.Type != "feedback" {
		t.Fatalf("type = %q, want %q", m.Type, "feedback")
	}
	if m.Tags != "testing" {
		t.Fatalf("tags = %q, want %q", m.Tags, "testing")
	}
	if m.Project != "kairos" {
		t.Fatalf("project = %q, want %q", m.Project, "kairos")
	}
	if m.Created.IsZero() {
		t.Fatal("created should not be zero")
	}
}

func TestGetNotFound(t *testing.T) {
	d := testDB(t)
	_, err := d.Get(999)
	if err == nil {
		t.Fatal("expected error for non-existent id")
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

```bash
go test ./internal/db/ -v -count=1
```

Expected: compilation error — package `db` not found.

- [ ] **Step 4: Implement Open, Close, Store, Get**

Create `internal/db/db.go`:

```go
package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Memory 一筆持久記憶。
type Memory struct {
	ID      int64     `json:"id"`
	Type    string    `json:"type"`
	Content string    `json:"content"`
	Tags    string    `json:"tags"`
	Project string    `json:"project"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
}

// ListOptions 列表查詢參數。
type ListOptions struct {
	Type  string
	Limit int
	Since string
}

// Stats 記憶統計。
type Stats struct {
	Total    int            `json:"total"`
	ByType   map[string]int `json:"by_type"`
	Earliest string         `json:"earliest"`
	Latest   string         `json:"latest"`
}

// DB SQLite 資料庫連線。
type DB struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS memories (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    type     TEXT NOT NULL CHECK(type IN ('feedback','til','summary','knowledge')),
    content  TEXT NOT NULL,
    tags     TEXT NOT NULL DEFAULT '',
    project  TEXT NOT NULL DEFAULT '',
    created  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S','now')),
    updated  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S','now'))
);

CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(
    content, tags, project,
    content=memories, content_rowid=id
);

CREATE TRIGGER IF NOT EXISTS memories_ai AFTER INSERT ON memories BEGIN
    INSERT INTO memories_fts(rowid, content, tags, project)
    VALUES (new.id, new.content, new.tags, new.project);
END;

CREATE TRIGGER IF NOT EXISTS memories_ad AFTER DELETE ON memories BEGIN
    INSERT INTO memories_fts(memories_fts, rowid, content, tags, project)
    VALUES ('delete', old.id, old.content, old.tags, old.project);
END;

CREATE TRIGGER IF NOT EXISTS memories_au AFTER UPDATE ON memories BEGIN
    INSERT INTO memories_fts(memories_fts, rowid, content, tags, project)
    VALUES ('delete', old.id, old.content, old.tags, old.project);
    INSERT INTO memories_fts(rowid, content, tags, project)
    VALUES (new.id, new.content, new.tags, new.project);
END;
`

const timeLayout = "2006-01-02T15:04:05"

// Open 開啟或建立 SQLite 資料庫，自動建立目錄與 schema。
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if _, err := sqlDB.Exec(schema); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &DB{db: sqlDB}, nil
}

// Close 關閉資料庫連線。
func (d *DB) Close() error {
	return d.db.Close()
}

// Store 儲存一筆記憶，回傳 auto-increment ID。
func (d *DB) Store(mem *Memory) (int64, error) {
	res, err := d.db.Exec(
		`INSERT INTO memories (type, content, tags, project) VALUES (?, ?, ?, ?)`,
		mem.Type, mem.Content, mem.Tags, mem.Project,
	)
	if err != nil {
		return 0, fmt.Errorf("store: %w", err)
	}
	return res.LastInsertId()
}

// Get 依 ID 取得一筆記憶。
func (d *DB) Get(id int64) (*Memory, error) {
	var m Memory
	var created, updated string
	err := d.db.QueryRow(
		`SELECT id, type, content, tags, project, created, updated FROM memories WHERE id = ?`, id,
	).Scan(&m.ID, &m.Type, &m.Content, &m.Tags, &m.Project, &created, &updated)
	if err != nil {
		return nil, fmt.Errorf("get: %w", err)
	}
	m.Created, _ = time.Parse(timeLayout, created)
	m.Updated, _ = time.Parse(timeLayout, updated)
	return &m, nil
}

func parseSince(s string) (int, error) {
	if !strings.HasSuffix(s, "d") {
		return 0, fmt.Errorf("invalid since format %q, expected Nd", s)
	}
	n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
	if err != nil {
		return 0, fmt.Errorf("invalid since format %q: %w", s, err)
	}
	return n, nil
}
```

- [ ] **Step 5: Run tests to verify Open/Store/Get pass**

```bash
go test ./internal/db/ -v -count=1 -run "TestOpen|TestStore|TestGet"
```

Expected: all PASS.

- [ ] **Step 6: Write failing tests for Update, Delete**

Append to `internal/db/db_test.go`:

```go
func TestUpdate(t *testing.T) {
	d := testDB(t)
	id, _ := d.Store(&Memory{Type: "til", Content: "original"})

	if err := d.Update(id, "updated content"); err != nil {
		t.Fatal(err)
	}
	m, _ := d.Get(id)
	if m.Content != "updated content" {
		t.Fatalf("content = %q, want %q", m.Content, "updated content")
	}
}

func TestUpdateNotFound(t *testing.T) {
	d := testDB(t)
	if err := d.Update(999, "content"); err == nil {
		t.Fatal("expected error for non-existent id")
	}
}

func TestDelete(t *testing.T) {
	d := testDB(t)
	id, _ := d.Store(&Memory{Type: "til", Content: "to delete"})

	if err := d.Delete(id); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Get(id); err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestDeleteNotFound(t *testing.T) {
	d := testDB(t)
	if err := d.Delete(999); err == nil {
		t.Fatal("expected error for non-existent id")
	}
}
```

- [ ] **Step 7: Run tests to verify Update/Delete fail**

```bash
go test ./internal/db/ -v -count=1 -run "TestUpdate|TestDelete"
```

Expected: compilation error — `Update` and `Delete` not defined.

- [ ] **Step 8: Implement Update, Delete**

Append to `internal/db/db.go`:

```go
// Update 更新指定記憶的內容。
func (d *DB) Update(id int64, content string) error {
	res, err := d.db.Exec(
		`UPDATE memories SET content = ?, updated = strftime('%Y-%m-%dT%H:%M:%S','now') WHERE id = ?`,
		content, id,
	)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("update: memory %d not found", id)
	}
	return nil
}

// Delete 刪除指定記憶。
func (d *DB) Delete(id int64) error {
	res, err := d.db.Exec(`DELETE FROM memories WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("delete: memory %d not found", id)
	}
	return nil
}
```

- [ ] **Step 9: Run tests to verify Update/Delete pass**

```bash
go test ./internal/db/ -v -count=1 -run "TestUpdate|TestDelete"
```

Expected: all PASS.

- [ ] **Step 10: Write failing tests for List, Stats**

Append to `internal/db/db_test.go`:

```go
func TestList(t *testing.T) {
	d := testDB(t)
	d.Store(&Memory{Type: "til", Content: "first"})
	d.Store(&Memory{Type: "feedback", Content: "second"})
	d.Store(&Memory{Type: "til", Content: "third"})

	all, err := d.List(ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("len = %d, want 3", len(all))
	}
	// ORDER BY created DESC — most recent first
	if all[0].Content != "third" {
		t.Fatalf("first result = %q, want %q", all[0].Content, "third")
	}
}

func TestListByType(t *testing.T) {
	d := testDB(t)
	d.Store(&Memory{Type: "til", Content: "a"})
	d.Store(&Memory{Type: "feedback", Content: "b"})
	d.Store(&Memory{Type: "til", Content: "c"})

	tils, err := d.List(ListOptions{Type: "til"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tils) != 2 {
		t.Fatalf("len = %d, want 2", len(tils))
	}
}

func TestListLimit(t *testing.T) {
	d := testDB(t)
	d.Store(&Memory{Type: "til", Content: "a"})
	d.Store(&Memory{Type: "til", Content: "b"})
	d.Store(&Memory{Type: "til", Content: "c"})

	limited, err := d.List(ListOptions{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 2 {
		t.Fatalf("len = %d, want 2", len(limited))
	}
}

func TestListSince(t *testing.T) {
	d := testDB(t)
	d.Store(&Memory{Type: "til", Content: "recent"})

	recent, err := d.List(ListOptions{Since: "1d"})
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 1 {
		t.Fatalf("len = %d, want 1", len(recent))
	}
}

func TestStats(t *testing.T) {
	d := testDB(t)
	d.Store(&Memory{Type: "til", Content: "a"})
	d.Store(&Memory{Type: "til", Content: "b"})
	d.Store(&Memory{Type: "feedback", Content: "c"})

	s, err := d.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if s.Total != 3 {
		t.Fatalf("total = %d, want 3", s.Total)
	}
	if s.ByType["til"] != 2 {
		t.Fatalf("til = %d, want 2", s.ByType["til"])
	}
	if s.ByType["feedback"] != 1 {
		t.Fatalf("feedback = %d, want 1", s.ByType["feedback"])
	}
}

func TestStatsEmpty(t *testing.T) {
	d := testDB(t)
	s, err := d.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if s.Total != 0 {
		t.Fatalf("total = %d, want 0", s.Total)
	}
}
```

- [ ] **Step 11: Run tests to verify List/Stats fail**

```bash
go test ./internal/db/ -v -count=1 -run "TestList|TestStats"
```

Expected: compilation error — `List` and `Stats` not defined.

- [ ] **Step 12: Implement List, Stats**

Append to `internal/db/db.go`:

```go
// List 列出記憶，支援 type 過濾、since 時間範圍、limit 筆數。
func (d *DB) List(opts ListOptions) ([]Memory, error) {
	query := `SELECT id, type, content, tags, project, created, updated FROM memories`
	var args []any
	var where []string

	if opts.Type != "" {
		where = append(where, "type = ?")
		args = append(args, opts.Type)
	}
	if opts.Since != "" {
		days, err := parseSince(opts.Since)
		if err != nil {
			return nil, err
		}
		where = append(where, "created >= datetime('now', ?)")
		args = append(args, fmt.Sprintf("-%d days", days))
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY created DESC"
	if opts.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, opts.Limit)
	}

	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}
	defer rows.Close()

	var memories []Memory
	for rows.Next() {
		var m Memory
		var created, updated string
		if err := rows.Scan(&m.ID, &m.Type, &m.Content, &m.Tags, &m.Project, &created, &updated); err != nil {
			return nil, fmt.Errorf("list scan: %w", err)
		}
		m.Created, _ = time.Parse(timeLayout, created)
		m.Updated, _ = time.Parse(timeLayout, updated)
		memories = append(memories, m)
	}
	return memories, rows.Err()
}

// Stats 回傳記憶統計資訊。
func (d *DB) Stats() (*Stats, error) {
	s := &Stats{ByType: make(map[string]int)}

	if err := d.db.QueryRow(`SELECT COUNT(*) FROM memories`).Scan(&s.Total); err != nil {
		return nil, fmt.Errorf("stats total: %w", err)
	}
	if s.Total == 0 {
		return s, nil
	}

	rows, err := d.db.Query(`SELECT type, COUNT(*) FROM memories GROUP BY type`)
	if err != nil {
		return nil, fmt.Errorf("stats by_type: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var typ string
		var n int
		if err := rows.Scan(&typ, &n); err != nil {
			return nil, fmt.Errorf("stats scan: %w", err)
		}
		s.ByType[typ] = n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	d.db.QueryRow(`SELECT MIN(created) FROM memories`).Scan(&s.Earliest)
	d.db.QueryRow(`SELECT MAX(created) FROM memories`).Scan(&s.Latest)
	return s, nil
}
```

- [ ] **Step 13: Run tests to verify List/Stats pass**

```bash
go test ./internal/db/ -v -count=1 -run "TestList|TestStats"
```

Expected: all PASS.

- [ ] **Step 14: Write failing tests for ExportAll, ImportBatch**

Append to `internal/db/db_test.go`:

```go
func TestExportImport(t *testing.T) {
	d := testDB(t)
	d.Store(&Memory{Type: "til", Content: "first", Tags: "go"})
	d.Store(&Memory{Type: "feedback", Content: "second", Project: "proj"})

	exported, err := d.ExportAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(exported) != 2 {
		t.Fatalf("exported = %d, want 2", len(exported))
	}

	d2 := testDB(t)
	n, err := d2.ImportBatch(exported)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("imported = %d, want 2", n)
	}

	all, _ := d2.List(ListOptions{})
	if len(all) != 2 {
		t.Fatalf("after import len = %d, want 2", len(all))
	}
}
```

- [ ] **Step 15: Run tests to verify ExportAll/ImportBatch fail**

```bash
go test ./internal/db/ -v -count=1 -run "TestExportImport"
```

Expected: compilation error.

- [ ] **Step 16: Implement ExportAll, ImportBatch**

Append to `internal/db/db.go`:

```go
// ExportAll 匯出所有記憶。
func (d *DB) ExportAll() ([]Memory, error) {
	return d.List(ListOptions{})
}

// ImportBatch 批次匯入記憶，回傳成功筆數。
func (d *DB) ImportBatch(memories []Memory) (int, error) {
	tx, err := d.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("import begin: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT INTO memories (type, content, tags, project) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return 0, fmt.Errorf("import prepare: %w", err)
	}
	defer stmt.Close()

	var count int
	for _, m := range memories {
		if _, err := stmt.Exec(m.Type, m.Content, m.Tags, m.Project); err != nil {
			return count, fmt.Errorf("import row: %w", err)
		}
		count++
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("import commit: %w", err)
	}
	return count, nil
}
```

- [ ] **Step 17: Run all DB tests**

```bash
go test ./internal/db/ -v -count=1
```

Expected: all PASS.

- [ ] **Step 18: Commit**

```bash
git add internal/db/db.go internal/db/db_test.go go.mod go.sum
git commit -m "feat: add db layer with SQLite schema, CRUD, list, stats, export/import"
```

---

### Task 2: FTS5 Search

**Files:**
- Create: `internal/db/search.go`
- Create: `internal/db/search_test.go`

**Interfaces:**
- Consumes: `DB` type, `Memory` type, `timeLayout` from Task 1
- Produces:

```go
type SearchOptions struct {
    Query string
    Type  string
    Limit int // default 5
}

type SearchResult struct {
    Memory
    Rank float64 `json:"rank"`
}

func (d *DB) Search(opts SearchOptions) ([]SearchResult, error)
```

- [ ] **Step 1: Write failing tests**

Create `internal/db/search_test.go`:

```go
package db

import "testing"

func TestSearch(t *testing.T) {
	d := testDB(t)
	d.Store(&Memory{Type: "til", Content: "SQLite FTS5 full text search", Tags: "sqlite,search"})
	d.Store(&Memory{Type: "feedback", Content: "always use real database for testing", Tags: "testing"})
	d.Store(&Memory{Type: "til", Content: "Go error handling best practices", Tags: "go"})

	results, err := d.Search(SearchOptions{Query: "database testing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected results for 'database testing'")
	}
}

func TestSearchTypeFilter(t *testing.T) {
	d := testDB(t)
	d.Store(&Memory{Type: "til", Content: "database connection pooling"})
	d.Store(&Memory{Type: "feedback", Content: "database testing approach"})

	results, err := d.Search(SearchOptions{Query: "database", Type: "til"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("len = %d, want 1", len(results))
	}
	if results[0].Type != "til" {
		t.Fatalf("type = %q, want til", results[0].Type)
	}
}

func TestSearchLimit(t *testing.T) {
	d := testDB(t)
	d.Store(&Memory{Type: "til", Content: "database one"})
	d.Store(&Memory{Type: "til", Content: "database two"})
	d.Store(&Memory{Type: "til", Content: "database three"})

	results, err := d.Search(SearchOptions{Query: "database", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("len = %d, want 2", len(results))
	}
}

func TestSearchNoResults(t *testing.T) {
	d := testDB(t)
	d.Store(&Memory{Type: "til", Content: "something unrelated"})

	results, err := d.Search(SearchOptions{Query: "nonexistent"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("len = %d, want 0", len(results))
	}
}

func TestSearchByTags(t *testing.T) {
	d := testDB(t)
	d.Store(&Memory{Type: "til", Content: "some content", Tags: "docker,kubernetes"})
	d.Store(&Memory{Type: "til", Content: "other content", Tags: "go,testing"})

	results, err := d.Search(SearchOptions{Query: "kubernetes"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("len = %d, want 1", len(results))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/db/ -v -count=1 -run "TestSearch"
```

Expected: compilation error — `SearchOptions`, `Search` not defined.

- [ ] **Step 3: Implement Search**

Create `internal/db/search.go`:

```go
package db

import (
	"fmt"
	"time"
)

// SearchOptions 搜索查詢參數。
type SearchOptions struct {
	Query string
	Type  string
	Limit int
}

// SearchResult 搜索結果，包含 FTS5 排名分數。
type SearchResult struct {
	Memory
	Rank float64 `json:"rank"`
}

// Search 使用 FTS5 全文搜索記憶，依相關度排序。
func (d *DB) Search(opts SearchOptions) ([]SearchResult, error) {
	query := `
		SELECT m.id, m.type, m.content, m.tags, m.project, m.created, m.updated, f.rank
		FROM memories_fts f
		JOIN memories m ON m.id = f.rowid
		WHERE memories_fts MATCH ?`
	args := []any{opts.Query}

	if opts.Type != "" {
		query += ` AND m.type = ?`
		args = append(args, opts.Type)
	}

	query += ` ORDER BY f.rank`

	limit := opts.Limit
	if limit <= 0 {
		limit = 5
	}
	query += ` LIMIT ?`
	args = append(args, limit)

	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		var created, updated string
		if err := rows.Scan(&r.ID, &r.Type, &r.Content, &r.Tags, &r.Project, &created, &updated, &r.Rank); err != nil {
			return nil, fmt.Errorf("search scan: %w", err)
		}
		r.Created, _ = time.Parse(timeLayout, created)
		r.Updated, _ = time.Parse(timeLayout, updated)
		results = append(results, r)
	}
	return results, rows.Err()
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/db/ -v -count=1 -run "TestSearch"
```

Expected: all PASS.

- [ ] **Step 5: Run all DB + search tests together**

```bash
go test ./internal/db/ -v -count=1
```

Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/db/search.go internal/db/search_test.go
git commit -m "feat: add FTS5 full-text search with type filter and ranking"
```

---

### Task 3: CLI Commands

**Files:**
- Create: `internal/cli/commands.go`
- Create: `main.go`

**Interfaces:**
- Consumes: all `DB` methods from Tasks 1–2 (`Store`, `Get`, `Update`, `Delete`, `List`, `Stats`, `ExportAll`, `ImportBatch`, `Search`)
- Produces: working CLI binary with subcommands: `store`, `search`, `list`, `delete`, `update`, `stats`, `export`, `import` (no `serve` — that's Task 4)

- [ ] **Step 1: Install cobra dependency**

```bash
go get github.com/spf13/cobra
```

- [ ] **Step 2: Create commands.go**

Create `internal/cli/commands.go`:

```go
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"memory-mcp/internal/db"

	"github.com/spf13/cobra"
)

var (
	dbPath   string
	jsonFlag bool
)

func defaultDBPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "memory-mcp", "memory.db")
}

func openDB() (*db.DB, error) {
	path := dbPath
	if path == "" {
		path = os.Getenv("MEMORY_MCP_DB")
	}
	if path == "" {
		path = defaultDBPath()
	}
	return db.Open(path)
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func formatMemory(m db.Memory) string {
	line := fmt.Sprintf("#%d [%s] %s", m.ID, m.Type, m.Created.Format("2006-01-02"))
	if m.Tags != "" {
		line += fmt.Sprintf("  tags:%s", m.Tags)
	}
	if m.Project != "" {
		line += fmt.Sprintf("  project:%s", m.Project)
	}
	line += "\n  " + m.Content
	return line
}

var rootCmd = &cobra.Command{
	Use:   "memory-mcp",
	Short: "Cross-session persistent memory for AI coding agents",
}

// Execute 執行 CLI root command。
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().StringVar(&dbPath, "db", "", "database path (default ~/.local/share/memory-mcp/memory.db)")
	rootCmd.PersistentFlags().BoolVar(&jsonFlag, "json", false, "output JSON format")

	storeCmd.Flags().StringP("type", "t", "", "memory type (feedback|til|summary|knowledge)")
	storeCmd.MarkFlagRequired("type")
	storeCmd.Flags().String("tags", "", "comma-separated tags")
	storeCmd.Flags().String("project", "", "project name")

	searchCmd.Flags().String("type", "", "filter by memory type")
	searchCmd.Flags().IntP("limit", "n", 5, "max results")

	listCmd.Flags().String("type", "", "filter by memory type")
	listCmd.Flags().IntP("limit", "n", 10, "max results")
	listCmd.Flags().String("since", "", "show memories since (Nd format, e.g. 7d)")

	exportCmd.Flags().String("format", "json", "export format")

	rootCmd.AddCommand(storeCmd, searchCmd, listCmd, deleteCmd, updateCmd, statsCmd, exportCmd, importCmd)
}

var storeCmd = &cobra.Command{
	Use:   "store <content>",
	Short: "Store a new memory",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		typ, _ := cmd.Flags().GetString("type")
		tags, _ := cmd.Flags().GetString("tags")
		project, _ := cmd.Flags().GetString("project")

		d, err := openDB()
		if err != nil {
			return err
		}
		defer d.Close()

		id, err := d.Store(&db.Memory{
			Type: typ, Content: args[0], Tags: tags, Project: project,
		})
		if err != nil {
			return err
		}
		if jsonFlag {
			return printJSON(map[string]any{"id": id})
		}
		fmt.Printf("Stored memory #%d\n", id)
		return nil
	},
}

var searchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search memories with FTS5",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		typ, _ := cmd.Flags().GetString("type")
		limit, _ := cmd.Flags().GetInt("limit")

		d, err := openDB()
		if err != nil {
			return err
		}
		defer d.Close()

		results, err := d.Search(db.SearchOptions{
			Query: args[0], Type: typ, Limit: limit,
		})
		if err != nil {
			return err
		}
		if jsonFlag {
			return printJSON(results)
		}
		for i, r := range results {
			if i > 0 {
				fmt.Println()
			}
			fmt.Println(formatMemory(r.Memory))
		}
		return nil
	},
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List memories",
	RunE: func(cmd *cobra.Command, args []string) error {
		typ, _ := cmd.Flags().GetString("type")
		limit, _ := cmd.Flags().GetInt("limit")
		since, _ := cmd.Flags().GetString("since")

		d, err := openDB()
		if err != nil {
			return err
		}
		defer d.Close()

		memories, err := d.List(db.ListOptions{
			Type: typ, Limit: limit, Since: since,
		})
		if err != nil {
			return err
		}
		if jsonFlag {
			return printJSON(memories)
		}
		for i, m := range memories {
			if i > 0 {
				fmt.Println()
			}
			fmt.Println(formatMemory(m))
		}
		return nil
	},
}

var deleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a memory by ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid id: %w", err)
		}

		d, err := openDB()
		if err != nil {
			return err
		}
		defer d.Close()

		if err := d.Delete(id); err != nil {
			return err
		}
		if jsonFlag {
			return printJSON(map[string]any{"deleted": true})
		}
		fmt.Printf("Deleted memory #%d\n", id)
		return nil
	},
}

var updateCmd = &cobra.Command{
	Use:   "update <id> <content>",
	Short: "Update a memory's content",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid id: %w", err)
		}

		d, err := openDB()
		if err != nil {
			return err
		}
		defer d.Close()

		if err := d.Update(id, args[1]); err != nil {
			return err
		}
		if jsonFlag {
			return printJSON(map[string]any{"updated": true})
		}
		fmt.Printf("Updated memory #%d\n", id)
		return nil
	},
}

var statsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show memory statistics",
	RunE: func(cmd *cobra.Command, args []string) error {
		d, err := openDB()
		if err != nil {
			return err
		}
		defer d.Close()

		s, err := d.Stats()
		if err != nil {
			return err
		}
		if jsonFlag {
			return printJSON(s)
		}
		fmt.Printf("Total: %d\n", s.Total)
		for typ, n := range s.ByType {
			fmt.Printf("  %s: %d\n", typ, n)
		}
		if s.Earliest != "" {
			fmt.Printf("Earliest: %s\n", strings.Split(s.Earliest, "T")[0])
			fmt.Printf("Latest: %s\n", strings.Split(s.Latest, "T")[0])
		}
		return nil
	},
}

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export all memories as JSON",
	RunE: func(cmd *cobra.Command, args []string) error {
		d, err := openDB()
		if err != nil {
			return err
		}
		defer d.Close()

		memories, err := d.ExportAll()
		if err != nil {
			return err
		}
		return printJSON(memories)
	},
}

var importCmd = &cobra.Command{
	Use:   "import <file>",
	Short: "Import memories from JSON file",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		data, err := os.ReadFile(args[0])
		if err != nil {
			return fmt.Errorf("read file: %w", err)
		}

		var memories []db.Memory
		if err := json.Unmarshal(data, &memories); err != nil {
			return fmt.Errorf("parse JSON: %w", err)
		}

		d, err := openDB()
		if err != nil {
			return err
		}
		defer d.Close()

		n, err := d.ImportBatch(memories)
		if err != nil {
			return err
		}
		if jsonFlag {
			return printJSON(map[string]any{"imported": n})
		}
		fmt.Printf("Imported %d memories\n", n)
		return nil
	},
}
```

- [ ] **Step 3: Create main.go**

Create `main.go`:

```go
package main

import (
	"fmt"
	"os"

	"memory-mcp/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

- [ ] **Step 4: Build and verify**

```bash
go build -o bin/memory-mcp .
```

Expected: clean build, binary at `bin/memory-mcp`.

- [ ] **Step 5: Smoke test CLI**

```bash
export MEMORY_MCP_DB=/tmp/test-memory.db

bin/memory-mcp store -t til "SQLite FTS5 支援中文搜索"
bin/memory-mcp store -t feedback --tags testing,go --project kairos "用 real DB 跑 integration test"
bin/memory-mcp list
bin/memory-mcp search "SQLite"
bin/memory-mcp search "database" --json
bin/memory-mcp stats
bin/memory-mcp update 1 "SQLite FTS5 支援中文搜索（已驗證）"
bin/memory-mcp list --json
bin/memory-mcp delete 2
bin/memory-mcp export > /tmp/export.json
bin/memory-mcp import /tmp/export.json

rm -f /tmp/test-memory.db /tmp/export.json
```

Verify: each command produces expected output. `list` shows human-readable format. `--json` shows JSON.

- [ ] **Step 6: Run go vet**

```bash
go vet ./...
```

Expected: no issues.

- [ ] **Step 7: Commit**

```bash
git add main.go internal/cli/commands.go
git commit -m "feat: add CLI with store/search/list/delete/update/stats/export/import commands"
```

---

### Task 4: MCP Server + serve Command

**Files:**
- Create: `internal/mcp/server.go`
- Create: `internal/mcp/server_test.go`
- Modify: `internal/cli/commands.go` — add `serve` subcommand

**Interfaces:**
- Consumes: `DB` type + `Store`, `Search`, `List`, `Delete` methods from Tasks 1–2
- Produces:
  - MCP tools: `memory_store`, `memory_search`, `memory_list`, `memory_delete`
  - `serve` CLI subcommand

```go
type Server struct { /* wraps *db.DB */ }

func NewServer(d *db.DB) *Server
func (s *Server) MCPServer() *mcpserver.MCPServer
```

- [ ] **Step 1: Install mcp-go dependency**

```bash
go get github.com/mark3labs/mcp-go
```

- [ ] **Step 2: Write failing tests for MCP handlers**

Create `internal/mcp/server_test.go`:

```go
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
```

- [ ] **Step 3: Run tests to verify they fail**

```bash
go test ./internal/mcp/ -v -count=1
```

Expected: compilation error — package `mcp` not found.

- [ ] **Step 4: Implement server.go**

Create `internal/mcp/server.go`:

```go
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

func argStr(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}

func argFloat(args map[string]any, key string) float64 {
	v, _ := args[key].(float64)
	return v
}

func textResult(v any) *gomcp.CallToolResult {
	data, _ := json.Marshal(v)
	return &gomcp.CallToolResult{
		Content: []gomcp.Content{gomcp.TextContent{Type: "text", Text: string(data)}},
	}
}

func errResult(err error) *gomcp.CallToolResult {
	return &gomcp.CallToolResult{
		IsError: true,
		Content: []gomcp.Content{gomcp.TextContent{Type: "text", Text: err.Error()}},
	}
}

func (s *Server) handleStore(_ context.Context, req gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	args := req.Params.Arguments
	id, err := s.db.Store(&db.Memory{
		Type:    argStr(args, "type"),
		Content: argStr(args, "content"),
		Tags:    argStr(args, "tags"),
		Project: argStr(args, "project"),
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

func (s *Server) handleSearch(_ context.Context, req gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	args := req.Params.Arguments
	limit := int(argFloat(args, "limit"))
	if limit <= 0 {
		limit = 5
	}
	results, err := s.db.Search(db.SearchOptions{
		Query: argStr(args, "query"),
		Type:  argStr(args, "type"),
		Limit: limit,
	})
	if err != nil {
		return errResult(err), nil
	}
	return textResult(results), nil
}

func (s *Server) handleList(_ context.Context, req gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	args := req.Params.Arguments
	limit := int(argFloat(args, "limit"))
	if limit <= 0 {
		limit = 10
	}
	memories, err := s.db.List(db.ListOptions{
		Type:  argStr(args, "type"),
		Limit: limit,
		Since: argStr(args, "since"),
	})
	if err != nil {
		return errResult(err), nil
	}
	return textResult(memories), nil
}

func (s *Server) handleDelete(_ context.Context, req gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	args := req.Params.Arguments
	id := int64(argFloat(args, "id"))
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
```

**注意：** `mcp-go` 的 API 可能隨版本變動。如果 import path 或函式簽名不符，請以 `go doc` 或 `mcp-go` README 為準調整。核心結構不變：NewServer → AddTool → handler。

- [ ] **Step 5: Run tests to verify they pass**

```bash
go test ./internal/mcp/ -v -count=1
```

Expected: all PASS. 如果 `mcp-go` API 有變，先修正 import/type 再跑。

- [ ] **Step 6: Add serve command to CLI**

Modify `internal/cli/commands.go` — add import and command:

Add to imports:
```go
mcpserver "github.com/mark3labs/mcp-go/server"
memcp "memory-mcp/internal/mcp"
```

Add to `init()`:
```go
rootCmd.AddCommand(serveCmd)
```

Add the command:
```go
var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start stdio MCP server",
	RunE: func(cmd *cobra.Command, args []string) error {
		d, err := openDB()
		if err != nil {
			return err
		}
		defer d.Close()

		srv := memcp.NewServer(d).MCPServer()
		return mcpserver.ServeStdio(srv)
	},
}
```

- [ ] **Step 7: Build and verify**

```bash
go build -o bin/memory-mcp .
go vet ./...
```

Expected: clean build, no vet issues.

- [ ] **Step 8: Run all tests**

```bash
go test ./... -v -count=1
```

Expected: all PASS across all packages.

- [ ] **Step 9: Commit**

```bash
git add internal/mcp/server.go internal/mcp/server_test.go internal/cli/commands.go
git commit -m "feat: add stdio MCP server with memory_store/search/list/delete tools"
```
