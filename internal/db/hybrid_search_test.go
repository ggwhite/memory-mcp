package db

import (
	"context"
	"fmt"
	"testing"
)

var errFakeEmbed = fmt.Errorf("ollama unreachable")

// keyedEmbedder 依內容文字回傳預先定義好的向量，用來模擬「語意相關但關鍵字
// 不重疊」的情境。
type keyedEmbedder struct {
	vectors map[string][]float32
}

func (k *keyedEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if v, ok := k.vectors[text]; ok {
		return v, nil
	}
	return []float32{0, 0}, nil
}

func (k *keyedEmbedder) Model() string { return "keyed-fake" }

func TestSearchFallsBackToFTSWithoutEmbedder(t *testing.T) {
	d := testDB(t)
	d.Store(&Memory{Type: "til", Content: "database connection pooling"})

	results, err := d.Search(SearchOptions{Query: "database"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("len = %d, want 1", len(results))
	}
}

func TestSearchFallsBackWhenEmbedderFails(t *testing.T) {
	d := testDB(t)
	d.Store(&Memory{Type: "til", Content: "database connection pooling"})
	d.SetEmbedder(&fakeEmbedder{err: errFakeEmbed})

	results, err := d.Search(SearchOptions{Query: "database"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("len = %d, want 1 (fallback to FTS5)", len(results))
	}
}

func TestSearchSurfacesSemanticOnlyMatch(t *testing.T) {
	d := testDB(t)
	embedder := &keyedEmbedder{
		vectors: map[string][]float32{
			"how do I fix flaky CI":               {1, 0},
			"retry logic solved the CI flakiness": {1, 0}, // 語意相關，關鍵字不重疊
			"unrelated memory about cooking":       {0, 1},
		},
	}
	d.SetEmbedder(embedder)

	d.Store(&Memory{Type: "til", Content: "retry logic solved the CI flakiness"})
	d.Store(&Memory{Type: "til", Content: "unrelated memory about cooking"})

	results, err := d.Search(SearchOptions{Query: "how do I fix flaky CI", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range results {
		if r.Content == "retry logic solved the CI flakiness" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected semantic match for 'retry logic solved the CI flakiness'")
	}
}

func TestSearchRespectsLimitAfterFusion(t *testing.T) {
	d := testDB(t)
	d.SetEmbedder(&fakeEmbedder{vec: []float32{1, 0}, model: "fake"})
	d.Store(&Memory{Type: "til", Content: "database one"})
	d.Store(&Memory{Type: "til", Content: "database two"})
	d.Store(&Memory{Type: "til", Content: "database three"})

	results, err := d.Search(SearchOptions{Query: "database", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("len = %d, want 2", len(results))
	}
}
