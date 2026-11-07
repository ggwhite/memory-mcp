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

func TestClientStoreSupersedes(t *testing.T) {
	c, d := testClient(t)
	a, _ := d.Store(&db.Memory{Type: "til", Content: "舊 a"})
	b, _ := d.Store(&db.Memory{Type: "til", Content: "舊 b"})

	mem := &db.Memory{Type: "til", Content: "新", Supersedes: []int64{a, b, b}}
	id, err := c.Store(mem)
	if err != nil {
		t.Fatal(err)
	}
	if len(mem.Supersedes) != 2 {
		t.Errorf("Supersedes = %v, want deduped deleted ids", mem.Supersedes)
	}
	got, _ := d.GetMany([]int64{a, b, id})
	if len(got) != 1 || got[0].ID != id {
		t.Fatalf("remaining = %+v", got)
	}

	if _, err := c.Store(&db.Memory{Type: "til", Content: "再新", Supersedes: []int64{id, 999}}); err == nil || !strings.Contains(err.Error(), "999") {
		t.Fatalf("err = %v, want missing #999", err)
	}
	if st, _ := d.Stats(); st.Total != 1 {
		t.Errorf("total = %d, want 1", st.Total)
	}
}

// 被取代的舊記憶不存在是呼叫端的問題：回 404，不是 500，且錯誤訊息要原樣傳到 client。
func TestStoreSupersededNotFoundIs404(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ts := httptest.NewServer(NewServer(d).Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/v1/store", "application/json",
		strings.NewReader(`{"type":"til","content":"x","supersedes":[999]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if body["error"] != "superseded memory not found: #999" {
		t.Errorf("error = %q", body["error"])
	}

	c := NewClient(ts.URL)
	_, err = c.Store(&db.Memory{Type: "til", Content: "x", Supersedes: []int64{999}})
	if err == nil || err.Error() != "remote: superseded memory not found: #999" {
		t.Errorf("client err = %v", err)
	}
}

// 沒有 supersedes 時回應不帶 "deleted" 欄位。
func TestStoreOmitsDeletedWhenNoSupersedes(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ts := httptest.NewServer(NewServer(d).Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/v1/store", "application/json", strings.NewReader(`{"type":"til","content":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	if _, ok := body["deleted"]; ok {
		t.Errorf("body = %v, want no deleted key", body)
	}
}

func TestClientCarriesSource(t *testing.T) {
	c, _ := testClient(t)
	id, err := c.Store(&db.Memory{Type: "summary", Content: "LLM 摘要", Project: "p", Source: "llm:anthropic/x"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Get(id)
	if err != nil || got.Source != "llm:anthropic/x" {
		t.Fatalf("Get source = %q, err %v", got.Source, err)
	}
	if _, err := c.ImportBatch([]db.Memory{{Type: "til", Content: "舊匯出"}, {Type: "summary", Content: "合併", Project: "p", Source: "compact"}}); err != nil {
		t.Fatal(err)
	}
	all, err := c.ExportAll()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"LLM 摘要": "llm:anthropic/x", "舊匯出": "user", "合併": "compact"}
	for _, m := range all {
		if m.Source != want[m.Content] {
			t.Errorf("%q source = %q, want %q", m.Content, m.Source, want[m.Content])
		}
	}
}

// REST 往返：client 把 Since / Until 帶進 query string，server 再傳給 store。
func TestClientSearchSinceUntil(t *testing.T) {
	c, d := testClient(t)
	d.Store(&db.Memory{Type: "til", Content: "gamma entry"})

	got, err := c.Search(db.SearchOptions{Query: "gamma", Since: "7d"})
	if err != nil || len(got) != 1 {
		t.Fatalf("since=7d: got %v, err %v; want 1 hit", got, err)
	}
	got, err = c.Search(db.SearchOptions{Query: "gamma", Until: "2020-01-10"})
	if err != nil || len(got) != 0 {
		t.Fatalf("until=2020-01-10: got %v, err %v; want 0 hits", got, err)
	}
	got, err = c.Search(db.SearchOptions{Query: "gamma", Since: "2999-01-01"})
	if err != nil || len(got) != 0 {
		t.Fatalf("since=2999-01-01: got %v, err %v; want 0 hits", got, err)
	}
	if _, err := c.Search(db.SearchOptions{Query: "gamma", Since: "nope"}); err == nil {
		t.Error("bad since should return an error")
	}
}
