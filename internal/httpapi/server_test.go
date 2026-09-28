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
