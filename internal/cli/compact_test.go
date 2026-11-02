package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ggwhite/memory-mcp/internal/db"
)

// seedCompactDB 建立含一組可合併的舊 summary（2 筆）與一筆近期 summary 的 DB。
func seedCompactDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "m.db")
	d, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, c := range []struct {
		content string
		created time.Time
	}{
		{"舊一", time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)},
		{"舊二", time.Date(2025, 3, 2, 0, 0, 0, 0, time.UTC)},
		{"近期", time.Now().UTC()},
	} {
		if _, err := d.StoreMerged(&db.Memory{Type: "summary", Project: "p", Content: c.content, Created: c.created}, nil); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func resetCompactFlags(t *testing.T) {
	orig := compactRunner
	t.Cleanup(func() {
		compactCmd.Flags().Set("apply", "false")
		compactCmd.Flags().Set("project", "")
		compactCmd.Flags().Set("older-than", "60d")
		compactRunner = orig
		remoteFlag = ""
	})
}

func exportCount(t *testing.T, path string) []db.Memory {
	t.Helper()
	d, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	all, _ := d.ExportAll()
	return all
}

func TestCompactCmdDryRun(t *testing.T) {
	path := seedCompactDB(t)
	resetCompactFlags(t)
	compactRunner = func(context.Context, string) (string, error) {
		t.Fatal("dry run must not call the LLM")
		return "", nil
	}
	if err := runCLI(t, path, "compact"); err != nil {
		t.Fatal(err)
	}
	if n := len(exportCount(t, path)); n != 3 {
		t.Errorf("memories = %d, want 3 (dry run changes nothing)", n)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "backups")); !os.IsNotExist(err) {
		t.Errorf("dry run must not write backups: %v", err)
	}
}

func TestCompactCmdApply(t *testing.T) {
	path := seedCompactDB(t)
	resetCompactFlags(t)
	compactRunner = func(context.Context, string) (string, error) { return "合併", nil }
	if err := runCLI(t, path, "compact", "--apply"); err != nil {
		t.Fatal(err)
	}
	all := exportCount(t, path)
	if len(all) != 2 {
		t.Fatalf("memories = %+v, want merged + recent", all)
	}
	if !strings.Contains(all[1].Content, "月度彙整") || all[1].Tags != "compacted,month:2025-03" {
		t.Errorf("merged = %+v", all[1])
	}
	backups, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "backups", "compact-*.json"))
	if len(backups) != 1 {
		t.Errorf("backups = %v, want one file", backups)
	}
}

func TestCompactCmdRefusesRemote(t *testing.T) {
	path := seedCompactDB(t)
	resetCompactFlags(t)
	err := runCLI(t, path, "compact", "--remote", "http://127.0.0.1:1")
	if err == nil || !strings.Contains(err.Error(), "compact only supports local DB") {
		t.Fatalf("err = %v", err)
	}
}

// 全部群組都被跳過時，錯誤只能由 main 印一次，cobra 不能再印一份 "Error: ..."。
func TestCompactCmdAllSkippedPrintsErrorOnce(t *testing.T) {
	path := seedCompactDB(t)
	resetCompactFlags(t)
	compactRunner = func(context.Context, string) (string, error) { return "", os.ErrInvalid }

	var errBuf strings.Builder
	t.Setenv("MEMORY_MCP_REMOTE", "")
	t.Cleanup(func() {
		dbPath = ""
		rootCmd.SetArgs(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetOut(nil)
	})
	rootCmd.SetErr(&errBuf)
	rootCmd.SetArgs([]string{"compact", "--apply", "--db", path})
	err := rootCmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "all 1 groups skipped") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(errBuf.String(), "all 1 groups skipped") {
		t.Errorf("cobra must not print the error itself, stderr = %q", errBuf.String())
	}
}

func TestCompactCmdRejectsNonPositiveOlderThan(t *testing.T) {
	path := seedCompactDB(t)
	resetCompactFlags(t)
	for _, v := range []string{"-5d", "0d"} {
		err := runCLI(t, path, "compact", "--older-than", v)
		if err == nil || !strings.Contains(err.Error(), "must be positive") {
			t.Errorf("--older-than %s: err = %v, want positive-duration error", v, err)
		}
	}
}
