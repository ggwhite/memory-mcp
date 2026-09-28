package summarize

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"memory-mcp/internal/db"
	"memory-mcp/internal/hook"
)

const fixture = `{"type":"mode","sessionId":"s"}
{"type":"user","isMeta":true,"timestamp":"2000-01-01T01:00:00.000Z","message":{"role":"user","content":"<local-command-caveat>ignore</local-command-caveat>"}}
{"type":"user","timestamp":"2000-01-01T01:00:01.000Z","message":{"role":"user","content":"第一個問題"}}
{"type":"assistant","timestamp":"2000-01-01T01:00:02.000Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"secret thoughts"},{"type":"text","text":"第一個回答"}]}}
{"type":"assistant","timestamp":"2000-01-01T01:00:03.000Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{}}]}}
{"type":"user","timestamp":"2000-01-01T01:00:04.000Z","message":{"role":"user","content":[{"type":"tool_result","content":"TOOL OUTPUT"}]}}
{"type":"user","timestamp":"2000-01-01T01:00:05.000Z","message":{"role":"user","content":[{"type":"text","text":"第二個問題"}]}}
{"type":"user","isSidechain":true,"timestamp":"2000-01-01T01:00:06.000Z","message":{"role":"user","content":"subagent prompt"}}
{"type":"user","timestamp":"2000-01-01T01:00:07.000Z","message":{"role":"user","content":"第三個問題"}}
`

func TestParseTranscript(t *testing.T) {
	tr, err := ParseTranscript(strings.NewReader(fixture), MaxTranscriptRunes)
	if err != nil {
		t.Fatal(err)
	}
	if tr.UserTurns != 3 {
		t.Errorf("UserTurns = %d, want 3", tr.UserTurns)
	}
	want := time.Date(2000, 1, 1, 1, 0, 0, 0, time.UTC)
	if !tr.Start.Equal(want) {
		t.Errorf("Start = %v, want %v", tr.Start, want)
	}
	for _, s := range []string{"[user] 第一個問題", "[assistant] 第一個回答", "[user] 第二個問題", "[user] 第三個問題"} {
		if !strings.Contains(tr.Text, s) {
			t.Errorf("missing %q in %q", s, tr.Text)
		}
	}
	for _, s := range []string{"TOOL OUTPUT", "secret thoughts", "ignore", "subagent prompt"} {
		if strings.Contains(tr.Text, s) {
			t.Errorf("unexpected %q in %q", s, tr.Text)
		}
	}
}

func TestParseTranscriptTruncatesFromFront(t *testing.T) {
	tr, _ := ParseTranscript(strings.NewReader(fixture), 10)
	if n := len([]rune(tr.Text)); n != 10 {
		t.Errorf("runes = %d, want 10", n)
	}
	if !strings.HasSuffix(tr.Text, "第三個問題") {
		t.Errorf("should keep tail, got %q", tr.Text)
	}
}

func setup(t *testing.T, transcript string) (hook.Input, *db.DB) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "t.jsonl")
	if err := os.WriteFile(path, []byte(transcript), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(dir, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return hook.Input{SessionID: "s", TranscriptPath: path, Cwd: "/x/Kairos"}, d
}

func deps(d db.Store, out string, called *int) Deps {
	return Deps{
		Store: d,
		Run: func(_ context.Context, prompt string) (string, error) {
			*called++
			return out, nil
		},
		Logf: func(string, ...any) {},
	}
}

func TestRunStoresSummary(t *testing.T) {
	in, d := setup(t, fixture)
	called := 0
	if err := Run(context.Background(), in, deps(d, "  2026-09-28 做了 X\n", &called)); err != nil {
		t.Fatal(err)
	}
	ms, _ := d.List(db.ListOptions{Type: "summary"})
	if called != 1 || len(ms) != 1 {
		t.Fatalf("called=%d stored=%d", called, len(ms))
	}
	if ms[0].Project != "kairos" || ms[0].Tags != AutoTag || ms[0].Content != "2026-09-28 做了 X" {
		t.Errorf("got %+v", ms[0])
	}
}

func TestRunSkipsShortSession(t *testing.T) {
	short := strings.Join(strings.Split(fixture, "\n")[:4], "\n") + "\n"
	in, d := setup(t, short)
	called := 0
	Run(context.Background(), in, deps(d, "x", &called))
	if called != 0 {
		t.Error("runner should not be called for short session")
	}
}

func TestRunSkipsWhenManualSummaryExists(t *testing.T) {
	in, d := setup(t, fixture)
	d.Store(&db.Memory{Type: "summary", Content: "manual", Project: "kairos"})
	called := 0
	Run(context.Background(), in, deps(d, "x", &called))
	if called != 0 {
		t.Error("runner should not be called when manual summary exists")
	}
}

func TestRunIgnoresOlderSummary(t *testing.T) {
	old := strings.ReplaceAll(fixture, "2000-01-01", "2099-01-01")
	in, d := setup(t, old)
	d.Store(&db.Memory{Type: "summary", Content: "older", Project: "kairos"})
	called := 0
	Run(context.Background(), in, deps(d, "new summary", &called))
	if called != 1 {
		t.Error("summary stored before session start should not block")
	}
}

func TestRunSkipOutput(t *testing.T) {
	in, d := setup(t, fixture)
	called := 0
	Run(context.Background(), in, deps(d, "SKIP", &called))
	ms, _ := d.List(db.ListOptions{Type: "summary"})
	if len(ms) != 0 {
		t.Error("SKIP output should not be stored")
	}
}

func TestBuildPrompt(t *testing.T) {
	p := BuildPrompt("kairos", time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC), "[user] hi")
	for _, s := range []string{"project: kairos", "2026-09-28", "繁體中文", "SKIP", "<transcript>\n[user] hi\n</transcript>"} {
		if !strings.Contains(p, s) {
			t.Errorf("missing %q", s)
		}
	}
}
