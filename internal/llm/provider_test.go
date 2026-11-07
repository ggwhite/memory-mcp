package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const fakeSecret = "sk-ant-THIS_IS_NOT_A_REAL_KEY_123456"

// recorder 記下 httptest 收到的每個 request body 與 header，並依序回傳 replies。
type recorder struct {
	mu      sync.Mutex
	bodies  []map[string]any
	raw     []string
	headers []http.Header
}

func (r *recorder) server(t *testing.T, replies ...func(w http.ResponseWriter, req *http.Request)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		data, _ := io.ReadAll(req.Body)
		var body map[string]any
		json.Unmarshal(data, &body)
		r.mu.Lock()
		i := len(r.bodies)
		r.bodies = append(r.bodies, body)
		r.raw = append(r.raw, string(data))
		r.headers = append(r.headers, req.Header.Clone())
		r.mu.Unlock()
		replies[min(i, len(replies)-1)](w, req)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func reply(status int, body string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		io.WriteString(w, body)
	}
}

// hang 一直等到 client 斷線（或保底 5 秒），用來測逾時。
func hang(w http.ResponseWriter, req *http.Request) {
	select {
	case <-req.Context().Done():
	case <-time.After(5 * time.Second):
	}
}

func anthropicText(texts ...string) string {
	var parts []string
	for _, s := range texts {
		b, _ := json.Marshal(s)
		parts = append(parts, `{"type":"text","text":`+string(b)+`}`)
	}
	return `{"content":[` + strings.Join(parts, ",") + `],"stop_reason":"end_turn"}`
}

func openaiText(s string) string {
	b, _ := json.Marshal(s)
	return `{"choices":[{"message":{"role":"assistant","content":` + string(b) + `},"finish_reason":"stop"}]}`
}

func newAnthropic(base string) *Anthropic {
	return &Anthropic{BaseURL: base, APIKey: "test-key", CheapModel: "cheap-m", SynthModel: "synth-m"}
}

func newOpenAI(base string) *OpenAI {
	return &OpenAI{BaseURL: base, APIKey: "test-key", CheapModel: "cheap-m", SynthModel: "synth-m"}
}

func TestAnthropicSuccess(t *testing.T) {
	var rec recorder
	srv := rec.server(t, reply(200, anthropicText("你好", "世界")))
	out, err := newAnthropic(srv.URL).Complete(context.Background(), Request{System: "sys", Prompt: "hi", Tier: TierSynth, MaxTokens: 77})
	if err != nil {
		t.Fatal(err)
	}
	if out != "你好世界" {
		t.Errorf("out = %q", out)
	}
	h := rec.headers[0]
	if h.Get("x-api-key") != "test-key" || h.Get("anthropic-version") != "2023-06-01" {
		t.Errorf("headers = %v", h)
	}
	b := rec.bodies[0]
	if b["model"] != "synth-m" || b["max_tokens"] != float64(77) || b["system"] != "sys" {
		t.Errorf("body = %v", b)
	}
	msgs := b["messages"].([]any)
	if m := msgs[0].(map[string]any); len(msgs) != 1 || m["role"] != "user" || m["content"] != "hi" {
		t.Errorf("messages = %v", msgs)
	}
}

func TestAnthropicCheapModelAndDefaultMaxTokens(t *testing.T) {
	var rec recorder
	srv := rec.server(t, reply(200, anthropicText("x")))
	if _, err := newAnthropic(srv.URL).Complete(context.Background(), Request{Prompt: "p"}); err != nil {
		t.Fatal(err)
	}
	if b := rec.bodies[0]; b["model"] != "cheap-m" || b["max_tokens"].(float64) <= 0 {
		t.Errorf("body = %v", b)
	}
	if _, ok := rec.bodies[0]["system"]; ok {
		t.Error("empty system should be omitted")
	}
}

func TestAnthropicErrors(t *testing.T) {
	cases := map[string]struct {
		reply func(http.ResponseWriter, *http.Request)
		want  string
	}{
		"429":     {reply(429, `{"type":"error","error":{"type":"rate_limit_error"}}`), "429"},
		"refusal": {reply(200, `{"content":[],"stop_reason":"refusal"}`), "refus"},
		"empty":   {reply(200, `{"content":[],"stop_reason":"end_turn"}`), "empty"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var rec recorder
			srv := rec.server(t, tc.reply)
			_, err := newAnthropic(srv.URL).Complete(context.Background(), Request{Prompt: "p"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestAnthropicTimeout(t *testing.T) {
	var rec recorder
	srv := rec.server(t, hang)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := newAnthropic(srv.URL).Complete(ctx, Request{Prompt: "p"})
	if err == nil {
		t.Fatal("want timeout error")
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("took %v, want prompt timeout", time.Since(start))
	}
}

func TestAnthropicRedact(t *testing.T) {
	var rec recorder
	srv := rec.server(t, reply(200, anthropicText("ok")))
	a := newAnthropic(srv.URL)
	SetRedact(a, func(s string) string { return strings.ReplaceAll(s, fakeSecret, "[REDACTED]") })
	if _, err := a.Complete(context.Background(), Request{System: "s " + fakeSecret, Prompt: "p " + fakeSecret}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rec.raw[0], fakeSecret) || strings.Count(rec.raw[0], "[REDACTED]") != 2 {
		t.Errorf("body not redacted: %s", rec.raw[0])
	}
}

func TestAnthropicCompleteJSONRetry(t *testing.T) {
	var rec recorder
	srv := rec.server(t, reply(200, anthropicText("oops not json")), reply(200, anthropicText("```json\n{\"a\":1}\n```")))
	var out struct{ A int }
	if err := CompleteJSON(context.Background(), newAnthropic(srv.URL), Request{Prompt: "p"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.A != 1 || len(rec.bodies) != 2 {
		t.Fatalf("out = %+v, requests = %d", out, len(rec.bodies))
	}
	second := rec.bodies[1]["messages"].([]any)[0].(map[string]any)["content"].(string)
	if !strings.Contains(second, "invalid character") {
		t.Errorf("retry prompt should carry parse error: %q", second)
	}
}

func TestOpenAISuccess(t *testing.T) {
	var rec recorder
	srv := rec.server(t, reply(200, openaiText("答案")))
	out, err := newOpenAI(srv.URL).Complete(context.Background(), Request{System: "sys", Prompt: "hi", Tier: TierSynth, MaxTokens: 33})
	if err != nil {
		t.Fatal(err)
	}
	if out != "答案" {
		t.Errorf("out = %q", out)
	}
	if h := rec.headers[0]; h.Get("Authorization") != "Bearer test-key" {
		t.Errorf("headers = %v", h)
	}
	b := rec.bodies[0]
	if b["model"] != "synth-m" || b["max_tokens"] != float64(33) {
		t.Errorf("body = %v", b)
	}
	msgs := b["messages"].([]any)
	sys, user := msgs[0].(map[string]any), msgs[1].(map[string]any)
	if len(msgs) != 2 || sys["role"] != "system" || sys["content"] != "sys" || user["role"] != "user" || user["content"] != "hi" {
		t.Errorf("messages = %v", msgs)
	}
}

func TestOpenAINoKeyOmitsAuthAndSystem(t *testing.T) {
	var rec recorder
	srv := rec.server(t, reply(200, openaiText("ok")))
	o := newOpenAI(srv.URL)
	o.APIKey = ""
	if _, err := o.Complete(context.Background(), Request{Prompt: "hi"}); err != nil {
		t.Fatal(err)
	}
	if rec.headers[0].Get("Authorization") != "" {
		t.Error("no key ⇒ no Authorization header")
	}
	if msgs := rec.bodies[0]["messages"].([]any); len(msgs) != 1 || rec.bodies[0]["model"] != "cheap-m" {
		t.Errorf("body = %v", rec.bodies[0])
	}
}

func TestOpenAIErrors(t *testing.T) {
	cases := map[string]struct {
		reply func(http.ResponseWriter, *http.Request)
		want  string
	}{
		"429":            {reply(429, `{"error":{"message":"rate limited"}}`), "429"},
		"refusal":        {reply(200, `{"choices":[{"message":{"role":"assistant","content":null,"refusal":"I can't"}}]}`), "refus"},
		"content_filter": {reply(200, `{"choices":[{"message":{"content":""},"finish_reason":"content_filter"}]}`), "refus"},
		"no choices":     {reply(200, `{"choices":[]}`), "empty"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var rec recorder
			srv := rec.server(t, tc.reply)
			_, err := newOpenAI(srv.URL).Complete(context.Background(), Request{Prompt: "p"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestOpenAITimeout(t *testing.T) {
	var rec recorder
	srv := rec.server(t, hang)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := newOpenAI(srv.URL).Complete(ctx, Request{Prompt: "p"}); err == nil {
		t.Fatal("want timeout error")
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("took %v, want prompt timeout", time.Since(start))
	}
}

func TestOpenAIRedact(t *testing.T) {
	var rec recorder
	srv := rec.server(t, reply(200, openaiText("ok")))
	o := newOpenAI(srv.URL)
	SetRedact(o, func(s string) string { return strings.ReplaceAll(s, fakeSecret, "[REDACTED]") })
	if _, err := o.Complete(context.Background(), Request{System: fakeSecret, Prompt: "p " + fakeSecret}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rec.raw[0], fakeSecret) || strings.Count(rec.raw[0], "[REDACTED]") != 2 {
		t.Errorf("body not redacted: %s", rec.raw[0])
	}
}

func TestOpenAICompleteJSONRetry(t *testing.T) {
	var rec recorder
	srv := rec.server(t, reply(200, openaiText("{broken")), reply(200, openaiText(`{"a":2}`)))
	var out struct{ A int }
	if err := CompleteJSON(context.Background(), newOpenAI(srv.URL), Request{Prompt: "p"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.A != 2 || len(rec.bodies) != 2 {
		t.Fatalf("out = %+v, requests = %d", out, len(rec.bodies))
	}
	msgs := rec.bodies[1]["messages"].([]any)
	if last := msgs[len(msgs)-1].(map[string]any)["content"].(string); !strings.Contains(last, "invalid character") {
		t.Errorf("retry prompt = %q", last)
	}
}

// fakeClaude 寫一支假的 claude 指令：把參數、防遞迴環境變數與 stdin 印出來。
func fakeClaude(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude")
	script := "#!/bin/sh\necho \"args:$*\"\necho \"env:$MEMORY_MCP_SUMMARIZING\"\ncat\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestClaudeCLI(t *testing.T) {
	c := &ClaudeCLI{Command: fakeClaude(t), CheapModel: "haiku", SynthModel: "sonnet"}
	out, err := c.Complete(context.Background(), Request{Prompt: "提問"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"args:-p --model haiku --no-session-persistence --tools  --strict-mcp-config", "env:1", "提問"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
	out, _ = c.Complete(context.Background(), Request{System: "系統", Prompt: "提問", Tier: TierSynth})
	if !strings.Contains(out, "--model sonnet") || !strings.Contains(out, "系統\n\n提問") {
		t.Errorf("synth out = %q", out)
	}
	if c.Name() != "claude-cli/haiku" {
		t.Errorf("Name = %q", c.Name())
	}
}

func TestClaudeCLIFailure(t *testing.T) {
	c := &ClaudeCLI{Command: filepath.Join(t.TempDir(), "missing"), CheapModel: "haiku", SynthModel: "sonnet"}
	if _, err := c.Complete(context.Background(), Request{Prompt: "p"}); err == nil {
		t.Error("want error for missing binary")
	}
}
