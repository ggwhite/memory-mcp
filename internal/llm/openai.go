package llm

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// OpenAI 呼叫 OpenAI 相容的 Chat Completions API（{BaseURL}/chat/completions），
// 也適用 Ollama（BaseURL http://localhost:11434/v1，APIKey 可留空）。
type OpenAI struct {
	BaseURL    string
	APIKey     string
	CheapModel string
	SynthModel string
	// Redact 非 nil 時，送出前套用在 System 與 Prompt。
	Redact func(string) string
	// HTTPClient 為 nil 時用 http.DefaultClient（逾時由 ctx 控制）。
	HTTPClient *http.Client
}

type openaiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openaiRequest struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	Messages  []openaiMessage `json:"messages"`
}

type openaiResponse struct {
	Choices []struct {
		Message struct {
			Content *string `json:"content"`
			Refusal string  `json:"refusal"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

// Complete 見 Client；非 2xx 回傳含 status 的錯誤，refusal 或 content_filter 時回傳錯誤。
func (o *OpenAI) Complete(ctx context.Context, req Request) (string, error) {
	req = applyRedact(o.Redact, req)
	var msgs []openaiMessage
	if req.System != "" {
		msgs = append(msgs, openaiMessage{Role: "system", Content: req.System})
	}
	msgs = append(msgs, openaiMessage{Role: "user", Content: req.Prompt})
	body := openaiRequest{Model: o.Model(req.Tier), MaxTokens: maxTokens(req), Messages: msgs}
	headers := map[string]string{}
	if o.APIKey != "" {
		headers["Authorization"] = "Bearer " + o.APIKey
	}
	var out openaiResponse
	if err := postJSON(ctx, o.HTTPClient, req.Tier, strings.TrimSuffix(o.BaseURL, "/")+"/chat/completions", headers, body, &out); err != nil {
		return "", fmt.Errorf("openai: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("openai: empty response (no choices)")
	}
	ch := out.Choices[0]
	if ch.Message.Refusal != "" || ch.FinishReason == "content_filter" {
		return "", fmt.Errorf("openai: model refused the request")
	}
	if ch.Message.Content == nil || *ch.Message.Content == "" {
		return "", fmt.Errorf("openai: empty response (finish_reason %q)", ch.FinishReason)
	}
	return *ch.Message.Content, nil
}

// Name 見 Client。
func (o *OpenAI) Name() string { return "openai/" + o.CheapModel }

// Model 見 Modeler。
func (o *OpenAI) Model(t Tier) string { return pickModel(t, o.CheapModel, o.SynthModel) }
