package hook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInjectedPathSanitize(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	if InjectedPath("") != "" {
		t.Error("empty session id should give empty path")
	}
	p := InjectedPath("../../etc/passwd")
	if filepath.Dir(p) != filepath.Join(os.TempDir(), "memory-mcp") || strings.Contains(filepath.Base(p), "/") {
		t.Errorf("unsafe path %q", p)
	}
	if got := filepath.Base(InjectedPath("abc-123_X")); got != "recall-abc-123_X.json" {
		t.Errorf("got %q", got)
	}
}

func TestAddLoadInjected(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	if err := AddInjected("s1", []int64{3, 5}); err != nil {
		t.Fatal(err)
	}
	if err := AddInjected("s1", []int64{5, 7}); err != nil {
		t.Fatal(err)
	}
	got := LoadInjected("s1")
	if len(got) != 3 || !got[3] || !got[5] || !got[7] {
		t.Errorf("got %v", got)
	}
	if len(LoadInjected("s2")) != 0 {
		t.Error("other session should be empty")
	}
	if err := AddInjected("", []int64{1}); err != nil || len(LoadInjected("")) != 0 {
		t.Error("empty session id should be a no-op")
	}
}

func TestResolveRegistered(t *testing.T) {
	home := setupProjects(t, testPrefixes)
	cwd := filepath.Join(home, "work/Acme/docs")
	other := filepath.Join(home, "github/widget")
	mkdirs(t, cwd, other)
	if p, ok := Resolve(cwd); p != "acme" || !ok {
		t.Errorf("Resolve(registered) = %q, %v", p, ok)
	}
	if p, ok := Resolve(other); p != "widget" || ok {
		t.Errorf("Resolve(unregistered) = %q, %v", p, ok)
	}
	if p, ok := Resolve(home); p != "home" || ok {
		t.Errorf("Resolve(home) = %q, %v", p, ok)
	}
}

func TestAddLoadInjectedNotes(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	AddInjected("s1", []int64{1})
	if err := AddInjectedNotes("s1", []string{"~/ob/a.md", "~/ob/a.md"}); err != nil {
		t.Fatal(err)
	}
	AddInjectedNotes("s1", []string{"~/ob/b.md"})
	notes := LoadInjectedNotes("s1")
	if len(notes) != 2 || !notes["~/ob/a.md"] || !notes["~/ob/b.md"] {
		t.Errorf("notes = %v", notes)
	}
	if ids := LoadInjected("s1"); !ids[1] {
		t.Errorf("ids lost after adding notes: %v", ids)
	}
}
