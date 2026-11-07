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

// seedConsolidateDB 在 project demo 建立 3 筆近似重複的 feedback 與 1 筆無關的。
func seedConsolidateDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "m.db")
	d, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, c := range []string{"回覆一律用英文", "回覆一律用英文撰寫", "一律用英文撰寫回覆內容", "部署前先跑測試"} {
		if _, err := d.Store(&db.Memory{Type: "feedback", Project: "demo", Content: c}); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func resetConsolidateFlags(t *testing.T) {
	orig := consolidateLLM
	t.Cleanup(func() {
		for _, f := range []string{"project", "type", "threshold", "cosine-threshold"} {
			consolidateCmd.Flags().Lookup(f).Value.Set(consolidateCmd.Flags().Lookup(f).DefValue)
		}
		consolidateCmd.Flags().Set("apply", "false")
		consolidateCmd.Flags().Set("all", "false")
		observationListCmd.Flags().Set("project", "")
		consolidateLLM = orig
		remoteFlag = ""
		jsonFlag = false
	})
	consolidateLLM = func(*db.DB) (llm.Client, error) { return nil, nil }
}

// runCLIOut 執行 CLI 並回傳 stdout。
func runCLIOut(t *testing.T, path string, args ...string) (string, error) {
	t.Helper()
	var out strings.Builder
	rootCmd.SetOut(&out)
	t.Cleanup(func() { rootCmd.SetOut(nil) })
	err := runCLI(t, path, args...)
	return out.String(), err
}

func observationCount(t *testing.T, path string) int {
	t.Helper()
	d, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	obs, err := d.ListObservations(db.ObservationFilter{AllProjects: true, IncludeInactive: true})
	if err != nil {
		t.Fatal(err)
	}
	return len(obs)
}

// projectContextOf 讀 project 的 context（context 指令直接寫 os.Stdout，這裡改走 DB）。
func projectContextOf(t *testing.T, path, project string) string {
	t.Helper()
	d, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	out, err := d.Context(db.ContextOptions{Project: project})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestConsolidateCmdPlanWritesNothing(t *testing.T) {
	path := seedConsolidateDB(t)
	resetConsolidateFlags(t)
	out, err := runCLIOut(t, path, "consolidate", "--project", "demo")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "new ×3") || !strings.Contains(out, "(ev: #1 #2 #3)") || !strings.Contains(out, "Plan only") {
		t.Errorf("plan output = %q", out)
	}
	if n := observationCount(t, path); n != 0 {
		t.Errorf("observations = %d, plan must write nothing", n)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "backups")); !os.IsNotExist(err) {
		t.Errorf("plan must not write backups: %v", err)
	}
}

func TestConsolidateCmdApplyBackupContextAndDelete(t *testing.T) {
	path := seedConsolidateDB(t)
	resetConsolidateFlags(t)
	out, err := runCLIOut(t, path, "consolidate", "--project", "demo", "--apply")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Backup: ") || !strings.Contains(out, "o1 ×3") {
		t.Errorf("apply output = %q", out)
	}
	backups, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "backups", "consolidate-*.json"))
	if len(backups) != 1 {
		t.Fatalf("backups = %v, want one file", backups)
	}
	data, _ := os.ReadFile(backups[0])
	var backup struct {
		Observations []db.Observation `json:"observations"`
		Proposals    []db.Observation `json:"proposals"`
	}
	if err := json.Unmarshal(data, &backup); err != nil || len(backup.Proposals) != 1 {
		t.Errorf("backup = %s (err %v)", data, err)
	}
	if n := observationCount(t, path); n != 1 {
		t.Fatalf("observations = %d, want 1", n)
	}

	ctxOut := projectContextOf(t, path, "demo")
	if !strings.Contains(ctxOut, "- o1 ×3 ") {
		t.Errorf("context should show observation:\n%s", ctxOut)
	}
	for _, hidden := range []string{"- #1 ", "- #2 ", "- #3 "} {
		if strings.Contains(ctxOut, hidden) {
			t.Errorf("evidence %q should be hidden:\n%s", hidden, ctxOut)
		}
	}
	if !strings.Contains(ctxOut, "- #4 ") {
		t.Errorf("unrelated memory should stay:\n%s", ctxOut)
	}

	listOut, err := runCLIOut(t, path, "observation", "list", "--project", "demo")
	if err != nil || !strings.Contains(listOut, "o1 demo feedback ×3 heuristic") {
		t.Errorf("observation list = %q (err %v)", listOut, err)
	}

	if _, err := runCLIOut(t, path, "observation", "delete", "1"); err != nil {
		t.Fatal(err)
	}
	ctxOut = projectContextOf(t, path, "demo")
	if strings.Contains(ctxOut, "o1 ") || !strings.Contains(ctxOut, "- #1 ") || !strings.Contains(ctxOut, "- #3 ") {
		t.Errorf("delete should restore evidence lines:\n%s", ctxOut)
	}
	if _, err := runCLIOut(t, path, "observation", "delete", "o1"); err == nil {
		t.Error("deleting an inactive observation should fail")
	}
}

func TestConsolidateCmdUsesLLMAndReportsFailures(t *testing.T) {
	path := seedConsolidateDB(t)
	resetConsolidateFlags(t)
	consolidateLLM = func(*db.DB) (llm.Client, error) {
		return llm.FuncClient{ID: "fake", Fn: func(context.Context, llm.Request) (string, error) {
			return "", llm.ErrBudget
		}}, nil
	}
	out, err := runCLIOut(t, path, "consolidate", "--project", "demo", "--apply")
	if err == nil || !strings.Contains(err.Error(), "all 1 clusters failed") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(out, "skipped #1 #2 #3") || !strings.Contains(out, "budget") {
		t.Errorf("failure should be reported in output: %q", out)
	}
	if n := observationCount(t, path); n != 0 {
		t.Errorf("observations = %d, LLM failure must not fall back to heuristic", n)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "backups")); !os.IsNotExist(err) {
		t.Errorf("nothing to write ⇒ no backup: %v", err)
	}
}

func TestConsolidateCmdLLMResolveError(t *testing.T) {
	path := seedConsolidateDB(t)
	resetConsolidateFlags(t)
	consolidateLLM = func(*db.DB) (llm.Client, error) { return nil, errors.New("MEMORY_MCP_LLM_API_KEY missing") }
	if _, err := runCLIOut(t, path, "consolidate", "--project", "demo"); err == nil || !strings.Contains(err.Error(), "API_KEY") {
		t.Fatalf("err = %v", err)
	}
}

func TestConsolidateCmdNeedsScope(t *testing.T) {
	path := seedConsolidateDB(t)
	resetConsolidateFlags(t)
	if _, err := runCLIOut(t, path, "consolidate"); err == nil || !strings.Contains(err.Error(), "--project or --all") {
		t.Fatalf("err = %v", err)
	}
}

func TestConsolidateCmdRefusesRemote(t *testing.T) {
	path := seedConsolidateDB(t)
	resetConsolidateFlags(t)
	for _, args := range [][]string{
		{"consolidate", "--all", "--remote", "http://127.0.0.1:1"},
		{"observation", "list", "--remote", "http://127.0.0.1:1"},
		{"observation", "delete", "1", "--remote", "http://127.0.0.1:1"},
	} {
		_, err := runCLIOut(t, path, args...)
		if err == nil || !strings.Contains(err.Error(), "only supports local DB") {
			t.Errorf("%v: err = %v", args, err)
		}
		remoteFlag = ""
	}
}
