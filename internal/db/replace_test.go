package db

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func countRows(t *testing.T, d *DB, table string) int {
	t.Helper()
	var n int
	if err := d.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestStoreSupersedes(t *testing.T) {
	d := testDB(t)
	d.SetEmbedder(&fakeEmbedder{vec: []float32{0.1, 0.2}, model: "fake"})
	a, _ := d.Store(&Memory{Type: "feedback", Project: "p", Content: "舊規則 A"})
	b, _ := d.Store(&Memory{Type: "feedback", Project: "p", Content: "舊規則 B"})
	keep, _ := d.Store(&Memory{Type: "til", Content: "無關"})

	mem := &Memory{Type: "feedback", Project: "p", Content: "新規則", Supersedes: []int64{a, b, a}}
	id, err := d.Store(mem)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(mem.Supersedes, []int64{a, b}) {
		t.Errorf("Supersedes = %v, want deduped [%d %d]", mem.Supersedes, a, b)
	}
	got, _ := d.GetMany([]int64{a, b, keep, id})
	if len(got) != 2 || got[0].ID != keep || got[1].ID != id {
		t.Fatalf("remaining = %+v", got)
	}
	if n := countRows(t, d, "memory_embeddings"); n != 2 {
		t.Errorf("embeddings = %d, want 2 (superseded rows' vectors removed)", n)
	}
}

func TestStoreSupersedesMissingID(t *testing.T) {
	d := testDB(t)
	a, _ := d.Store(&Memory{Type: "til", Content: "舊"})
	_, err := d.Store(&Memory{Type: "til", Content: "新", Supersedes: []int64{a, 999}})
	if err == nil || !strings.Contains(err.Error(), "999") {
		t.Fatalf("err = %v, want missing #999", err)
	}
	if n := countRows(t, d, "memories"); n != 1 {
		t.Errorf("memories = %d, want 1 (nothing stored, nothing deleted)", n)
	}
	if _, err := d.Get(a); err != nil {
		t.Errorf("#%d should remain: %v", a, err)
	}
}

func TestStoreMergedKeepsCreated(t *testing.T) {
	d := testDB(t)
	a, _ := d.Store(&Memory{Type: "summary", Project: "p", Content: "一"})
	b, _ := d.Store(&Memory{Type: "summary", Project: "p", Content: "二"})
	created := time.Date(2026, 3, 15, 8, 30, 0, 0, time.UTC)
	id, err := d.StoreMerged(&Memory{Type: "summary", Project: "p", Content: "合併", Tags: "compacted", Created: created}, []int64{a, b})
	if err != nil {
		t.Fatal(err)
	}
	m, err := d.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Created.Equal(created) || m.Content != "合併" {
		t.Errorf("got %+v, want created %v", m, created)
	}
	if n := countRows(t, d, "memories"); n != 1 {
		t.Errorf("memories = %d, want 1", n)
	}
}

func TestSummariesOlderThan(t *testing.T) {
	d := testDB(t)
	old1, _ := d.Store(&Memory{Type: "summary", Project: "p", Content: "舊1"})
	old2, _ := d.Store(&Memory{Type: "summary", Project: "q", Content: "舊2"})
	d.Store(&Memory{Type: "summary", Project: "p", Content: "新"})
	oldTil, _ := d.Store(&Memory{Type: "til", Project: "p", Content: "舊 til"})
	for _, id := range []int64{old1, old2, oldTil} {
		d.db.Exec(`UPDATE memories SET created = strftime('%Y-%m-%dT%H:%M:%S','now','-100 days') WHERE id = ?`, id)
	}

	got, err := d.SummariesOlderThan("60d", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != old1 || got[1].ID != old2 {
		t.Errorf("all projects = %+v", got)
	}
	got, _ = d.SummariesOlderThan("60d", "q")
	if len(got) != 1 || got[0].ID != old2 {
		t.Errorf("project q = %+v", got)
	}
	if _, err := d.SummariesOlderThan("60", ""); err == nil {
		t.Error("want invalid format error")
	}
}
