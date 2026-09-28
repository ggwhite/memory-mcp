package summarize

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"memory-mcp/internal/db"
	"memory-mcp/internal/hook"
)

const (
	MaxTranscriptRunes = 60000
	MinUserTurns       = 3
	AutoTag            = "auto-summary"
	skipToken          = "SKIP"
)

// Runner 把 prompt 交給 LLM 並回傳輸出。
type Runner func(ctx context.Context, prompt string) (string, error)

// Deps Run 的外部依賴，測試時可替換。
type Deps struct {
	Store db.Store
	Run   Runner
	Logf  func(format string, args ...any)
}

// BuildPrompt 組出摘要 prompt。
func BuildPrompt(project string, date time.Time, transcript string) string {
	return fmt.Sprintf(`以下是一段 Claude Code 對話紀錄（project: %s，日期: %s）。
用繁體中文寫一段工作摘要，600 字以內，開頭寫日期，包含：
1. 做了什麼
2. 關鍵決定與理由
3. 還沒做完的部分
保留具體識別資訊（檔案路徑、MR 編號、指令、主機名）。
不要寫出密碼、金鑰、token 的值。
如果這段對話只是閒聊、沒有實際工作內容，只輸出 SKIP。

<transcript>
%s
</transcript>
`, project, date.Format("2006-01-02"), transcript)
}

// Run 解析 transcript，符合條件時產生並儲存 summary。跳過時回傳 nil。
func Run(ctx context.Context, in hook.Input, deps Deps) error {
	project := hook.Project(in.Cwd)
	logf := func(format string, args ...any) {
		deps.Logf(in.SessionID+" "+project+" "+format, args...)
	}

	f, err := os.Open(in.TranscriptPath)
	if err != nil {
		logf("open transcript: %v", err)
		return err
	}
	tr, err := ParseTranscript(f, MaxTranscriptRunes)
	f.Close()
	if err != nil {
		logf("parse transcript: %v", err)
		return err
	}
	if tr.UserTurns < MinUserTurns {
		logf("skip: %d user turns", tr.UserTurns)
		return nil
	}

	recent, err := deps.Store.List(db.ListOptions{Type: "summary", Limit: 50})
	if err != nil {
		logf("list summaries: %v", err)
		return err
	}
	start := tr.Start.Truncate(time.Second)
	for _, m := range recent {
		if m.Project == project && !m.Created.Before(start) {
			logf("skip: manual summary #%d exists", m.ID)
			return nil
		}
	}

	date := tr.Start
	if date.IsZero() {
		date = time.Now()
	}
	out, err := deps.Run(ctx, BuildPrompt(project, date.Local(), tr.Text))
	if err != nil {
		logf("runner: %v", err)
		return err
	}
	out = strings.TrimSpace(out)
	if out == "" || strings.HasPrefix(out, skipToken) {
		logf("skip: model returned %q", out)
		return nil
	}

	id, err := deps.Store.Store(&db.Memory{Type: "summary", Content: out, Tags: AutoTag, Project: project})
	if err != nil {
		logf("store: %v", err)
		return err
	}
	logf("stored #%d", id)
	return nil
}

// ClaudeRunner 以 claude -p（haiku、不保存 session）產生摘要，並設定防遞迴環境變數。
func ClaudeRunner(ctx context.Context, prompt string) (string, error) {
	cmd := exec.CommandContext(ctx, "claude", "-p", "--model", "haiku", "--no-session-persistence", "--tools", "", "--strict-mcp-config")
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Env = append(os.Environ(), hook.SummarizingEnv+"=1")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("claude -p: %w", err)
	}
	return string(out), nil
}
