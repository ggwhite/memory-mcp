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
// 讓子 session 的 hook 直接跳過。System 有值時接在 prompt 前面一起由 stdin 送入。
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
	cmd.Env = append(os.Environ(), hook.SummarizingEnv+"=1")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("claude -p: %w", err)
	}
	return string(out), nil
}

// Name 見 Client。
func (c *ClaudeCLI) Name() string { return "claude-cli/" + c.CheapModel }

// Model 見 Modeler。
func (c *ClaudeCLI) Model(t Tier) string { return pickModel(t, c.CheapModel, c.SynthModel) }
