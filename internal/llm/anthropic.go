package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Anthropic 以 net/http 呼叫 Anthropic Messages API（{BaseURL}/v1/messages）。
type Anthropic struct {
	BaseURL    string
	APIKey     string
	CheapModel string
	SynthModel string
	// Redact 非 nil 時，送出前套用在 System 與 Prompt。
	Redact func(string) string
	// HTTPClient 為 nil 時用 http.DefaultClient（逾時由 ctx 控制）。
	HTTPClient *http.Client
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
}

// Complete 見 Client；非 2xx 回傳含 status 的錯誤，stop_reason 為 refusal 時回傳錯誤。
func (a *Anthropic) Complete(ctx context.Context, req Request) (string, error) {
	req = applyRedact(a.Redact, req)
	body := anthropicRequest{
		Model:     a.Model(req.Tier),
		MaxTokens: maxTokens(req),
		System:    req.System,
		Messages:  []anthropicMessage{{Role: "user", Content: req.Prompt}},
	}
	headers := map[string]string{"x-api-key": a.APIKey, "anthropic-version": "2023-06-01"}
	var out anthropicResponse
	if err := postJSON(ctx, a.HTTPClient, req.Tier, strings.TrimSuffix(a.BaseURL, "/")+"/v1/messages", headers, body, &out); err != nil {
		return "", fmt.Errorf("anthropic: %w", err)
	}
	if out.StopReason == "refusal" {
		return "", fmt.Errorf("anthropic: model refused the request")
	}
	var b strings.Builder
	for _, c := range out.Content {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	if b.Len() == 0 {
		return "", fmt.Errorf("anthropic: empty response (stop_reason %q)", out.StopReason)
	}
	return b.String(), nil
}

// Name 見 Client。
func (a *Anthropic) Name() string { return "anthropic/" + a.CheapModel }

// Model 見 Modeler。
func (a *Anthropic) Model(t Tier) string { return pickModel(t, a.CheapModel, a.SynthModel) }

// postJSON 以 tier 預設逾時 POST JSON 到 url 並把 2xx 回應解到 out；非 2xx 回傳含 status 與部分 body 的錯誤。
func postJSON(ctx context.Context, hc *http.Client, tier Tier, url string, headers map[string]string, body, out any) error {
	ctx, cancel := withTierTimeout(ctx, tier)
	defer cancel()
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("call: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	return nil
}
