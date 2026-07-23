// Package embed 提供把文字轉成向量的 Embedder，供 db package 做語意搜尋用。
package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

// Embedder 把文字轉成向量。
type Embedder interface {
	// Embed 回傳 text 的 embedding 向量。
	Embed(ctx context.Context, text string) ([]float32, error)
	// Model 回傳目前使用的 embedding 模型名稱。
	Model() string
}

// OllamaEmbedder 呼叫本機 Ollama HTTP API（/api/embed）產生 embedding。
type OllamaEmbedder struct {
	baseURL string
	model   string
	hc      *http.Client
}

// NewOllamaEmbedder 建立 OllamaEmbedder，baseURL/model 為空字串時套用預設值。
func NewOllamaEmbedder(baseURL, model string) *OllamaEmbedder {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	if model == "" {
		model = "nomic-embed-text"
	}
	return &OllamaEmbedder{
		baseURL: baseURL,
		model:   model,
		hc:      &http.Client{Timeout: 3 * time.Second},
	}
}

// NewOllamaEmbedderFromEnv 依 MEMORY_MCP_OLLAMA_URL / MEMORY_MCP_EMBED_MODEL
// 環境變數建立 OllamaEmbedder，未設定時使用預設值。
func NewOllamaEmbedderFromEnv() *OllamaEmbedder {
	return NewOllamaEmbedder(os.Getenv("MEMORY_MCP_OLLAMA_URL"), os.Getenv("MEMORY_MCP_EMBED_MODEL"))
}

type embedRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type embedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

// Embed 見 Embedder。
func (o *OllamaEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	body, err := json.Marshal(embedRequest{Model: o.model, Input: text})
	if err != nil {
		return nil, fmt.Errorf("embed marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embed: unexpected status %d", resp.StatusCode)
	}

	var out embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("embed decode: %w", err)
	}
	if len(out.Embeddings) == 0 {
		return nil, fmt.Errorf("embed: empty response")
	}
	return out.Embeddings[0], nil
}

// Model 見 Embedder。
func (o *OllamaEmbedder) Model() string {
	return o.model
}
