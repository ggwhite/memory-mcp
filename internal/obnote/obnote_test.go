package obnote

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"memory-mcp/internal/db"
)

func writeVault(t *testing.T, files ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, f := range files {
		p := filepath.Join(root, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

var testFiles = []string{
	"追劇/雲夢記.md", "追劇/海港小鎮日常記.md", "社團/積分表/規則說明.md",
	"旅遊/露營車改車全攻略.md", "料理/咖哩筆記.md", "園藝/澆水與施肥.md",
	"index.md", "wiki/log.md", "README.md", "a/SUMMARY.md",
	".obsidian/雲夢記.md", ".claude/x.md", ".trash/雲夢記舊.md", "assets/雲夢記.md", "raw/雲夢記.md",
	"追劇/封面.png",
}

func rels(es []Entry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Rel)
	}
	return out
}

func TestWalkExclusions(t *testing.T) {
	root := writeVault(t, testFiles...)
	es, err := Walk(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	got := rels(es)
	sort.Strings(got)
	want := []string{"社團/積分表/規則說明.md", "旅遊/露營車改車全攻略.md", "料理/咖哩筆記.md", "園藝/澆水與施肥.md", "追劇/海港小鎮日常記.md", "追劇/雲夢記.md"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
	for _, e := range es {
		if e.Rel == "社團/積分表/規則說明.md" && (e.Stem != "規則說明" || e.Dir != "積分表") {
			t.Errorf("entry = %+v", e)
		}
	}
}

func match(t *testing.T, root, prompt string, exclude map[string]bool) []string {
	t.Helper()
	es, err := Walk(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	return rels(Match(db.NewPromptTerms(prompt), es, exclude, 2))
}

func TestMatch(t *testing.T) {
	root := writeVault(t, testFiles...)
	cases := map[string][]string{
		"雲夢記看到第幾季了":     {"追劇/雲夢記.md"},
		"積分表這週點數怎麼算":    {"社團/積分表/規則說明.md"}, // 目錄名命中
		"露營車的準備還差什麼":    {"旅遊/露營車改車全攻略.md"},
		"咖哩要不要加辣":     {"料理/咖哩筆記.md"},
		"陽台澆水跟施肥量要怎麼安排": {"園藝/澆水與施肥.md"}, // 只靠 2 字詞但幾乎全覆蓋
		"今天天氣如何":        nil,
		"幫我寫個 README":   nil,
		"規則要怎麼改":        nil, // 只命中泛用名稱的一小段
		"趕海":            nil, // 覆蓋太少
	}
	for prompt, want := range cases {
		if got := match(t, root, prompt, nil); !reflect.DeepEqual(got, want) {
			t.Errorf("%q: got %v, want %v", prompt, got, want)
		}
	}
	if got := match(t, root, "雲夢記看到第幾季了", map[string]bool{"追劇/雲夢記.md": true}); got != nil {
		t.Errorf("excluded note returned: %v", got)
	}
}

func TestMatchLimitAndStemFirst(t *testing.T) {
	root := writeVault(t, "積分表/a.md", "積分表/b.md", "積分表/c.md", "社團/積分表說明.md")
	got := match(t, root, "積分表這週點數怎麼算", nil)
	if len(got) != 2 || got[0] != "社團/積分表說明.md" {
		t.Errorf("got %v, want stem match first and 2 results", got)
	}
}

func TestLoadCache(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	root := writeVault(t, "追劇/雲夢記.md")
	ctx := context.Background()
	if es, err := Load(ctx, root); err != nil || len(es) != 1 {
		t.Fatalf("first load: %v %v", es, err)
	}
	// 子目錄新增檔案不改 root mtime：TTL 內仍用快取
	os.WriteFile(filepath.Join(root, "追劇", "雲霞記.md"), []byte("x"), 0o644)
	if es, _ := Load(ctx, root); len(es) != 1 {
		t.Errorf("within TTL should use cache, got %d", len(es))
	}
	// root mtime 改變 → 重建
	os.WriteFile(filepath.Join(root, "新筆記.md"), []byte("x"), 0o644)
	future := time.Now().Add(2 * time.Second)
	os.Chtimes(root, future, future)
	if es, _ := Load(ctx, root); len(es) != 3 {
		t.Errorf("root mtime changed should rebuild, got %d", len(es))
	}
	// 快取過期 → 重建
	os.WriteFile(filepath.Join(root, "追劇", "夢裡尋家.md"), []byte("x"), 0o644)
	data, _ := os.ReadFile(CachePath())
	var c cacheFile
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	c.Built = time.Now().Add(-2 * cacheTTL).UnixNano()
	saveCache(c)
	if es, _ := Load(ctx, root); len(es) != 4 {
		t.Errorf("expired cache should rebuild, got %d", len(es))
	}
	// 快取壞掉 → 重建
	os.WriteFile(CachePath(), []byte("{broken"), 0o600)
	os.WriteFile(filepath.Join(root, "追劇", "雲瀾.md"), []byte("x"), 0o644)
	if es, err := Load(ctx, root); err != nil || len(es) != 5 {
		t.Errorf("broken cache should rebuild, got %d %v", len(es), err)
	}
}

func TestWalkCancelled(t *testing.T) {
	root := writeVault(t, "追劇/雲夢記.md")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Walk(ctx, root); err == nil {
		t.Error("want error on cancelled ctx")
	}
}

func TestRoot(t *testing.T) {
	real := writeVault(t, "a.md")
	link := filepath.Join(t.TempDir(), "ob")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv(RootEnv, link)
	r, display, ok := Root()
	wantReal, _ := filepath.EvalSymlinks(real)
	if !ok || r != wantReal || display != link {
		t.Errorf("Root() = %q %q %v", r, display, ok)
	}
	t.Setenv(RootEnv, filepath.Join(t.TempDir(), "missing"))
	if _, _, ok := Root(); ok {
		t.Error("missing vault should be skipped")
	}
}
