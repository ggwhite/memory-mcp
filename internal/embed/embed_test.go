package embed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOllamaEmbedderSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req embedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Model != "test-model" {
			t.Fatalf("model = %q, want test-model", req.Model)
		}
		if req.Input != "hello world" {
			t.Fatalf("input = %q, want hello world", req.Input)
		}
		json.NewEncoder(w).Encode(embedResponse{Embeddings: [][]float32{{0.1, 0.2, 0.3}}})
	}))
	defer srv.Close()

	e := NewOllamaEmbedder(srv.URL, "test-model")
	vec, err := e.Embed(context.Background(), "hello world")
	if err != nil {
		t.Fatal(err)
	}
	if len(vec) != 3 {
		t.Fatalf("len(vec) = %d, want 3", len(vec))
	}
	if vec[0] != 0.1 {
		t.Fatalf("vec[0] = %f, want 0.1", vec[0])
	}
}

func TestOllamaEmbedderErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	e := NewOllamaEmbedder(srv.URL, "test-model")
	if _, err := e.Embed(context.Background(), "hello"); err == nil {
		t.Fatal("expected error for 500 status")
	}
}

func TestOllamaEmbedderTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()

	e := NewOllamaEmbedder(srv.URL, "test-model")
	e.hc.Timeout = 50 * time.Millisecond
	if _, err := e.Embed(context.Background(), "hello"); err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestOllamaEmbedderModel(t *testing.T) {
	e := NewOllamaEmbedder("http://example.com", "my-model")
	if e.Model() != "my-model" {
		t.Fatalf("Model() = %q, want my-model", e.Model())
	}
}

func TestOllamaEmbedderDefaults(t *testing.T) {
	e := NewOllamaEmbedder("", "")
	if e.baseURL != "http://localhost:11434" {
		t.Fatalf("baseURL = %q, want http://localhost:11434", e.baseURL)
	}
	if e.model != "nomic-embed-text" {
		t.Fatalf("model = %q, want nomic-embed-text", e.model)
	}
}
