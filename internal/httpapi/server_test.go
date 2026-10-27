package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ggwhite/memory-mcp/internal/db"
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

func TestServerStoreFeedbackRequiresProject(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(NewServer(d).Handler())
	t.Cleanup(func() {
		ts.Close()
		d.Close()
	})

	post := func(body string) (int, map[string]any) {
		t.Helper()
		resp, err := http.Post(ts.URL+"/v1/store", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	code, out := post(`{"type":"feedback","content":"no project rule"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%v)", code, out)
	}
	if msg, _ := out["error"].(string); !strings.Contains(msg, "global") {
		t.Errorf("error = %v, want mention of global", out)
	}
	if ms, _ := d.List(db.ListOptions{}); len(ms) != 0 {
		t.Errorf("rejected feedback was stored: %+v", ms)
	}

	code, out = post(`{"type":"feedback","content":"global rule","project":"global"}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%v)", code, out)
	}
}

func TestServerImportRejectsFeedbackWithoutProject(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(NewServer(d).Handler())
	t.Cleanup(func() {
		ts.Close()
		d.Close()
	})

	body := `[{"type":"til","content":"ok til","project":"p"},{"type":"feedback","content":"no project rule"}]`
	resp, err := http.Post(ts.URL+"/v1/import", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%v)", resp.StatusCode, out)
	}
	if msg, _ := out["error"].(string); !strings.Contains(msg, "第 2 筆") || !strings.Contains(msg, "global") {
		t.Errorf("error = %v, want index and global hint", out)
	}
	if ms, _ := d.List(db.ListOptions{}); len(ms) != 0 {
		t.Errorf("batch should be rejected entirely, stored: %+v", ms)
	}

	c := NewClient(ts.URL)
	n, err := c.ImportBatch([]db.Memory{{Type: "feedback", Content: "global rule", Project: "global"}, {Type: "summary", Content: "s"}})
	if err != nil || n != 2 {
		t.Fatalf("valid batch: n=%d err=%v", n, err)
	}
}
