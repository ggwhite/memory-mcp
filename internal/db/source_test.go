package db

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// 舊版 schema（沒有 source 欄位），用來驗證 migration。
const oldSchema = `
CREATE TABLE memories (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    type     TEXT NOT NULL CHECK(type IN ('feedback','til','summary','knowledge')),
    content  TEXT NOT NULL,
    tags     TEXT NOT NULL DEFAULT '',
    project  TEXT NOT NULL DEFAULT '',
    created  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S','now')),
    updated  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S','now'))
);
INSERT INTO memories (type, content, project) VALUES ('til', '舊資料一', 'p'), ('feedback', '舊資料二', 'global');
`

func TestOpenMigratesSourceColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(oldSchema); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	d, err := Open(path)
	if err != nil {
		t.Fatalf("Open old DB: %v", err)
	}
	all, err := d.ExportAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("rows = %d, want 2", len(all))
	}
	for _, m := range all {
		if m.Source != "user" {
			t.Errorf("#%d source = %q, want user", m.ID, m.Source)
		}
	}
	d.Close()

	// 第二次開啟不能因欄位已存在而失敗。
	d, err = Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	d.Close()
}

func TestSourceRoundTrip(t *testing.T) {
	d := testDB(t)
	userID, _ := d.Store(&Memory{Type: "til", Content: "使用者寫的"})
	llmID, _ := d.Store(&Memory{Type: "summary", Content: "LLM 寫的", Project: "p", Source: "llm:anthropic/x"})
	mergedID, _ := d.StoreMerged(&Memory{Type: "summary", Content: "合併", Project: "p", Source: "compact", Created: time.Now().UTC()}, nil)

	want := map[int64]string{userID: "user", llmID: "llm:anthropic/x", mergedID: "compact"}
	for id, src := range want {
		m, err := d.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if m.Source != src {
			t.Errorf("Get #%d source = %q, want %q", id, m.Source, src)
		}
	}
	list, _ := d.List(ListOptions{})
	for _, m := range list {
		if m.Source != want[m.ID] {
			t.Errorf("List #%d source = %q, want %q", m.ID, m.Source, want[m.ID])
		}
	}
	res, _ := d.Search(SearchOptions{Query: "LLM 寫的", Limit: 5})
	if len(res) == 0 || res[0].Source != "llm:anthropic/x" {
		t.Errorf("search results = %+v", res)
	}
}

func TestImportSourceDefaultsToUser(t *testing.T) {
	d := testDB(t)
	n, err := d.ImportBatch([]Memory{
		{Type: "til", Content: "舊匯出沒有 source"},
		{Type: "summary", Content: "新匯出", Project: "p", Source: "compact"},
	})
	if err != nil || n != 2 {
		t.Fatalf("import n=%d err=%v", n, err)
	}
	all, _ := d.ExportAll()
	got := map[string]string{}
	for _, m := range all {
		got[m.Content] = m.Source
	}
	if got["舊匯出沒有 source"] != "user" || got["新匯出"] != "compact" {
		t.Errorf("sources = %v", got)
	}
}

func TestLLMUsageCounter(t *testing.T) {
	d := testDB(t)
	c := d.LLMUsage()
	for want := 1; want <= 3; want++ {
		n, err := c.Incr("2026-11-07")
		if err != nil || n != want {
			t.Fatalf("Incr = %d, %v; want %d", n, err, want)
		}
	}
	if n, _ := c.Incr("2026-11-08"); n != 1 {
		t.Errorf("new day = %d, want 1", n)
	}
	if n, err := c.Calls("2026-11-07"); err != nil || n != 3 {
		t.Errorf("Calls = %d, %v; want 3", n, err)
	}
	if n, err := c.Calls("2000-01-01"); err != nil || n != 0 {
		t.Errorf("Calls unused day = %d, %v; want 0", n, err)
	}
}
