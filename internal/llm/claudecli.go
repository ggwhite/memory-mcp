package llm

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/ggwhite/memory-mcp/internal/hook"
)

// ClaudeCLI 以 claude -p（不保存 session、不載入工具與 MCP）呼叫模型，並設定防遞迴環境變數，
// 讓子 session 的 hook 直接跳過；預設移除 Anthropic 金鑰環境變數（見 childEnv）。System 有值時接在 prompt 前面一起由 stdin 送入。
type ClaudeCLI struct {
	// Command claude 執行檔路徑，空字串用 "claude"。
	Command    string
	CheapModel string
	SynthModel string
}

// Complete 見 Client。
func (c *ClaudeCLI) Complete(ctx context.Context, req Request) (string, error) {
	ctx, cancel := withTierTimeout(ctx, req.Tier)
	defer cancel()
	command := c.Command
	if command == "" {
		command = "claude"
	}
	prompt := req.Prompt
	if req.System != "" {
		prompt = req.System + "\n\n" + prompt
	}
	cmd := exec.CommandContext(ctx, command, "-p", "--model", c.Model(req.Tier), "--no-session-persistence", "--tools", "", "--strict-mcp-config")
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Env = append(childEnv(os.Environ()), hook.SummarizingEnv+"=1")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("claude -p: %w", err)
	}
	return string(out), nil
}

// KeepAPIKeyEnv 設為 "1" 時，claude -p 子行程保留 ANTHROPIC_API_KEY / ANTHROPIC_AUTH_TOKEN。
const KeepAPIKeyEnv = "MEMORY_MCP_LLM_CLAUDE_KEEP_API_KEY"

// childEnv 預設移除 Anthropic 金鑰，讓 claude -p 走使用者的訂閱方案，而不是靜默改用環境裡剛好存在的
// API key 計費（付費 provider 必須明確選用）；KeepAPIKeyEnv=1 時原樣保留。
func childEnv(env []string) []string {
	if os.Getenv(KeepAPIKeyEnv) == "1" {
		return env
	}
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") || strings.HasPrefix(kv, "ANTHROPIC_AUTH_TOKEN=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// Name 見 Client。
func (c *ClaudeCLI) Name() string { return "claude-cli/" + c.CheapModel }

// Model 見 Modeler。
func (c *ClaudeCLI) Model(t Tier) string { return pickModel(t, c.CheapModel, c.SynthModel) }
