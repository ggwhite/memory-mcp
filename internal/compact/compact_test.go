package compact

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ggwhite/memory-mcp/internal/db"
	"github.com/ggwhite/memory-mcp/internal/llm"
)

// fakeLLM 把只看 prompt 的假函式包成 llm.Client。
func fakeLLM(run func(context.Context, string) (string, error)) llm.Client {
	return llm.FuncClient{ID: "fake", Fn: func(ctx context.Context, req llm.Request) (string, error) {
		return run(ctx, req.Prompt)
	}}
}

func testDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// put 以指定 created 寫入一筆記憶（StoreMerged 不帶 ids 就是單純寫入）。
func put(t *testing.T, d *db.DB, typ, project, content string, created time.Time) int64 {
	t.Helper()
	id, err := d.StoreMerged(&db.Memory{Type: typ, Project: project, Content: content, Created: created}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func day(s string) time.Time {
	t, _ := time.Parse("2006-01-02T15:04:05", s)
	return t
}

type fixture struct {
	d               *db.DB
	march           []int64
	april, til      int64
	recent1, recent int64
}

// seed：p 的 2025-03 三筆（要合併）、2025-04 一筆（單筆不動）、舊 til、兩筆近期 summary。
func seed(t *testing.T) fixture {
	d := testDB(t)
	f := fixture{d: d}
	f.march = []int64{
		put(t, d, "summary", "p", "3/20 完成 B，A 已做完", day("2025-03-20T10:00:00")),
		put(t, d, "summary", "p", "3/05 做了 A，待辦：A 收尾", day("2025-03-05T09:00:00")),
		put(t, d, "summary", "p", "3/12 決定用 X，參考 #7", day("2025-03-12T12:00:00")),
	}
	f.april = put(t, d, "summary", "p", "4/01 單筆", day("2025-04-01T00:00:00"))
	f.til = put(t, d, "til", "p", "舊 til", day("2025-03-07T00:00:00"))
	f.recent1 = put(t, d, "summary", "p", "近期 1", time.Now().UTC().AddDate(0, 0, -1))
	f.recent = put(t, d, "summary", "p", "近期 2", time.Now().UTC().AddDate(0, 0, -2))
	return f
}

func TestPlanGroups(t *testing.T) {
	f := seed(t)
	put(t, f.d, "summary", "q", "q 3 月一", day("2025-03-01T00:00:00"))
	put(t, f.d, "summary", "q", "q 3 月二", day("2025-03-02T00:00:00"))

	groups, err := Plan(f.d, "60d", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 || groups[0].Project != "p" || groups[0].Month != "2025-03" || groups[1].Project != "q" {
		t.Fatalf("groups = %+v", groups)
	}
	want := []int64{f.march[1], f.march[2], f.march[0]} // created 由舊到新
	if got := groups[0].IDs(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("ids = %v, want %v", got, want)
	}
	if groups[0].Chars() == 0 {
		t.Error("chars should be counted")
	}

	groups, _ = Plan(f.d, "60d", "q")
	if len(groups) != 1 || groups[0].Project != "q" {
		t.Errorf("project filter: %+v", groups)
	}
}

func TestApplyMergesGroup(t *testing.T) {
	f := seed(t)
	groups, _ := Plan(f.d, "60d", "")
	var prompts []string
	run := func(_ context.Context, prompt string) (string, error) {
		prompts = append(prompts, prompt)
		return "  合併後的內容 #7  \n", nil
	}
	outcomes := Apply(context.Background(), f.d, groups, Deps{LLM: fakeLLM(run)})
	if len(outcomes) != 1 || outcomes[0].Err != nil || outcomes[0].NewID == 0 {
		t.Fatalf("outcomes = %+v", outcomes)
	}
	p := prompts[0]
	if !(strings.Index(p, "3/05") < strings.Index(p, "3/12") && strings.Index(p, "3/12") < strings.Index(p, "3/20")) {
		t.Errorf("prompt inputs should be sorted ascending:\n%s", p)
	}
	if !strings.Contains(p, fmt.Sprintf("#%d（2025-03-05）", f.march[1])) {
		t.Errorf("prompt should prefix id and date:\n%s", p)
	}

	m, err := f.d.Get(outcomes[0].NewID)
	if err != nil {
		t.Fatal(err)
	}
	if m.Type != "summary" || m.Project != "p" || m.Tags != "compacted,month:2025-03" || m.Source != "compact" {
		t.Errorf("new memory = %+v", m)
	}
	if !m.Created.Equal(day("2025-03-20T10:00:00")) {
		t.Errorf("created = %v, want newest original's created", m.Created)
	}
	header := fmt.Sprintf("2025-03 p 月度彙整（合併 3 筆：#%d #%d #%d）\n合併後的內容 #7", f.march[1], f.march[2], f.march[0])
	if m.Content != header {
		t.Errorf("content = %q, want %q", m.Content, header)
	}
	if got, _ := f.d.GetMany(f.march); len(got) != 0 {
		t.Errorf("originals should be deleted: %+v", got)
	}
	if got, _ := f.d.GetMany([]int64{f.april, f.til, f.recent1, f.recent}); len(got) != 4 {
		t.Errorf("single-row group, til and recent rows must stay: %+v", got)
	}
}

func TestApplyRunnerFailureKeepsRows(t *testing.T) {
	for name, run := range map[string]func(context.Context, string) (string, error){
		"error": func(context.Context, string) (string, error) { return "", fmt.Errorf("claude down") },
		"empty": func(context.Context, string) (string, error) { return " \n", nil },
	} {
		t.Run(name, func(t *testing.T) {
			f := seed(t)
			groups, _ := Plan(f.d, "60d", "")
			var logged []string
			outcomes := Apply(context.Background(), f.d, groups, Deps{LLM: fakeLLM(run), Logf: func(format string, args ...any) {
				logged = append(logged, fmt.Sprintf(format, args...))
			}})
			if len(outcomes) != 1 || outcomes[0].Err == nil || outcomes[0].NewID != 0 {
				t.Fatalf("outcomes = %+v", outcomes)
			}
			if len(logged) == 0 {
				t.Error("skipped group should be logged")
			}
			if got, _ := f.d.GetMany(f.march); len(got) != 3 {
				t.Errorf("nothing should be deleted: %+v", got)
			}
		})
	}
}

func TestApplyChunksLargeGroup(t *testing.T) {
	f := seed(t)
	groups, _ := Plan(f.d, "60d", "")
	var calls int
	run := func(_ context.Context, prompt string) (string, error) {
		calls++
		return fmt.Sprintf("段%d", calls), nil
	}
	// 每筆輸入約 25 字，上限 40 字 → 三筆各成一段，再合併三段結果
	outcomes := Apply(context.Background(), f.d, groups, Deps{LLM: fakeLLM(run), MaxInputRunes: 40})
	if outcomes[0].Err != nil {
		t.Fatal(outcomes[0].Err)
	}
	if calls != 4 {
		t.Errorf("runner calls = %d, want 3 chunks + 1 final", calls)
	}
	m, _ := f.d.Get(outcomes[0].NewID)
	if !strings.HasSuffix(m.Content, "\n段4") {
		t.Errorf("content = %q, want final roll-up result", m.Content)
	}
}

// 分段結果每段都超過上限時，不能無限遞迴，要直接一次合併。
func TestApplyChunksOversizeResults(t *testing.T) {
	f := seed(t)
	groups, _ := Plan(f.d, "60d", "")
	var calls int
	run := func(context.Context, string) (string, error) {
		calls++
		return strings.Repeat("長", 50), nil
	}
	outcomes := Apply(context.Background(), f.d, groups, Deps{LLM: fakeLLM(run), MaxInputRunes: 40})
	if outcomes[0].Err != nil || calls != 4 {
		t.Errorf("err=%v calls=%d, want 3 chunks + 1 final", outcomes[0].Err, calls)
	}
}

func TestWriteBackup(t *testing.T) {
	f := seed(t)
	groups, _ := Plan(f.d, "60d", "")
	dir := filepath.Join(t.TempDir(), "backups")
	path, err := WriteBackup(dir, groups, time.Date(2026, 11, 2, 9, 8, 7, 0, time.Local))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "compact-20261102-090807.json" {
		t.Errorf("path = %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got []db.Memory
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].ID != f.march[1] || got[0].Content == "" {
		t.Errorf("backup = %+v", got)
	}
}

// 同一秒內連續備份不能互相覆蓋：沿用原檔名，撞名時加序號。
func TestWriteBackupSameSecondDoesNotOverwrite(t *testing.T) {
	f := seed(t)
	groups, _ := Plan(f.d, "60d", "")
	dir := filepath.Join(t.TempDir(), "backups")
	now := time.Date(2026, 11, 2, 9, 8, 7, 0, time.Local)
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		path, err := WriteBackup(dir, groups, now)
		if err != nil {
			t.Fatal(err)
		}
		if seen[path] {
			t.Fatalf("path %s reused", path)
		}
		seen[path] = true
	}
	files, _ := filepath.Glob(filepath.Join(dir, "compact-*.json"))
	if len(files) != 3 {
		t.Errorf("backups = %v, want 3 files", files)
	}
}
