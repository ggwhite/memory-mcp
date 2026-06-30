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
