// Package compact 把舊的 summary 依 (project, 月份) 合併成一筆月度彙整，
// 由 LLM 產生內容後，在單一 transaction 內寫入新記憶並刪除原始記憶。
package compact

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ggwhite/memory-mcp/internal/db"
	"github.com/ggwhite/memory-mcp/internal/summarize"
)

const (
	// DefaultMaxInputRunes 單次送給 LLM 的輸入上限，超過就分段彙整再合併。
	DefaultMaxInputRunes = 80000
	// Tag 合併後記憶的固定 tag，另加 month:YYYY-MM。
	Tag = "compacted"
)

// Group 同一 project、同一月份（依 created 的 UTC 年月）的 summary，Memories 依 created 由舊到新。
type Group struct {
	Project  string      `json:"project"`
	Month    string      `json:"month"`
	Memories []db.Memory `json:"-"`
}

// IDs 依 created 由舊到新列出群組內的記憶 id。
func (g Group) IDs() []int64 {
	ids := make([]int64, len(g.Memories))
	for i, m := range g.Memories {
		ids[i] = m.ID
	}
	return ids
}

// Chars 群組內所有記憶內容的字元（rune）數合計。
func (g Group) Chars() int {
	var n int
	for _, m := range g.Memories {
		n += utf8.RuneCountInString(m.Content)
	}
	return n
}

// Plan 列出 created 早於 olderThan（Nd 格式）的 summary 並依 (project, 月份) 分組，
// 只回傳至少 2 筆的群組，依 project、月份排序。唯讀，不做任何變更。
func Plan(d *db.DB, olderThan, project string) ([]Group, error) {
	memories, err := d.SummariesOlderThan(olderThan, project)
	if err != nil {
		return nil, err
	}
	byKey := make(map[[2]string]*Group)
	for _, m := range memories {
		key := [2]string{m.Project, m.Created.UTC().Format("2006-01")}
		g, ok := byKey[key]
		if !ok {
			g = &Group{Project: key[0], Month: key[1]}
			byKey[key] = g
		}
		g.Memories = append(g.Memories, m)
	}
	var out []Group
	for _, g := range byKey {
		if len(g.Memories) >= 2 {
			out = append(out, *g)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Project != out[j].Project {
			return out[i].Project < out[j].Project
		}
		return out[i].Month < out[j].Month
	})
	return out, nil
}

// WriteBackup 把所有群組內、即將被刪除的記憶以 export 相同格式寫到
// dir/compact-YYYYMMDD-HHMMSS.json（同名已存在則改 -1、-2… 後綴，絕不覆蓋），回傳檔案路徑。必須在任何變更之前呼叫。
func WriteBackup(dir string, groups []Group, now time.Time) (string, error) {
	var memories []db.Memory
	for _, g := range groups {
		memories = append(memories, g.Memories...)
	}
	data, err := json.MarshalIndent(memories, "", "  ")
	if err != nil {
		return "", fmt.Errorf("backup marshal: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("backup dir: %w", err)
	}
	// O_EXCL 保證不覆蓋既有備份：撞名時在檔名後加 -1、-2…。
	base := "compact-" + now.Format("20060102-150405")
	for i := 0; ; i++ {
		name := base + ".json"
		if i > 0 {
			name = fmt.Sprintf("%s-%d.json", base, i)
		}
		path := filepath.Join(dir, name)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("backup write: %w", err)
		}
		_, err = f.Write(append(data, '\n'))
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return "", fmt.Errorf("backup write: %w", err)
		}
		return path, nil
	}
}

// Deps Apply 的外部依賴，測試時可替換。
type Deps struct {
	Run           summarize.Runner
	Logf          func(format string, args ...any) // nil 則不記錄
	MaxInputRunes int                              // 0 用 DefaultMaxInputRunes
}

// Outcome 一個群組的合併結果；Err 非 nil 表示該群組被跳過，原始記憶未刪除。
type Outcome struct {
	Group Group
	NewID int64
	Err   error
}

// Apply 逐群組先取得 LLM 合併結果，成功才在單一 transaction 寫入新記憶並刪除原始記憶；
// LLM 失敗或回傳空白就跳過該群組（記 log），不刪任何東西。呼叫前應先 WriteBackup。
func Apply(ctx context.Context, d *db.DB, groups []Group, deps Deps) []Outcome {
	logf := deps.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	maxRunes := deps.MaxInputRunes
	if maxRunes <= 0 {
		maxRunes = DefaultMaxInputRunes
	}
	out := make([]Outcome, 0, len(groups))
	for _, g := range groups {
		o := Outcome{Group: g}
		o.NewID, o.Err = applyGroup(ctx, d, g, deps.Run, maxRunes)
		if o.Err != nil {
			logf("compact %s %s: skipped: %v", g.Project, g.Month, o.Err)
		}
		out = append(out, o)
	}
	return out
}

func applyGroup(ctx context.Context, d *db.DB, g Group, run summarize.Runner, maxRunes int) (int64, error) {
	inputs := make([]string, len(g.Memories))
	for i, m := range g.Memories {
		inputs[i] = fmt.Sprintf("#%d（%s）\n%s", m.ID, m.Created.UTC().Format("2006-01-02"), m.Content)
	}
	body, err := rollup(ctx, g, inputs, run, maxRunes)
	if err != nil {
		return 0, err
	}
	mem := &db.Memory{
		Type:    "summary",
		Project: g.Project,
		Tags:    Tag + ",month:" + g.Month,
		Content: header(g) + "\n" + body,
		Created: g.Memories[len(g.Memories)-1].Created,
	}
	return d.StoreMerged(mem, g.IDs())
}

// rollup 輸入合計不超過 maxRunes 就直接交給 LLM；否則分段各自彙整，再把各段結果彙整成一筆。
func rollup(ctx context.Context, g Group, inputs []string, run summarize.Runner, maxRunes int) (string, error) {
	chunks := splitChunks(inputs, maxRunes)
	if len(chunks) == 1 {
		return runOnce(ctx, run, BuildPrompt(g.Project, g.Month, chunks[0]))
	}
	parts := make([]string, len(chunks))
	for i, c := range chunks {
		res, err := runOnce(ctx, run, BuildPrompt(g.Project, g.Month, c))
		if err != nil {
			return "", fmt.Errorf("chunk %d/%d: %w", i+1, len(chunks), err)
		}
		parts[i] = fmt.Sprintf("第 %d 段彙整\n%s", i+1, res)
	}
	// 各段結果仍無法再併段（每段單筆就超過上限）時直接一次合併，避免無限遞迴。
	if len(splitChunks(parts, maxRunes)) >= len(parts) {
		return runOnce(ctx, run, BuildPrompt(g.Project, g.Month, parts))
	}
	return rollup(ctx, g, parts, run, maxRunes)
}

func runOnce(ctx context.Context, run summarize.Runner, prompt string) (string, error) {
	res, err := run(ctx, prompt)
	if err != nil {
		return "", err
	}
	res = strings.TrimSpace(res)
	if res == "" {
		return "", fmt.Errorf("model returned empty output")
	}
	return res, nil
}

// splitChunks 依序把輸入切成每段合計不超過 maxRunes 的分段；單筆就超過上限時自成一段。
func splitChunks(inputs []string, maxRunes int) [][]string {
	var chunks [][]string
	var cur []string
	var n int
	for _, in := range inputs {
		r := utf8.RuneCountInString(in)
		if len(cur) > 0 && n+r > maxRunes {
			chunks = append(chunks, cur)
			cur, n = nil, 0
		}
		cur = append(cur, in)
		n += r
	}
	if len(cur) > 0 {
		chunks = append(chunks, cur)
	}
	return chunks
}

// header 合併後記憶的第一行，例如「2025-03 kairos 月度彙整（合併 3 筆：#1 #2 #3）」。
func header(g Group) string {
	refs := make([]string, len(g.Memories))
	for i, m := range g.Memories {
		refs[i] = fmt.Sprintf("#%d", m.ID)
	}
	return fmt.Sprintf("%s %s 月度彙整（合併 %d 筆：%s）", g.Month, g.Project, len(g.Memories), strings.Join(refs, " "))
}

// BuildPrompt 組出把多筆 summary（已依時間由舊到新、各自帶 id 與日期）合併成一筆月度彙整的 prompt。
func BuildPrompt(project, month string, inputs []string) string {
	return fmt.Sprintf(`以下是 project %s 在 %s 的多筆工作摘要，依時間由舊到新排列，每筆開頭標有記憶 id 與日期。
請用繁體中文合併成「一筆」月度彙整，1500 字以內，包含：
1. 做了什麼
2. 關鍵決定與理由
3. 還沒做完的部分：較晚的摘要已說明完成的項目要移除，不要列為未完成
保留具體識別資訊（檔案路徑、MR 編號、指令、主機名），以及內文提到的記憶 id（例如 #123）。
不要寫出密碼、金鑰、token 的值。只輸出彙整內容本身，不要加前言或結語。

<summaries>
%s
</summaries>
`, project, month, strings.Join(inputs, "\n\n"))
}
