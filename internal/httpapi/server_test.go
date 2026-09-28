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
