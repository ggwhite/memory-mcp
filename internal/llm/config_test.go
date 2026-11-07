package llm

import (
	"errors"
	"strings"
	"testing"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func lookPathFound(string) (string, error) { return "/usr/local/bin/claude", nil }

func lookPathMissing(string) (string, error) { return "", errors.New("not found") }

func TestFromEnv(t *testing.T) {
	cases := []struct {
		name     string
		env      map[string]string
		lookPath func(string) (string, error)
		wantName string // 空字串表示預期 nil client
		wantErr  string
	}{
		{name: "nothing set, no claude", env: nil, lookPath: lookPathMissing},
		{name: "nothing set, claude on PATH", env: nil, lookPath: lookPathFound, wantName: "claude-cli/haiku"},
		{name: "anthropic key alone never bills", env: map[string]string{"ANTHROPIC_API_KEY": fakeSecret}, lookPath: lookPathMissing},
		{name: "openai key alone never bills", env: map[string]string{"OPENAI_API_KEY": "x", "MEMORY_MCP_LLM_API_KEY": "y"}, lookPath: lookPathMissing},
		{name: "anthropic key alone with claude ⇒ claude-cli", env: map[string]string{"ANTHROPIC_API_KEY": fakeSecret}, lookPath: lookPathFound, wantName: "claude-cli/haiku"},
		{name: "anthropic without key", env: map[string]string{"MEMORY_MCP_LLM_PROVIDER": "anthropic"}, lookPath: lookPathFound, wantErr: "MEMORY_MCP_LLM_API_KEY"},
		{name: "anthropic does not use OPENAI_API_KEY", env: map[string]string{"MEMORY_MCP_LLM_PROVIDER": "anthropic", "OPENAI_API_KEY": "x"}, lookPath: lookPathMissing, wantErr: "ANTHROPIC_API_KEY"},
		{name: "anthropic with fallback key", env: map[string]string{"MEMORY_MCP_LLM_PROVIDER": "anthropic", "ANTHROPIC_API_KEY": fakeSecret}, lookPath: lookPathMissing, wantName: "anthropic/claude-haiku-5-5"},
		{name: "anthropic model override", env: map[string]string{"MEMORY_MCP_LLM_PROVIDER": "anthropic", "MEMORY_MCP_LLM_API_KEY": "k", "MEMORY_MCP_LLM_MODEL_CHEAP": "m1"}, lookPath: lookPathMissing, wantName: "anthropic/m1"},
		{name: "openai without models", env: map[string]string{"MEMORY_MCP_LLM_PROVIDER": "openai", "MEMORY_MCP_LLM_API_KEY": "k"}, lookPath: lookPathMissing, wantErr: "MEMORY_MCP_LLM_MODEL_CHEAP"},
		{name: "openai without synth model", env: map[string]string{"MEMORY_MCP_LLM_PROVIDER": "openai", "MEMORY_MCP_LLM_API_KEY": "k", "MEMORY_MCP_LLM_MODEL_CHEAP": "a"}, lookPath: lookPathMissing, wantErr: "MEMORY_MCP_LLM_MODEL_SYNTH"},
		{name: "openai default base without key", env: map[string]string{"MEMORY_MCP_LLM_PROVIDER": "openai", "MEMORY_MCP_LLM_MODEL_CHEAP": "a", "MEMORY_MCP_LLM_MODEL_SYNTH": "b"}, lookPath: lookPathMissing, wantErr: "OPENAI_API_KEY"},
		{name: "openai with fallback key", env: map[string]string{"MEMORY_MCP_LLM_PROVIDER": "openai", "OPENAI_API_KEY": "k", "MEMORY_MCP_LLM_MODEL_CHEAP": "a", "MEMORY_MCP_LLM_MODEL_SYNTH": "b"}, lookPath: lookPathMissing, wantName: "openai/a"},
		{name: "openai local base without key", env: map[string]string{"MEMORY_MCP_LLM_PROVIDER": "openai", "MEMORY_MCP_LLM_BASE_URL": "http://localhost:11434/v1", "MEMORY_MCP_LLM_MODEL_CHEAP": "a", "MEMORY_MCP_LLM_MODEL_SYNTH": "b"}, lookPath: lookPathMissing, wantName: "openai/a"},
		{name: "off", env: map[string]string{"MEMORY_MCP_LLM_PROVIDER": "off", "ANTHROPIC_API_KEY": "k"}, lookPath: lookPathFound},
		{name: "claude-cli explicit, missing binary", env: map[string]string{"MEMORY_MCP_LLM_PROVIDER": "claude-cli"}, lookPath: lookPathMissing, wantErr: "claude"},
		{name: "claude-cli model override", env: map[string]string{"MEMORY_MCP_LLM_PROVIDER": "claude-cli", "MEMORY_MCP_LLM_MODEL_CHEAP": "sonnet"}, lookPath: lookPathFound, wantName: "claude-cli/sonnet"},
		{name: "unknown provider", env: map[string]string{"MEMORY_MCP_LLM_PROVIDER": "gemini"}, lookPath: lookPathFound, wantErr: "MEMORY_MCP_LLM_PROVIDER"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := FromEnv(envOf(tc.env), tc.lookPath)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				if strings.Contains(err.Error(), fakeSecret) {
					t.Error("error must not leak the key")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantName == "" {
				if c != nil {
					t.Fatalf("want nil client, got %s", c.Name())
				}
				return
			}
			if c == nil || c.Name() != tc.wantName {
				t.Fatalf("client = %v, want %s", c, tc.wantName)
			}
		})
	}
}

func TestFromEnvAnthropicDefaults(t *testing.T) {
	c, err := FromEnv(envOf(map[string]string{"MEMORY_MCP_LLM_PROVIDER": "anthropic", "MEMORY_MCP_LLM_API_KEY": "k"}), lookPathMissing)
	if err != nil {
		t.Fatal(err)
	}
	a := c.(*Anthropic)
	if a.BaseURL != "https://api.anthropic.com" || a.CheapModel != "claude-haiku-5-5" || a.SynthModel != "claude-sonnet-5-5" || a.APIKey != "k" {
		t.Errorf("anthropic = %+v", a)
	}
	if ModelFor(c, TierSynth) != "claude-sonnet-5-5" {
		t.Errorf("synth model = %q", ModelFor(c, TierSynth))
	}
}

func TestFromEnvOpenAIDefaultsAndClaudeCLIModels(t *testing.T) {
	c, _ := FromEnv(envOf(map[string]string{"MEMORY_MCP_LLM_PROVIDER": "openai", "MEMORY_MCP_LLM_API_KEY": "k", "MEMORY_MCP_LLM_MODEL_CHEAP": "a", "MEMORY_MCP_LLM_MODEL_SYNTH": "b"}), lookPathMissing)
	if o := c.(*OpenAI); o.BaseURL != "https://api.openai.com/v1" || o.SynthModel != "b" {
		t.Errorf("openai = %+v", o)
	}
	c, _ = FromEnv(envOf(nil), lookPathFound)
	if cl := c.(*ClaudeCLI); cl.CheapModel != "haiku" || cl.SynthModel != "sonnet" || cl.Command != "/usr/local/bin/claude" {
		t.Errorf("claude-cli = %+v", cl)
	}
}

func TestDailyLimit(t *testing.T) {
	for v, want := range map[string]int{"": 50, "0": 0, "7": 7} {
		n, err := DailyLimit(envOf(map[string]string{"MEMORY_MCP_LLM_DAILY_CALLS": v}))
		if err != nil || n != want {
			t.Errorf("%q: n=%d err=%v, want %d", v, n, err, want)
		}
	}
	for _, v := range []string{"abc", "-1"} {
		if _, err := DailyLimit(envOf(map[string]string{"MEMORY_MCP_LLM_DAILY_CALLS": v})); err == nil {
			t.Errorf("%q: want error", v)
		}
	}
}

func TestDisabledReason(t *testing.T) {
	if r := DisabledReason(envOf(map[string]string{"MEMORY_MCP_LLM_PROVIDER": "off"})); !strings.Contains(r, "off") {
		t.Errorf("off reason = %q", r)
	}
	if r := DisabledReason(envOf(nil)); !strings.Contains(r, "claude") {
		t.Errorf("unset reason = %q", r)
	}
}
