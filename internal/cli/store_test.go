package cli

import (
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ggwhite/memory-mcp/internal/db"
)

// runCLI 以指定 DB 執行一次 root command；cobra flag 值會殘留，結束時重設 reset 列出的 flag。
func runCLI(t *testing.T, path string, args ...string) error {
	t.Helper()
	t.Setenv("MEMORY_MCP_OLLAMA_URL", "http://127.0.0.1:1")
	t.Setenv("MEMORY_MCP_REMOTE", "")
	t.Cleanup(func() {
		dbPath = ""
		rootCmd.SetArgs(nil)
		rootCmd.SetErr(nil)
		storeCmd.Flags().Set("supersedes", "")
	})
	rootCmd.SetErr(io.Discard)
	rootCmd.SetArgs(append(args, "--db", path))
	return rootCmd.Execute()
}

func TestStoreCmdSupersedes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.db")
	d, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := d.Store(&db.Memory{Type: "til", Content: "舊 a"})
	b, _ := d.Store(&db.Memory{Type: "til", Content: "舊 b"})
	d.Close()

	if err := runCLI(t, path, "store", "-t", "til", "--supersedes", "1,999", "新"); err == nil || !strings.Contains(err.Error(), "999") {
		t.Fatalf("err = %v, want missing #999", err)
	}
	if err := runCLI(t, path, "store", "-t", "til", "--supersedes", "1,2", "新"); err != nil {
		t.Fatal(err)
	}

	d, _ = db.Open(path)
	defer d.Close()
	all, _ := d.ExportAll()
	if len(all) != 1 || all[0].Content != "新" || all[0].ID == a || all[0].ID == b {
		t.Errorf("remaining = %+v", all)
	}
}
