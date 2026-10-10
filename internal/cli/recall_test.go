package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"memory-mcp/internal/db"
	"memory-mcp/internal/hook"
	"memory-mcp/internal/obnote"
)

type recallEnv struct {
	open func() (*db.DB, error)
	work string // 登記在前綴對照表的工作目錄
	free string // 未登記的目錄
}

func setupRecall(t *testing.T) recallEnv {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", filepath.Join(dir, "tmp"))
	t.Setenv("HOME", dir)
	cfg := filepath.Join(dir, "projects.json")
	os.WriteFile(cfg, []byte(`{"prefixes": {"~/work": "work"}}`), 0o644)
	t.Setenv(hook.ProjectsEnv, cfg)
	t.Setenv(obnote.RootEnv, filepath.Join(dir, "no-vault"))
	env := recallEnv{work: filepath.Join(dir, "work"), free: filepath.Join(dir, "free")}
	os.MkdirAll(env.work, 0o755)
	os.MkdirAll(env.free, 0o755)
	os.MkdirAll(filepath.Join(dir, "tmp"), 0o755)

	path := filepath.Join(dir, "m.db")
	d, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	mems := []db.Memory{
		{Type: "knowledge", Project: "work", Content: "305-qa 年度報稅匯出卡在上傳中，訪客量暴增導致查詢逾時"},
		{Type: "summary", Project: "personal-hobby", Content: "積分表第一週點數已填進積分紀錄"},
		// 次名分數約第一名 55%：首次不過比例門檻，#1 去重後也不能浮上來
		{Type: "til", Project: "work", Content: "201-ds 客服單：分層名單與玩家導出 卡住，另見同日處理紀錄"},
	}
	for i := 0; i < 1200; i++ {
		mems = append(mems, db.Memory{Type: "til", Project: "noise", Content: fmt.Sprintf("無關的填充內容第 %d 筆", i)})
	}
	if _, err := d.ImportBatch(mems); err != nil {
		t.Fatal(err)
	}
	d.Close()
	env.open = func() (*db.DB, error) { return db.Open(path) }
	return env
}

func hookJSON(session, cwd, prompt string) string {
	return fmt.Sprintf(`{"session_id":%q,"cwd":%q,"prompt":%q,"hook_event_name":"UserPromptSubmit"}`, session, cwd, prompt)
}

func runRecall(env recallEnv, input string, timeout time.Duration) (string, string) {
	var out, errw bytes.Buffer
	runRecallHook(strings.NewReader(input), &out, &errw, timeout, env.open)
	return out.String(), errw.String()
}

func TestRecallHookOutputAndDedupe(t *testing.T) {
	env := setupRecall(t)
	in := hookJSON("sess-1", env.work, "305-qa 年度報稅匯出又卡住了")
	out, errs := runRecall(env, in, time.Second)
	if !strings.HasPrefix(out, recallHeader) || !strings.Contains(out, "- #1 knowledge work ") || strings.Contains(out, "- #3 ") {
		t.Fatalf("unexpected output %q (stderr %q)", out, errs)
	}
	if out2, _ := runRecall(env, in, time.Second); out2 != "" {
		t.Errorf("second run should not re-inject, got %q", out2)
	}
	if out3, _ := runRecall(env, hookJSON("sess-2", env.work, "305-qa 年度報稅匯出又卡住了"), time.Second); out3 == "" {
		t.Error("another session should still get the memory")
	}
}

func TestRecallHookSkipsContextInjected(t *testing.T) {
	env := setupRecall(t)
	hook.AddInjected("sess-ctx", contextIDs("## Memory context\n\n### x\n- #1 knowledge 2026-10-10 305-qa\n"))
	if out, _ := runRecall(env, hookJSON("sess-ctx", env.work, "305-qa 年度報稅匯出又卡住了"), time.Second); out != "" {
		t.Errorf("id injected by SessionStart should be skipped, got %q", out)
	}
}

func TestRecallHookPersonalScope(t *testing.T) {
	env := setupRecall(t)
	prompt := "積分表這週點數怎麼算"
	if out, _ := runRecall(env, hookJSON("a", env.work, prompt), time.Second); strings.Contains(out, "personal-") {
		t.Errorf("registered project should exclude personal-*, got %q", out)
	}
	if out, _ := runRecall(env, hookJSON("b", env.free, prompt), time.Second); !strings.Contains(out, "personal-hobby") {
		t.Errorf("unregistered project should include personal-*, got %q", out)
	}
}

func TestRecallHookSilentCases(t *testing.T) {
	env := setupRecall(t)
	cases := map[string]string{
		"short":    hookJSON("s", env.work, " 好 "),
		"slash":    hookJSON("s", env.work, "/mcp 305-qa 年度報稅匯出"),
		"bad json": `{"prompt":`,
		"no hit":   hookJSON("s", env.work, "今天天氣如何"),
	}
	for name, in := range cases {
		out, errs := runRecall(env, in, time.Second)
		if out != "" {
			t.Errorf("%s: stdout should be empty, got %q", name, out)
		}
		if name == "bad json" && strings.Count(errs, "\n") != 1 {
			t.Errorf("bad json: want one stderr line, got %q", errs)
		}
	}
}

func TestRecallHookTimeout(t *testing.T) {
	env := setupRecall(t)
	slow := func() (*db.DB, error) {
		time.Sleep(2 * time.Second)
		return env.open()
	}
	start := time.Now()
	var out, errw bytes.Buffer
	runRecallHook(strings.NewReader(hookJSON("t", env.work, "305-qa 年度報稅匯出又卡住了")), &out, &errw, 100*time.Millisecond, slow)
	if el := time.Since(start); el > time.Second {
		t.Errorf("should return at deadline, took %s", el)
	}
	if out.Len() != 0 || !strings.Contains(errw.String(), "timeout") {
		t.Errorf("stdout=%q stderr=%q", out.String(), errw.String())
	}
}

func TestContextIDs(t *testing.T) {
	out := "## Memory context（project: x）\n\n### 全域 feedback\n- #12 feedback 2026-10-10 a\n- #7 til 2026-10-10 b - #9 not line start\n"
	if got := contextIDs(out); !reflect.DeepEqual(got, []int64{12, 7}) {
		t.Errorf("got %v", got)
	}
}

func setupVault(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	for _, f := range []string{"追劇/雲夢記.md", "社團/積分表/規則說明.md"} {
		os.MkdirAll(filepath.Join(root, filepath.Dir(f)), 0o755)
		os.WriteFile(filepath.Join(root, f), []byte("x"), 0o644)
	}
	t.Setenv(obnote.RootEnv, root)
}

func TestRecallHookNotes(t *testing.T) {
	env := setupRecall(t)
	setupVault(t)
	vault := os.Getenv(obnote.RootEnv)
	note := "- 筆記 " + vault + "/追劇/雲夢記.md\n"

	out, errs := runRecall(env, hookJSON("n1", env.free, "雲夢記看到第幾季了"), time.Second)
	if out != recallHeader+note {
		t.Fatalf("unregistered: got %q (stderr %q)", out, errs)
	}
	if out2, _ := runRecall(env, hookJSON("n1", env.free, "雲夢記看到第幾季了"), time.Second); out2 != "" {
		t.Errorf("same session should not re-inject note, got %q", out2)
	}
	if out3, _ := runRecall(env, hookJSON("n2", env.work, "雲夢記看到第幾季了"), time.Second); out3 != "" {
		t.Errorf("registered project must not match notes, got %q", out3)
	}
	// 記憶行在前、筆記行在後
	out4, _ := runRecall(env, hookJSON("n3", env.free, "積分表這週點數怎麼算"), time.Second)
	mi, ni := strings.Index(out4, "- #2 "), strings.Index(out4, "- 筆記 "+vault+"/社團/積分表/規則說明.md")
	if mi < 0 || ni < 0 || mi > ni {
		t.Errorf("want memory line before note line, got %q", out4)
	}
}

func TestRecallNotesDeadline(t *testing.T) {
	setupRecall(t)
	setupVault(t)
	// 剩餘時間不到 recallNoteMargin：放棄走訪，只回錯誤（記憶結果不受影響）
	ctx, cancel := context.WithTimeout(context.Background(), recallNoteMargin/2)
	defer cancel()
	if notes, err := recallNotes(ctx, "雲夢記看到第幾季了", nil, "d"); err == nil || notes != nil {
		t.Errorf("notes=%v err=%v, want deadline error", notes, err)
	}
}

type panicWriter struct{}

func (panicWriter) Write([]byte) (int, error) { panic("injected stdout panic") }

// TestRecallHookPanicHelper 只在子程序中執行：依模式注入 panic 後呼叫 runRecallHook。
func TestRecallHookPanicHelper(t *testing.T) {
	mode := os.Getenv("RECALL_PANIC_HELPER")
	if mode == "" {
		t.Skip("subprocess helper")
	}
	env := setupRecall(t)
	in := strings.NewReader(hookJSON("p", env.work, "305-qa 年度報稅匯出又卡住了"))
	switch mode {
	case "worker": // 查詢 goroutine panic
		runRecallHook(in, os.Stdout, os.Stderr, time.Second, func() (*db.DB, error) { panic("injected worker panic") })
	case "main": // 進入點所在 goroutine panic（寫 stdout 時）
		runRecallHook(in, panicWriter{}, os.Stderr, time.Second, env.open)
	}
	os.Exit(0)
}

func TestRecallHookPanicExitCode(t *testing.T) {
	for _, mode := range []string{"worker", "main"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestRecallHookPanicHelper$", "-test.count=1")
		cmd.Env = append(os.Environ(), "RECALL_PANIC_HELPER="+mode)
		var so, se bytes.Buffer
		cmd.Stdout, cmd.Stderr = &so, &se
		err := cmd.Run()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		if code != 0 || so.Len() != 0 || !strings.Contains(se.String(), "memory-mcp recall: panic: injected") {
			t.Errorf("%s: exit=%d stdout=%q stderr=%q", mode, code, so.String(), se.String())
		}
	}
}

func TestRecallHookNotesSurviveDBTimeout(t *testing.T) {
	env := setupRecall(t)
	setupVault(t)
	slow := func() (*db.DB, error) {
		time.Sleep(2 * time.Second)
		return env.open()
	}
	var out, errw bytes.Buffer
	runRecallHook(strings.NewReader(hookJSON("n", env.free, "雲夢記看到第幾季了")), &out, &errw, 200*time.Millisecond, slow)
	want := recallHeader + "- 筆記 " + os.Getenv(obnote.RootEnv) + "/追劇/雲夢記.md\n"
	if out.String() != want || !strings.Contains(errw.String(), "timeout") {
		t.Errorf("stdout=%q stderr=%q", out.String(), errw.String())
	}
}

func TestRecallPromptCap(t *testing.T) {
	question := "305-qa 年度報稅匯出又卡住了"
	var log strings.Builder
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&log, "2026-10-10 12:%02d:%02d ERROR [exec-%d] com.example.ReportExportService taskId=%d java.sql.SQLTimeoutException\n", i%60, i*7%60, i%200, i*37)
	}
	cases := map[string]string{
		"question first": question + "\n" + log.String(),
		"question last":  log.String() + question,
	}
	env := setupRecall(t)
	for name, long := range cases {
		if n := utf8.RuneCountInString(first(recallPrompt(long))); n != recallMaxPromptRunes+1 {
			t.Errorf("%s: capped runes = %d, want %d", name, n, recallMaxPromptRunes+1)
		}
		start := time.Now()
		out, errs := runRecall(env, hookJSON("long-"+name, env.work, long), time.Second)
		if el := time.Since(start); el > 100*time.Millisecond {
			t.Errorf("%s: took %s", name, el)
		}
		if !strings.Contains(out, "- #1 ") {
			t.Errorf("%s: should hit #1: %q %q", name, out, errs)
		}
	}
	// 頭尾之間以空白分隔，不黏成假的窗口
	if got, _ := recallPrompt(strings.Repeat("甲", 300) + strings.Repeat("乙", 300)); !strings.Contains(got, "甲 乙") {
		t.Errorf("head and tail should be separated by a space")
	}
}

func first(s string, _ []string) string { return s }

// diverseLog 每行不同的 UUID 片段、類別名、taskId，讓任一段 250 字都切得出遠多於 24 個不同詞。
func diverseLog(lines int) string {
	var b strings.Builder
	for i := 0; i < lines; i++ {
		fmt.Fprintf(&b, "%08x Svc%d t%d\n", uint32(i)*2654435761, i, 100000+i*7919)
	}
	return b.String()
}

func TestRecallPromptDiverseHeadQuestionLast(t *testing.T) {
	log := diverseLog(3000)
	prompt, parts := recallPrompt(log + "305-qa 年度報稅匯出又卡住了")
	// 前提：頭段每行 3 個不同詞、至少 9 行完整 → 超過 24 個；尾段在提問前也塞滿 log 詞
	if len(parts) != 2 || strings.Count(parts[0], "\n") < 9 || len(db.RecallTerms(parts[0])) != 24 || len(db.RecallTerms(parts[1])) != 24 {
		t.Fatalf("fixture precondition failed: head lines=%d", strings.Count(parts[0], "\n"))
	}
	terms := db.NewPromptTerms(prompt, parts...)
	if s, _, _ := terms.Coverage("305-qa 年度報稅匯出"); s == 0 {
		t.Errorf("question terms dropped from query")
	}
	env := setupRecall(t)
	start := time.Now()
	out, errs := runRecall(env, hookJSON("diverse", env.work, log+"305-qa 年度報稅匯出又卡住了"), time.Second)
	if el := time.Since(start); el > 100*time.Millisecond {
		t.Errorf("took %s", el)
	}
	if !strings.Contains(out, "- #1 ") {
		t.Errorf("should hit #1: %q %q", out, errs)
	}
}
