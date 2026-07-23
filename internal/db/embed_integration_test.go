package db

import (
	"context"
	"fmt"
	"testing"
)

// fakeEmbedder 是測試用的 embed.Embedder 假實作，供本檔與後續 hybrid
// search／reindex 測試共用。
type fakeEmbedder struct {
	vec   []float32
	model string
	err   error
}

func (f *fakeEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.vec, nil
}

func (f *fakeEmbedder) Model() string {
	return f.model
}

func TestStoreWithEmbedderPersistsVector(t *testing.T) {
	d := testDB(t)
	d.SetEmbedder(&fakeEmbedder{vec: []float32{0.1, 0.2}, model: "fake"})

	id, err := d.Store(&Memory{Type: "til", Content: "hello"})
	if err != nil {
		t.Fatal(err)
	}

	var count int
	if err := d.db.QueryRow(`SELECT COUNT(*) FROM memory_embeddings WHERE memory_id = ?`, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
}

func TestStoreWithoutEmbedderSkipsVector(t *testing.T) {
	d := testDB(t)

	id, err := d.Store(&Memory{Type: "til", Content: "hello"})
	if err != nil {
		t.Fatal(err)
	}

	var count int
	if err := d.db.QueryRow(`SELECT COUNT(*) FROM memory_embeddings WHERE memory_id = ?`, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("count = %d, want 0 (no embedder configured)", count)
	}
}

func TestStoreWithFailingEmbedderStillSucceeds(t *testing.T) {
	d := testDB(t)
	d.SetEmbedder(&fakeEmbedder{err: fmt.Errorf("ollama down")})

	id, err := d.Store(&Memory{Type: "til", Content: "hello"})
	if err != nil {
		t.Fatalf("Store should succeed even if embedder fails: %v", err)
	}
	if id <= 0 {
		t.Fatal("expected valid id")
	}
}

func TestUpdateRefreshesEmbedding(t *testing.T) {
	d := testDB(t)
	d.SetEmbedder(&fakeEmbedder{vec: []float32{0.1}, model: "fake"})

	id, err := d.Store(&Memory{Type: "til", Content: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Update(id, "v2"); err != nil {
		t.Fatal(err)
	}

	var blob []byte
	if err := d.db.QueryRow(`SELECT vector FROM memory_embeddings WHERE memory_id = ?`, id).Scan(&blob); err != nil {
		t.Fatal(err)
	}
	if len(blob) != 4 { // 1 個 float32 = 4 bytes
		t.Fatalf("len(blob) = %d, want 4", len(blob))
	}
}

func TestDeleteCascadesEmbedding(t *testing.T) {
	d := testDB(t)
	d.SetEmbedder(&fakeEmbedder{vec: []float32{0.1}, model: "fake"})

	id, err := d.Store(&Memory{Type: "til", Content: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Delete(id); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := d.db.QueryRow(`SELECT COUNT(*) FROM memory_embeddings WHERE memory_id = ?`, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("count = %d, want 0 (cascade delete)", count)
	}
}
