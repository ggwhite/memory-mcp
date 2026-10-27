// Package obnote 依訊息內容比對 Obsidian vault 的筆記檔名（不讀內容），給 recall --hook 用。
package obnote

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ggwhite/memory-mcp/internal/db"
)

// RootEnv 覆寫 vault 根目錄（預設 ~/ob）。
const RootEnv = "MEMORY_MCP_OB"

const (
	cacheTTL = 60 * time.Second
	// 門檻用真實 vault 校準，寧可漏不可亂給：
	// 至少 3 個字元被 3 字以上的查詢詞覆蓋且覆蓋率 ≥ minStrongRatio，
	// 或整個名稱（≥4 字）幾乎都被查詢詞（含 2 字詞）覆蓋。
	minStrongRunes = 3
	minStrongRatio = 0.3
	minFullRunes   = 4
	minFullRatio   = 0.8
)

// skipDirs 走訪時整個略過的目錄名（另外 . 開頭的目錄一律略過）。
var skipDirs = map[string]bool{"assets": true, "raw": true}

// genericStems 泛用檔名，不拿來比對。
var genericStems = map[string]bool{
	"index": true, "log": true, "readme": true, "agents": true, "claude": true, "summary": true, "todo": true,
}

// Entry 一份筆記：相對 vault 的路徑、檔名（不含 .md）、上層目錄名。
type Entry struct {
	Rel  string `json:"rel"`
	Stem string `json:"stem"`
	Dir  string `json:"dir"`
}

// Root 回傳 vault 實際路徑（已解 symlink）與輸出用的顯示前綴；vault 不存在時 ok=false。
func Root() (root, display string, ok bool) {
	home, _ := os.UserHomeDir()
	p, display := os.Getenv(RootEnv), os.Getenv(RootEnv)
	if p == "" {
		if home == "" {
			return "", "", false
		}
		p, display = filepath.Join(home, "ob"), "~/ob"
	} else if home != "" && (p == "~" || strings.HasPrefix(p, "~/")) {
		p = filepath.Join(home, p[1:])
	}
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", "", false
	}
	if fi, err := os.Stat(r); err != nil || !fi.IsDir() {
		return "", "", false
	}
	return r, strings.TrimSuffix(display, "/"), true
}

type cacheFile struct {
	Root      string  `json:"root"`
	RootMtime int64   `json:"root_mtime"`
	Built     int64   `json:"built"`
	Entries   []Entry `json:"entries"`
}

// CachePath 走訪結果快取檔位置。
func CachePath() string {
	return filepath.Join(os.TempDir(), "memory-mcp", "ob-index.json")
}

// Load 取得 vault 筆記清單：快取的 root 相同、root mtime 未變且未超過 60 秒就用快取，
// 否則（含快取壞掉）重新走訪並寫回快取。ctx 逾時會中斷走訪，不寫快取。
func Load(ctx context.Context, root string) ([]Entry, error) {
	fi, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	mtime := fi.ModTime().UnixNano()
	if data, err := os.ReadFile(CachePath()); err == nil {
		var c cacheFile
		if json.Unmarshal(data, &c) == nil && c.Root == root && c.RootMtime == mtime &&
			time.Since(time.Unix(0, c.Built)) < cacheTTL && c.Entries != nil {
			return c.Entries, nil
		}
	}
	entries, err := Walk(ctx, root)
	if err != nil {
		return nil, err
	}
	saveCache(cacheFile{Root: root, RootMtime: mtime, Built: time.Now().UnixNano(), Entries: entries})
	return entries, nil
}

func saveCache(c cacheFile) {
	data, err := json.Marshal(c)
	if err != nil {
		return
	}
	path := CachePath()
	if os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ob-index-*.tmp")
	if err != nil {
		return
	}
	_, werr := tmp.Write(data)
	if cerr := tmp.Close(); werr != nil || cerr != nil {
		os.Remove(tmp.Name())
		return
	}
	if os.Rename(tmp.Name(), path) != nil {
		os.Remove(tmp.Name())
	}
}

// Walk 走訪 vault 內所有 .md，略過 . 開頭目錄、assets、raw 與泛用檔名。
func Walk(ctx context.Context, root string) ([]Entry, error) {
	entries := []Entry{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			if d != nil && d.IsDir() && path != root {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if path != root && (strings.HasPrefix(name, ".") || skipDirs[name]) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(name), ".md") {
			return nil
		}
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		if genericStems[strings.ToLower(stem)] {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		dir := filepath.Base(filepath.Dir(path))
		if filepath.Dir(rel) == "." {
			dir = ""
		}
		entries = append(entries, Entry{Rel: filepath.ToSlash(rel), Stem: stem, Dir: dir})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// passes 名稱是否過門檻，回傳覆蓋率。
func passes(pt db.PromptTerms, name string) (bool, float64) {
	if name == "" {
		return false, 0
	}
	strong, all, n := pt.Coverage(name)
	ratio := float64(all) / float64(n)
	if strong >= minStrongRunes && float64(strong)/float64(n) >= minStrongRatio {
		return true, ratio
	}
	if n >= minFullRunes && ratio >= minFullRatio {
		return true, ratio
	}
	return false, 0
}

// Match 回傳檔名或上層目錄名過門檻的筆記，檔名命中排在目錄命中之前，同級依覆蓋率排序；
// exclude 為已注入過的相對路徑。
func Match(pt db.PromptTerms, entries []Entry, exclude map[string]bool, limit int) []Entry {
	if pt.Empty() {
		return nil
	}
	type scored struct {
		e     Entry
		score float64
	}
	var hits []scored
	for _, e := range entries {
		if exclude[e.Rel] {
			continue
		}
		if ok, r := passes(pt, e.Stem); ok {
			hits = append(hits, scored{e, 1 + r})
		} else if ok, r := passes(pt, e.Dir); ok {
			hits = append(hits, scored{e, r})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].e.Rel < hits[j].e.Rel
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]Entry, len(hits))
	for i, h := range hits {
		out[i] = h.e
	}
	return out
}
