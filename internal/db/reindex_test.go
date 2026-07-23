package db

import (
	"context"
	"testing"
)

func TestReindexBackfillsMissingEmbeddings(t *testing.T) {
	d := testDB(t)
	// 先在沒有 embedder 的狀態下存兩筆，模擬「Ollama 曾經離線」。
	d.Store(&Memory{Type: "til", Content: "first"})
	d.Store(&Memory{Type: "til", Content: "second"})

	d.SetEmbedder(&fakeEmbedder{vec: []float32{0.1, 0.2}, model: "fake-v1"})

	stats, err := d.Reindex(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total != 2 || stats.Processed != 2 || stats.Failed != 0 {
		t.Fatalf("stats = %+v, want {Total:2 Processed:2 Failed:0}", stats)
	}

	var count int
	if err := d.db.QueryRow(`SELECT COUNT(*) FROM memory_embeddings`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
}

func TestReindexIsIdempotent(t *testing.T) {
	d := testDB(t)
	d.SetEmbedder(&fakeEmbedder{vec: []float32{0.1}, model: "fake-v1"})
	d.Store(&Memory{Type: "til", Content: "first"})

	if _, err := d.Reindex(context.Background()); err != nil {
		t.Fatal(err)
	}
	stats, err := d.Reindex(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total != 0 {
		t.Fatalf("second reindex Total = %d, want 0 (already up to date)", stats.Total)
	}
}

func TestReindexRefreshesOnModelChange(t *testing.T) {
	d := testDB(t)
	d.SetEmbedder(&fakeEmbedder{vec: []float32{0.1}, model: "fake-v1"})
	d.Store(&Memory{Type: "til", Content: "first"})
	if _, err := d.Reindex(context.Background()); err != nil {
		t.Fatal(err)
	}

	// 換模型後，同一筆記憶應該被視為需要重算。
	d.SetEmbedder(&fakeEmbedder{vec: []float32{0.2}, model: "fake-v2"})
	stats, err := d.Reindex(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total != 1 || stats.Processed != 1 {
		t.Fatalf("stats = %+v, want {Total:1 Processed:1}", stats)
	}
}

func TestReindexWithoutEmbedderNoOp(t *testing.T) {
	d := testDB(t)
	d.Store(&Memory{Type: "til", Content: "first"})

	stats, err := d.Reindex(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats != (ReindexStats{}) {
		t.Fatalf("stats = %+v, want zero value", stats)
	}
}
