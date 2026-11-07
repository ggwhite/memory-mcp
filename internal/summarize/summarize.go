package summarize

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ggwhite/memory-mcp/internal/db"
	"github.com/ggwhite/memory-mcp/internal/hook"
	"github.com/ggwhite/memory-mcp/internal/llm"
)

const (
	MaxTranscriptRunes = 60000
	MinUserTurns       = 3
	AutoTag            = "auto-summary"
	skipToken          = "SKIP"
	// summaryMaxTokens 600 字繁中摘要約 1200 token，留餘裕。
	summaryMaxTokens = 2048
)

// Deps Run 的外部依賴，測試時可替換。LLM 為 nil 表示沒有設定 LLM，Run 只記 log 並跳過。
// OnStored 非 nil 時，在 summary 成功寫入後以 project 與新 id 呼叫（例如接著做 mental build）。
type Deps struct {
	Store    db.Store
	LLM      llm.Client
	Logf     func(format string, args ...any)
	OnStored func(project string, id int64)
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
	if deps.LLM == nil {
		logf("skip: LLM not configured")
		return nil
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
	// 沿用 cheap tier（claude-cli 預設 haiku），維持既有 SessionEnd 摘要的成本與速度。
	out, err := deps.LLM.Complete(ctx, llm.Request{
		Prompt:    BuildPrompt(project, date.Local(), tr.Text),
		Tier:      llm.TierCheap,
		MaxTokens: summaryMaxTokens,
	})
	if err != nil {
		logf("llm %s: %v", deps.LLM.Name(), err)
		return err
	}
	out = strings.TrimSpace(out)
	if out == "" || strings.HasPrefix(out, skipToken) {
		logf("skip: model returned %q", out)
		return nil
	}

	id, err := deps.Store.Store(&db.Memory{Type: "summary", Content: out, Tags: AutoTag, Project: project, Source: "llm:" + deps.LLM.Name()})
	if err != nil {
		logf("store: %v", err)
		return err
	}
	logf("stored #%d", id)
	if deps.OnStored != nil {
		deps.OnStored(project, id)
	}
	return nil
}
