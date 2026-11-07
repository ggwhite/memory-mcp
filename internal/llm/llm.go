// Package llm 提供可選的 LLM provider 抽象（anthropic、OpenAI 相容 API、claude CLI），
// 供 summary、compact 等功能使用。沒設定 provider 時 FromEnv 回傳 nil，呼叫端應降級為非 LLM 路徑。
// 本 package 不可 import internal/db，避免 import cycle。
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Tier 呼叫的用途等級，決定使用的模型與預設逾時。
type Tier int

const (
	// TierCheap 便宜快速的工作，例如分類、去重。
	TierCheap Tier = iota
	// TierSynth 需要較好品質的綜合工作，例如摘要、mental model。
	TierSynth
)

// String 回傳 tier 的名稱（cheap／synth）。
func (t Tier) String() string {
	if t == TierSynth {
		return "synth"
	}
	return "cheap"
}

// Request 一次 LLM 呼叫的輸入；MaxTokens 為 0 時依 tier 套用預設值。
type Request struct {
	System    string
	Prompt    string
	Tier      Tier
	MaxTokens int
}

// Client LLM provider 的共同介面。
type Client interface {
	// Complete 送出 req 並回傳模型輸出文字；ctx 沒有 deadline 時依 tier 套用預設逾時。
	Complete(ctx context.Context, req Request) (string, error)
	// Name 回傳 provider 與 cheap 模型，例如 "anthropic/claude-haiku-5-5"、"claude-cli/haiku"，用於 provenance 與 log。
	Name() string
}

// Modeler 可回報各 tier 實際使用模型的 Client 會實作此介面。
type Modeler interface {
	Model(t Tier) string
}

// ModelFor 回傳 c 在 tier t 使用的模型；c 沒實作 Modeler 時回傳空字串。
func ModelFor(c Client, t Tier) string {
	if m, ok := c.(Modeler); ok {
		return m.Model(t)
	}
	return ""
}

// ErrBudget 今日 LLM 呼叫次數已達 MEMORY_MCP_LLM_DAILY_CALLS 上限；呼叫端應記 log 並跳過。
var ErrBudget = errors.New("llm: daily call budget exceeded")

const (
	cheapTimeout   = 20 * time.Second
	synthTimeout   = 90 * time.Second
	cheapMaxTokens = 1024
	synthMaxTokens = 4096
)

// withTierTimeout ctx 已有 deadline 就沿用，否則依 tier 套用預設逾時。
func withTierTimeout(ctx context.Context, t Tier) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	if t == TierSynth {
		return context.WithTimeout(ctx, synthTimeout)
	}
	return context.WithTimeout(ctx, cheapTimeout)
}

func maxTokens(req Request) int {
	if req.MaxTokens > 0 {
		return req.MaxTokens
	}
	if req.Tier == TierSynth {
		return synthMaxTokens
	}
	return cheapMaxTokens
}

func pickModel(t Tier, cheap, synth string) string {
	if t == TierSynth {
		return synth
	}
	return cheap
}

const jsonOnlyInstruction = "只輸出一個合法的 JSON 值，不要任何說明文字，也不要用 ``` 包起來。"

// CompleteJSON 要求模型只輸出 JSON，去掉 ``` 圍欄後 json.Unmarshal 到 out；
// 解析失敗時把錯誤附加到 prompt 後重試一次，仍失敗就回傳解析錯誤。
func CompleteJSON(ctx context.Context, c Client, req Request, out any) error {
	if req.System == "" {
		req.System = jsonOnlyInstruction
	} else {
		req.System += "\n\n" + jsonOnlyInstruction
	}
	text, err := c.Complete(ctx, req)
	if err != nil {
		return err
	}
	perr := json.Unmarshal([]byte(stripFences(text)), out)
	if perr == nil {
		return nil
	}
	req.Prompt = fmt.Sprintf("%s\n\n上一次的輸出不是合法 JSON（%v），請只輸出合法 JSON。", req.Prompt, perr)
	text, err = c.Complete(ctx, req)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(stripFences(text)), out); err != nil {
		return fmt.Errorf("llm: invalid JSON after retry: %w", err)
	}
	return nil
}

// stripFences 去掉模型常加的 ```json ... ``` 圍欄與前後空白。
func stripFences(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	} else {
		s = strings.TrimPrefix(s, "```")
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "```"))
}

// FuncClient 把函式包成 Client，給測試與簡單的 adapter 用；Name 回傳 ID。
type FuncClient struct {
	ID string
	Fn func(ctx context.Context, req Request) (string, error)
}

// Complete 見 Client。
func (f FuncClient) Complete(ctx context.Context, req Request) (string, error) {
	return f.Fn(ctx, req)
}

// Name 見 Client。
func (f FuncClient) Name() string { return f.ID }

// SetRedact 設定網路 provider（anthropic、openai）送出前套用在 System 與 Prompt 的遮蔽函式；
// 其他 Client（例如 claude-cli）不受影響。
func SetRedact(c Client, redact func(string) string) {
	switch p := c.(type) {
	case *Anthropic:
		p.Redact = redact
	case *OpenAI:
		p.Redact = redact
	}
}

func applyRedact(redact func(string) string, req Request) Request {
	if redact == nil {
		return req
	}
	req.System = redact(req.System)
	req.Prompt = redact(req.Prompt)
	return req
}
