package db

import (
	"testing"
	"time"
)

func TestParseTimeBound(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		in      string
		until   bool
		want    string
		wantErr bool
	}{
		{"empty", "", false, "", false},
		{"days", "7d", false, "2026-10-31T12:00:00", false},
		{"hours", "24h", false, "2026-11-06T12:00:00", false},
		{"weeks", "2w", false, "2026-10-24T12:00:00", false},
		{"relative until", "1d", true, "2026-11-06T12:00:00", false},
		{"absolute since", "2026-11-01", false, "2026-11-01T00:00:00", false},
		{"absolute until inclusive of whole day", "2026-11-01", true, "2026-11-01T23:59:59", false},
		{"bad unit", "7x", false, "", true},
		{"bad number", "abcd", false, "", true},
		{"zero", "0d", false, "", true},
		{"negative", "-3d", false, "", true},
		{"bad date", "2026-13-45", true, "", true},
		{"garbage", "yesterday", false, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseTimeBound(tt.in, tt.until, now)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// seedTemporal 存三筆都含 "alpha" 的記憶，created 分別設為固定時間，回傳 id。
func seedTemporal(t *testing.T, d *DB) (old, mid, recent int64) {
	t.Helper()
	old, _ = d.Store(&Memory{Type: "til", Content: "alpha old note"})
	mid, _ = d.Store(&Memory{Type: "til", Content: "alpha mid note"})
	recent, _ = d.Store(&Memory{Type: "til", Content: "alpha recent note"})
	set := func(id int64, created string) {
		if _, err := d.db.Exec(`UPDATE memories SET created = ? WHERE id = ?`, created, id); err != nil {
			t.Fatal(err)
		}
	}
	set(old, "2020-01-10T08:00:00")
	set(mid, "2020-02-20T23:30:00")
	set(recent, time.Now().UTC().Add(-1*time.Hour).Format(timeLayout))
	return
}

func idsOf(rs []SearchResult) map[int64]bool {
	m := map[int64]bool{}
	for _, r := range rs {
		m[r.ID] = true
	}
	return m
}

func TestSearchTemporalFTS(t *testing.T) {
	d := testDB(t)
	old, mid, recent := seedTemporal(t, d)

	tests := []struct {
		name         string
		since, until string
		want         []int64
	}{
		{"no bounds", "", "", []int64{old, mid, recent}},
		{"relative since 24h", "24h", "", []int64{recent}},
		{"relative since 2w", "2w", "", []int64{recent}},
		{"relative since 7d", "7d", "", []int64{recent}},
		{"absolute since", "2020-02-01", "", []int64{mid, recent}},
		{"absolute until inclusive whole day", "", "2020-02-20", []int64{old, mid}},
		{"until before mid day", "", "2020-02-19", []int64{old}},
		{"since and until window", "2020-01-10", "2020-01-10", []int64{old}},
		{"relative until excludes recent", "", "1d", []int64{old, mid}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rs, err := d.Search(SearchOptions{Query: "alpha", Limit: 10, Since: tt.since, Until: tt.until})
			if err != nil {
				t.Fatal(err)
			}
			got := idsOf(rs)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for _, id := range tt.want {
				if !got[id] {
					t.Fatalf("got %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestSearchTemporalBadFormat(t *testing.T) {
	d := testDB(t)
	seedTemporal(t, d)
	if _, err := d.Search(SearchOptions{Query: "alpha", Since: "lastweek"}); err == nil {
		t.Error("bad since: want error")
	}
	if _, err := d.Search(SearchOptions{Query: "alpha", Until: "2026-99-99"}); err == nil {
		t.Error("bad until: want error")
	}
}

// 混合搜尋：向量端也要套時間過濾，純語意命中的舊記憶不能漏進來。
func TestSearchTemporalHybrid(t *testing.T) {
	d := testDB(t)
	d.SetEmbedder(&keyedEmbedder{vectors: map[string][]float32{
		"alpha":                  {1, 0},
		"alpha old note":         {1, 0},
		"alpha recent note":      {1, 0},
		"semantic only old note": {1, 0}, // 無 alpha 關鍵字，只能靠向量命中
		"semantic only new note": {1, 0},
	}})
	old, _, recent := seedTemporal(t, d)
	semOld, _ := d.Store(&Memory{Type: "til", Content: "semantic only old note"})
	semNew, _ := d.Store(&Memory{Type: "til", Content: "semantic only new note"})
	d.db.Exec(`UPDATE memories SET created = '2020-03-01T00:00:00' WHERE id = ?`, semOld)

	// 沒有過濾：兩筆純語意命中都在
	rs, err := d.Search(SearchOptions{Query: "alpha", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got := idsOf(rs); !got[semOld] || !got[semNew] {
		t.Fatalf("unfiltered hybrid should surface semantic-only hits, got %v", got)
	}

	rs, err = d.Search(SearchOptions{Query: "alpha", Limit: 10, Since: "7d"})
	if err != nil {
		t.Fatal(err)
	}
	got := idsOf(rs)
	if got[old] || got[semOld] {
		t.Errorf("old rows leaked through since=7d: %v", got)
	}
	if !got[recent] || !got[semNew] {
		t.Errorf("recent rows missing: %v", got)
	}

	rs, err = d.Search(SearchOptions{Query: "alpha", Limit: 10, Until: "2020-03-01"})
	if err != nil {
		t.Fatal(err)
	}
	got = idsOf(rs)
	if got[recent] || got[semNew] {
		t.Errorf("recent rows leaked through until=2020-03-01: %v", got)
	}
	if !got[semOld] || !got[old] {
		t.Errorf("old rows missing: %v", got)
	}
}
