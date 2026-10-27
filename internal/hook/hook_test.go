package hook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRead(t *testing.T) {
	in := Read(strings.NewReader(`{"session_id":"s1","transcript_path":"/t.jsonl","cwd":"/home/dev/work/Globex","reason":"exit"}`))
	if in.SessionID != "s1" || in.TranscriptPath != "/t.jsonl" || in.Cwd != "/home/dev/work/Globex" || in.Reason != "exit" {
		t.Errorf("got %+v", in)
	}
}

func TestReadEmptyInput(t *testing.T) {
	if in := Read(strings.NewReader("")); in != (Input{}) {
		t.Errorf("got %+v", in)
	}
	if in := Read(strings.NewReader("not json")); in != (Input{}) {
		t.Errorf("got %+v", in)
	}
}

func TestProject(t *testing.T) {
	t.Setenv(ProjectsEnv, filepath.Join(t.TempDir(), "none.json"))
	if got := Project("/home/dev/work/Globex"); got != "globex" {
		t.Errorf("got %q", got)
	}
	if got := Project("/home/dev/side/my.blog"); got != "my.blog" {
		t.Errorf("got %q", got)
	}
	wd, _ := os.Getwd()
	if got := Project(""); got != strings.ToLower(filepath.Base(wd)) {
		t.Errorf("got %q", got)
	}
}

// setupProjects 建立假 HOME 與對照設定檔，回傳 HOME 路徑。
func setupProjects(t *testing.T, cfg string) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	path := filepath.Join(home, "projects.json")
	if cfg != "" {
		if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv(ProjectsEnv, path)
	return home
}

func mkdirs(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

const testPrefixes = `{"prefixes": {"~/work/Acme": "Acme", "~/work/Acme/svc": "svc", "~/work/Globex": "globex"}}`

func TestProjectPrefixSubdir(t *testing.T) {
	home := setupProjects(t, testPrefixes)
	cases := map[string]string{
		filepath.Join(home, "work/Acme"):             "acme",
		filepath.Join(home, "work/Acme/acme/legacy"): "acme",
		filepath.Join(home, "work/Acme/docs"):        "acme",
		filepath.Join(home, "work/Globex/sub"):       "globex",
	}
	for cwd, want := range cases {
		mkdirs(t, cwd)
		if got := Project(cwd); got != want {
			t.Errorf("Project(%q) = %q, want %q", cwd, got, want)
		}
	}
}

func TestProjectLongestPrefix(t *testing.T) {
	home := setupProjects(t, testPrefixes)
	cwd := filepath.Join(home, "work/Acme/svc/api")
	mkdirs(t, cwd)
	if got := Project(cwd); got != "svc" {
		t.Errorf("got %q, want svc", got)
	}
}

func TestProjectComponentBoundary(t *testing.T) {
	home := setupProjects(t, testPrefixes)
	cwd := filepath.Join(home, "work/Acme2")
	mkdirs(t, cwd)
	if got := Project(cwd); got != "acme2" {
		t.Errorf("got %q, want acme2", got)
	}
}

func TestProjectTildeExpandNonexistentCwd(t *testing.T) {
	home := setupProjects(t, testPrefixes)
	if got := Project(filepath.Join(home, "work/Globex/not-created")); got != "globex" {
		t.Errorf("got %q, want globex", got)
	}
}

func TestProjectSymlink(t *testing.T) {
	home := setupProjects(t, testPrefixes)
	real := filepath.Join(home, "work/Globex/app")
	mkdirs(t, real)
	link := filepath.Join(home, "globex-link")
	if err := os.Symlink(filepath.Join(home, "work/Globex"), link); err != nil {
		t.Fatal(err)
	}
	if got := Project(filepath.Join(link, "app")); got != "globex" {
		t.Errorf("cwd via symlink: got %q, want globex", got)
	}

	// prefix 本身是 symlink
	linkHome := setupProjects(t, `{"prefixes": {"~/alias": "aliased"}}`)
	target := filepath.Join(linkHome, "real/proj")
	mkdirs(t, target)
	if err := os.Symlink(filepath.Join(linkHome, "real"), filepath.Join(linkHome, "alias")); err != nil {
		t.Fatal(err)
	}
	if got := Project(target); got != "aliased" {
		t.Errorf("prefix via symlink: got %q, want aliased", got)
	}
}

func TestProjectConfigMissing(t *testing.T) {
	home := setupProjects(t, "")
	cwd := filepath.Join(home, "work/Acme/docs")
	mkdirs(t, cwd)
	if got := Project(cwd); got != "docs" {
		t.Errorf("got %q, want docs", got)
	}
}

func TestProjectConfigInvalid(t *testing.T) {
	home := setupProjects(t, `{"prefixes": [`)
	cwd := filepath.Join(home, "work/Acme/Docs")
	mkdirs(t, cwd)
	if got := Project(cwd); got != "docs" {
		t.Errorf("got %q, want docs", got)
	}
}

func TestProjectHome(t *testing.T) {
	home := setupProjects(t, testPrefixes)
	if got := Project(home); got != "home" {
		t.Errorf("got %q, want home", got)
	}
	setupProjects(t, "")
	if got := Project(os.Getenv("HOME")); got != "home" {
		t.Errorf("no config: got %q, want home", got)
	}
	h := setupProjects(t, `{"prefixes": {"~": "me"}}`)
	if got := Project(h); got != "me" {
		t.Errorf("mapped home: got %q, want me", got)
	}
}

func TestProjectNoMatchFallback(t *testing.T) {
	home := setupProjects(t, testPrefixes)
	cwd := filepath.Join(home, "other/My-Repo")
	mkdirs(t, cwd)
	if got := Project(cwd); got != "my-repo" {
		t.Errorf("got %q, want my-repo", got)
	}
}
