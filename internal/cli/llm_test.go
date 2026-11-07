package cli

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

const fakeLLMKey = "sk-ant-NOT_A_REAL_KEY_FOR_TESTS_0001"

// runLLMStatus 以指定 env 與 lookPath 執行 llm status，回傳 stdout。
func runLLMStatus(t *testing.T, env map[string]string, claudeFound bool, extra ...string) (string, error) {
	t.Helper()
	for _, k := range []string{"MEMORY_MCP_LLM_PROVIDER", "MEMORY_MCP_LLM_API_KEY", "MEMORY_MCP_LLM_BASE_URL", "MEMORY_MCP_LLM_MODEL_CHEAP", "MEMORY_MCP_LLM_MODEL_SYNTH", "MEMORY_MCP_LLM_DAILY_CALLS", "ANTHROPIC_API_KEY", "OPENAI_API_KEY"} {
		t.Setenv(k, env[k])
	}
	orig := llmLookPath
	llmLookPath = func(string) (string, error) {
		if claudeFound {
			return "/fake/claude", nil
		}
		return "", errors.New("not found")
	}
	var out strings.Builder
	t.Cleanup(func() {
		llmLookPath = orig
		rootCmd.SetOut(nil)
		remoteFlag = ""
	})
	rootCmd.SetOut(&out)
	err := runCLI(t, filepath.Join(t.TempDir(), "m.db"), append([]string{"llm", "status"}, extra...)...)
	return out.String(), err
}

func TestLLMStatusDisabled(t *testing.T) {
	out, err := runLLMStatus(t, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "disabled: ") || !strings.Contains(out, "claude") {
		t.Errorf("out = %q", out)
	}
}

func TestLLMStatusKeyAloneDoesNotEnable(t *testing.T) {
	out, err := runLLMStatus(t, map[string]string{"ANTHROPIC_API_KEY": fakeLLMKey}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "disabled: ") || strings.Contains(out, fakeLLMKey) {
		t.Errorf("out = %q", out)
	}
}

func TestLLMStatusClaudeCLI(t *testing.T) {
	out, err := runLLMStatus(t, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"provider: claude-cli/haiku", "cheap: haiku", "synth: sonnet", "budget: 0/50"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
}

func TestLLMStatusAnthropicNeverPrintsKey(t *testing.T) {
	out, err := runLLMStatus(t, map[string]string{"MEMORY_MCP_LLM_PROVIDER": "anthropic", "ANTHROPIC_API_KEY": fakeLLMKey, "MEMORY_MCP_LLM_DAILY_CALLS": "0"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, fakeLLMKey) || strings.Contains(out, "NOT_A_REAL") {
		t.Fatalf("key leaked: %q", out)
	}
	for _, want := range []string{"provider: anthropic/claude-haiku-5-5", "synth: claude-sonnet-5-5", "budget: unlimited"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
}

func TestLLMStatusMisconfigured(t *testing.T) {
	out, err := runLLMStatus(t, map[string]string{"MEMORY_MCP_LLM_PROVIDER": "openai"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "disabled: ") || !strings.Contains(out, "MEMORY_MCP_LLM_MODEL_CHEAP") {
		t.Errorf("out = %q", out)
	}
}

func TestLLMStatusRefusesRemote(t *testing.T) {
	_, err := runLLMStatus(t, nil, true, "--remote", "http://127.0.0.1:1")
	if err == nil || !strings.Contains(err.Error(), "local DB") {
		t.Fatalf("err = %v", err)
	}
}

func TestRedactForLLM(t *testing.T) {
	got := redactForLLM("token=abcdef123456 and " + fakeLLMKey)
	if strings.Contains(got, "abcdef123456") || strings.Contains(got, fakeLLMKey) {
		t.Errorf("not redacted: %q", got)
	}
}
