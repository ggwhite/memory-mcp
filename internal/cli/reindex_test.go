package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"memory-mcp/internal/db"
)

// fakeOllama 對內容在 fail 集合內的請求回 404 model not found，其餘回固定向量。
func fakeOllama(t *testing.T, failAll bool, fail map[string]bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
			Input string `json:"input"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if failAll || fail[req.Input] {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"error":"model \"`+req.Model+`\" not found, try pulling it first"}`)
			return
		}
		io.WriteString(w, `{"embeddings":[[0.1,0.2,0.3]]}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func runReindexCmd(t *testing.T, contents []string, srv *httptest.Server) (string, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "m.db")
	d, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range contents {
		if _, err := d.Store(&db.Memory{Type: "til", Content: c}); err != nil {
			t.Fatal(err)
		}
	}
	d.Close()

	t.Setenv("MEMORY_MCP_OLLAMA_URL", srv.URL)
	t.Setenv("MEMORY_MCP_EMBED_MODEL", "")
	t.Setenv("MEMORY_MCP_REMOTE", "")
	t.Cleanup(func() {
		dbPath = ""
		rootCmd.SetArgs(nil)
		rootCmd.SetErr(nil)
		reindexCmd.SilenceUsage = false
	})
	var errw bytes.Buffer
	rootCmd.SetErr(&errw)
	rootCmd.SetArgs([]string{"reindex", "--db", path})
	err = rootCmd.Execute()
	return errw.String(), err
}

func TestReindexCmdAllFailed(t *testing.T) {
	stderr, err := runReindexCmd(t, []string{"a", "b"}, fakeOllama(t, true, nil))
	if err == nil {
		t.Fatalf("want error when every memory fails, stderr=%q", stderr)
	}
	for _, want := range []string{"all 2", "404", "bge-m3", "not found"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should contain %q", err, want)
		}
	}
}

func TestReindexCmdPartialFailed(t *testing.T) {
	stderr, err := runReindexCmd(t, []string{"ok", "bad"}, fakeOllama(t, false, map[string]bool{"bad": true}))
	if err != nil {
		t.Fatalf("partial success should exit 0, got %v", err)
	}
	if !strings.Contains(stderr, "1 failed") || !strings.Contains(stderr, "404") {
		t.Errorf("stderr should report failure count and reason, got %q", stderr)
	}
}

// 對照組：全部成功時 exit 0 且 stderr 不印錯誤。
func TestReindexCmdAllSucceeded(t *testing.T) {
	stderr, err := runReindexCmd(t, []string{"a", "b"}, fakeOllama(t, false, nil))
	if err != nil || stderr != "" {
		t.Errorf("err=%v stderr=%q", err, stderr)
	}
}
