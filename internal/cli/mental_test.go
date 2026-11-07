package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ggwhite/memory-mcp/internal/db"
	"github.com/ggwhite/memory-mcp/internal/llm"
)

// seedMentalDB 在 project demo 建立兩筆記憶並回傳 DB 路徑。
func seedMentalDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "m.db")
	d, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, c := range []string{"deploy uses helm", "deploy after tests"} {
		if _, err := d.Store(&db.Memory{Type: "til", Project: "demo", Content: c}); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// clearMentalFlags cobra 旗標值會跨 Execute 保留，同一測試內連續呼叫前要先清掉。
func clearMentalFlags() {
	mentalAddCmd.Flags().Set("project", "")
	mentalListCmd.Flags().Set("project", "")
	mentalBuildCmd.Flags().Set("project", "")
	mentalBuildCmd.Flags().Set("all", "false")
	mentalBuildCmd.Flags().Set("force", "false")
	remoteFlag = ""
	jsonFlag = false
}

// runMental 清掉 mental 旗標後執行 CLI 並回傳 stdout。
func runMental(t *testing.T, path string, args ...string) (string, error) {
	t.Helper()
	clearMentalFlags()
	return runCLIOut(t, path, args...)
}

// resetMentalFlags 預設把 mentalLLM 換成「沒有 LLM」，測試結束時還原旗標與全域狀態。
func resetMentalFlags(t *testing.T) {
	orig := mentalLLM
	t.Cleanup(func() {
		clearMentalFlags()
		mentalLLM = orig
	})
	mentalLLM = func(*db.DB) (llm.Client, error) { return nil, nil }
}

func useMentalLLM(fn func(req llm.Request) (string, error)) *int {
	calls := 0
	mentalLLM = func(*db.DB) (llm.Client, error) {
		return llm.FuncClient{ID: "fake/m", Fn: func(_ context.Context, req llm.Request) (string, error) {
			calls++
			return fn(req)
		}}, nil
	}
	return &calls
}

func TestMentalAddListRm(t *testing.T) {
	resetMentalFlags(t)
	path := seedMentalDB(t)
	out, err := runMental(t, path, "mental", "add", "--project", "demo", "怎麼部署？")
	if err != nil || !strings.Contains(out, "m1") {
		t.Fatalf("add: %q %v", out, err)
	}
	if _, err := runMental(t, path, "mental", "add", "怎麼部署？"); err == nil {
		t.Error("add without --project should fail")
	}
	out, err = runMental(t, path, "mental", "list", "--project", "demo")
	if err != nil || !strings.Contains(out, "m1 demo") || !strings.Contains(out, "怎麼部署？") || !strings.Contains(out, "(not built)") {
		t.Fatalf("list: %q %v", out, err)
	}
	out, _ = runMental(t, path, "mental", "list", "--project", "nope")
	if out != "" {
		t.Errorf("list other project = %q", out)
	}
	if out, err = runMental(t, path, "mental", "rm", "m1"); err != nil || !strings.Contains(out, "Removed m1") {
		t.Fatalf("rm: %q %v", out, err)
	}
	if _, err := runMental(t, path, "mental", "rm", "1"); err == nil {
		t.Error("rm missing should fail")
	}
	if _, err := runMental(t, path, "mental", "rm", "mx"); err == nil {
		t.Error("rm invalid id should fail")
	}
}

func TestMentalListJSON(t *testing.T) {
	resetMentalFlags(t)
	path := seedMentalDB(t)
	runMental(t, path, "mental", "add", "--project", "demo", "q")
	r, w, _ := os.Pipe()
	orig := os.Stdout
	os.Stdout = w
	clearMentalFlags()
	err := runCLI(t, path, "mental", "list", "--json")
	w.Close()
	os.Stdout = orig
	if err != nil {
		t.Fatal(err)
	}
	var got []db.MentalModel
	if err := json.NewDecoder(r).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Question != "q" || got[0].Project != "demo" {
		t.Errorf("json = %+v", got)
	}
}

func TestMentalBuildWithoutLLM(t *testing.T) {
	resetMentalFlags(t)
	path := seedMentalDB(t)
	runMental(t, path, "mental", "add", "--project", "demo", "deploy")
	out, err := runMental(t, path, "mental", "build", "--project", "demo")
	if err != nil || !strings.Contains(out, "LLM not configured") {
		t.Fatalf("build: %q %v", out, err)
	}
}

func TestMentalBuildFakeLLM(t *testing.T) {
	resetMentalFlags(t)
	path := seedMentalDB(t)
	runMental(t, path, "mental", "add", "--project", "demo", "deploy")
	calls := useMentalLLM(func(req llm.Request) (string, error) {
		if req.Tier != llm.TierSynth {
			t.Errorf("tier = %v", req.Tier)
		}
		return "Use helm #1", nil
	})
	out, err := runMental(t, path, "mental", "build", "--project", "demo")
	if err != nil || !strings.Contains(out, "built m1") || *calls != 1 {
		t.Fatalf("build: %q %v calls=%d", out, err, *calls)
	}
	out, err = runMental(t, path, "mental", "build", "--all")
	if err != nil || !strings.Contains(out, "unchanged m1") || *calls != 1 {
		t.Fatalf("rebuild should skip: %q %v calls=%d", out, err, *calls)
	}
	out, err = runMental(t, path, "mental", "build", "--project", "demo", "--force")
	if err != nil || !strings.Contains(out, "built m1") || *calls != 2 {
		t.Fatalf("force: %q %v calls=%d", out, err, *calls)
	}
	if !strings.Contains(projectContextOf(t, path, "demo"), "### demo mental models\n**Q:** deploy\nUse helm #1\n") {
		t.Errorf("context missing mental model:\n%s", projectContextOf(t, path, "demo"))
	}
	if _, err := runMental(t, path, "mental", "build"); err == nil {
		t.Error("build without --project / --all should fail")
	}
}

func TestMentalBuildFailures(t *testing.T) {
	resetMentalFlags(t)
	path := seedMentalDB(t)
	runMental(t, path, "mental", "add", "--project", "demo", "deploy")
	runMental(t, path, "mental", "add", "--project", "demo", "tests")
	useMentalLLM(func(req llm.Request) (string, error) {
		if strings.Contains(req.Prompt, "問題：deploy") {
			return "", llm.ErrBudget
		}
		return "ok #2", nil
	})
	out, err := runMental(t, path, "mental", "build", "--project", "demo")
	if err != nil || !strings.Contains(out, "skipped m1: "+llm.ErrBudget.Error()) || !strings.Contains(out, "built m2") {
		t.Fatalf("partial failure should exit 0: %q %v", out, err)
	}

	useMentalLLM(func(llm.Request) (string, error) { return "", errors.New("boom") })
	out, err = runMental(t, path, "mental", "build", "--project", "demo", "--force")
	if err == nil || !strings.Contains(out, "skipped m1: boom") || !strings.Contains(out, "skipped m2: boom") {
		t.Fatalf("all failed should exit non-zero: %q %v", out, err)
	}
	// 失敗不覆蓋舊答案。
	d, _ := db.Open(path)
	defer d.Close()
	ms, _ := d.ListMentalModels(db.MentalModelFilter{Project: "demo"})
	if ms[0].Answer != "" || ms[1].Answer != "ok #2" {
		t.Errorf("answers = %q / %q", ms[0].Answer, ms[1].Answer)
	}
}

func TestMentalRefusesRemote(t *testing.T) {
	resetMentalFlags(t)
	path := seedMentalDB(t)
	for _, args := range [][]string{
		{"mental", "add", "--project", "demo", "q"},
		{"mental", "list"},
		{"mental", "rm", "1"},
		{"mental", "build", "--all"},
	} {
		clearMentalFlags()
		err := runCLI(t, path, append(args, "--remote", "http://127.0.0.1:1")...)
		if err == nil || !strings.Contains(err.Error(), "--remote") {
			t.Errorf("%v: want --remote refusal, got %v", args, err)
		}
	}
}

const workerTranscript = `{"type":"user","timestamp":"2000-01-01T01:00:01.000Z","message":{"role":"user","content":"第一個問題"}}
{"type":"user","timestamp":"2000-01-01T01:00:02.000Z","message":{"role":"user","content":"第二個問題"}}
{"type":"user","timestamp":"2000-01-01T01:00:03.000Z","message":{"role":"user","content":"第三個問題"}}
`

// runWorker 用假 transcript 跑一次 SessionEnd worker（project = globex），回傳各 tier 的呼叫次數。
func runWorker(t *testing.T, path string) map[llm.Tier]int {
	t.Helper()
	t.Setenv("MEMORY_MCP_OLLAMA_URL", "http://127.0.0.1:1")
	t.Setenv("MEMORY_MCP_REMOTE", "")
	dir := t.TempDir()
	transcript := filepath.Join(dir, "t.jsonl")
	os.WriteFile(transcript, []byte(workerTranscript), 0o644)
	input, _ := json.Marshal(map[string]string{"session_id": "s", "transcript_path": transcript, "cwd": "/x/Globex"})
	file := filepath.Join(dir, "in.json")
	os.WriteFile(file, input, 0o644)

	calls := make(map[llm.Tier]int)
	orig := summarizeLLM
	t.Cleanup(func() { summarizeLLM = orig; dbPath = "" })
	summarizeLLM = func(*db.DB) (llm.Client, error) {
		return llm.FuncClient{ID: "fake/m", Fn: func(_ context.Context, req llm.Request) (string, error) {
			calls[req.Tier]++
			if req.Tier == llm.TierSynth {
				return "answer #1", nil
			}
			return "2026-11-07 summary of work", nil
		}}, nil
	}
	dbPath = path
	runSummarizeWorker(file)
	return calls
}

func TestSummarizeWorkerBuildsMentalModels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.db")
	d, _ := db.Open(path)
	d.Store(&db.Memory{Type: "til", Project: "globex", Content: "old fact"})
	d.AddMentalModel("globex", "work")
	d.AddMentalModel("other", "work")
	d.Close()

	calls := runWorker(t, path)
	if calls[llm.TierCheap] != 1 || calls[llm.TierSynth] != 1 {
		t.Fatalf("calls = %v, want 1 summary + 1 mental build", calls)
	}
	d, _ = db.Open(path)
	defer d.Close()
	ms, _ := d.ListMentalModels(db.MentalModelFilter{AllProjects: true})
	if ms[0].Project != "globex" || ms[0].Answer != "answer #1" || ms[0].BuiltFromMaxID != 2 || ms[1].Answer != "" {
		t.Errorf("models = %+v", ms)
	}
	log, _ := os.ReadFile(filepath.Join(filepath.Dir(path), "summarize.log"))
	if !strings.Contains(string(log), "mental built m1") {
		t.Errorf("log missing mental build:\n%s", log)
	}
}

func TestSummarizeWorkerWithoutMentalModels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.db")
	d, _ := db.Open(path)
	d.AddMentalModel("other", "work")
	d.Close()
	calls := runWorker(t, path)
	if calls[llm.TierCheap] != 1 || calls[llm.TierSynth] != 0 {
		t.Fatalf("calls = %v, want summary only", calls)
	}
}
