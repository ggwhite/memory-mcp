package db

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestRecallTerms(t *testing.T) {
	cases := map[string][]string{
		"305-qa 年度報稅匯出又卡住了":      {"305-qa", "年度報", "度報稅", "報稅匯", "稅匯出"},
		"memory-mcp 的注入規則":       {"memory-mcp", "注入規", "入規則"},
		"今天天氣如何":                 {"天天氣", "天氣如"},
		"好 ok 天氣":                nil, // 不足 3 字的段略過
		"What is the Hangar tag": {"hangar", "tag"},
		"(gen_daemon).":          {"gen_daemon"},
	}
	for in, want := range cases {
		if got := RecallTerms(in); !reflect.DeepEqual(got, want) {
			t.Errorf("RecallTerms(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestScoreUnits(t *testing.T) {
	segs := recallSegments("積分表這週分數怎麼算")
	// 積分表（3 字詞）與 分數（2 字詞）在訊息中不相連 → 2 個片段，2 字詞加分一次
	units, bonus := scoreUnits(segs, "積分表 v26：功課分數改看行為")
	if units != 2 || bonus != recallBigramWeight {
		t.Errorf("units=%d bonus=%v, want 2 and %v", units, bonus, recallBigramWeight)
	}
	// 只命中一個連續片段（安裝步驟 的兩個重疊窗口）算 1 個
	units, _ = scoreUnits(recallSegments("寫個安裝步驟"), "不寫安裝步驟")
	if units != 1 {
		t.Errorf("overlapping windows: units=%d, want 1", units)
	}
}

func seedRecall(t *testing.T) *DB {
	t.Helper()
	d := testDB(t)
	mems := []Memory{
		{Type: "knowledge", Project: "acme", Content: "305-qa 年度報稅匯出卡在上傳中，根因是訪客量暴增，導出查詢逾時"},
		{Type: "summary", Project: "personal-hobby", Content: "積分表第一週分數已填進積分紀錄，小明 19 分、小華 14 分"},
		{Type: "til", Project: "other", Content: "Hangar tag retention 把 git tag 清掉，docker compose pull 失敗"},
		{Type: "til", Project: "acme", Content: "Hangar tag retention 規則修正後 docker compose pull 恢復"},
		{Type: "til", Project: "other", Content: "重構程式碼時記得補測試"}, // 只命中一個片段，不該過門檻
	}
	for i := range mems {
		if _, err := d.Store(&mems[i]); err != nil {
			t.Fatal(err)
		}
	}
	addNoise(t, d)
	return d
}

// addNoise 補一千多筆無關記憶，讓 bm25 的 IDF 量級接近真實 DB（門檻是用真實 DB 校準的）。
func addNoise(t *testing.T, d *DB) {
	t.Helper()
	noise := make([]Memory, 1200)
	for i := range noise {
		noise[i] = Memory{Type: "til", Project: "noise", Content: fmt.Sprintf("無關的填充內容第 %d 筆 lorem ipsum", i)}
	}
	if _, err := d.ImportBatch(noise); err != nil {
		t.Fatal(err)
	}
}

func recallIDs(t *testing.T, d *DB, opts RecallOptions) []int64 {
	t.Helper()
	res, err := d.Recall(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, r := range res {
		ids = append(ids, r.ID)
	}
	return ids
}

func TestRecallHitsAndThreshold(t *testing.T) {
	d := seedRecall(t)
	if ids := recallIDs(t, d, RecallOptions{Prompt: "305-qa 年度報稅匯出又卡住了"}); len(ids) == 0 || ids[0] != 1 {
		t.Errorf("want #1 first, got %v", ids)
	}
	for _, p := range []string{"今天天氣如何", "這段程式碼有 bug 嗎", "幫我看一下這個 MR"} {
		if ids := recallIDs(t, d, RecallOptions{Prompt: p}); len(ids) != 0 {
			t.Errorf("%q: want no result, got %v", p, ids)
		}
	}
}

func TestRecallExcludePersonal(t *testing.T) {
	d := seedRecall(t)
	prompt := "積分表這週分數怎麼算"
	if ids := recallIDs(t, d, RecallOptions{Prompt: prompt}); !reflect.DeepEqual(ids, []int64{2}) {
		t.Errorf("without exclusion: got %v, want [2]", ids)
	}
	if ids := recallIDs(t, d, RecallOptions{Prompt: prompt, ExcludePersonal: true}); len(ids) != 0 {
		t.Errorf("personal leaked: %v", ids)
	}
}

func TestRecallExcludeIDsAndProjectBoost(t *testing.T) {
	d := seedRecall(t)
	prompt := "Hangar tag 又不見了 docker compose pull 失敗"
	base := recallIDs(t, d, RecallOptions{Prompt: prompt})
	if !reflect.DeepEqual(base, []int64{3, 4}) {
		t.Fatalf("base order = %v, want [3 4]", base)
	}
	if ids := recallIDs(t, d, RecallOptions{Prompt: prompt, Project: "acme"}); !reflect.DeepEqual(ids, []int64{4, 3}) {
		t.Errorf("same-project boost: got %v, want [4 3]", ids)
	}
	if ids := recallIDs(t, d, RecallOptions{Prompt: prompt, Exclude: map[int64]bool{3: true}}); !reflect.DeepEqual(ids, []int64{4}) {
		t.Errorf("exclude: got %v, want [4]", ids)
	}
}

func TestRecallLimit(t *testing.T) {
	d := testDB(t)
	addNoise(t, d)
	for i := 0; i < 6; i++ {
		d.Store(&Memory{Type: "til", Project: "p", Content: "authsvc legacyToken 漏洞紀錄"})
	}
	if ids := recallIDs(t, d, RecallOptions{Prompt: "authsvc legacyToken 漏洞"}); len(ids) != 3 {
		t.Errorf("want 3 results, got %v", ids)
	}
}

func TestPromptTermsCoverage(t *testing.T) {
	pt := NewPromptTerms("露營車的準備還差什麼")
	if s, a, n := pt.Coverage("露營車改車全攻略"); s != 3 || a != 3 || n != 8 {
		t.Errorf("strong=%d all=%d n=%d", s, a, n)
	}
	pt = NewPromptTerms("陽台澆水跟施肥量")
	if s, a, n := pt.Coverage("澆水與施肥"); s != 0 || a != 4 || n != 5 {
		t.Errorf("strong=%d all=%d n=%d", s, a, n)
	}
	if !NewPromptTerms("好 ok").Empty() {
		t.Error("want empty terms")
	}
}

// 兩個候選：次名約第一名 50%～60% 分數，排除第一名後次名不能因比例門檻改用自己當基準而浮上來。
const (
	dedupeTop    = "305-qa 年度報稅匯出卡在上傳中，訪客量暴增導致查詢逾時"
	dedupeSecond = "201-ds 客服單：年度名單與報稅匯出 卡住"
	dedupePrompt = "305-qa 年度報稅匯出又卡住了"
)

func TestRecallExcludeKeepsRatioBase(t *testing.T) {
	d := testDB(t)
	d.Store(&Memory{Type: "til", Project: "x", Content: dedupeTop})
	d.Store(&Memory{Type: "til", Project: "x", Content: dedupeSecond})
	addNoise(t, d)

	cands, err := d.recallCandidates(context.Background(), RecallOptions{Prompt: dedupePrompt})
	if err != nil {
		t.Fatal(err)
	}
	score := map[int64]RecallResult{}
	for _, c := range cands {
		score[c.ID] = c
	}
	ratio := score[2].Score / score[1].Score
	if ratio < 0.5 || ratio >= recallMinRatio || score[2].Score < recallMinScore || score[2].Units < recallMinUnits {
		t.Fatalf("fixture precondition: ratio=%.2f second=%+v", ratio, score[2])
	}

	if ids := recallIDs(t, d, RecallOptions{Prompt: dedupePrompt}); !reflect.DeepEqual(ids, []int64{1}) {
		t.Errorf("first: got %v, want [1]", ids)
	}
	if ids := recallIDs(t, d, RecallOptions{Prompt: dedupePrompt, Exclude: map[int64]bool{1: true}}); len(ids) != 0 {
		t.Errorf("after excluding #1: got %v, want none", ids)
	}
}

func TestRecallTermsForQuota(t *testing.T) {
	many := func(prefix string, n int) string {
		var b strings.Builder
		for i := 0; i < n; i++ {
			fmt.Fprintf(&b, "%s%03d ", prefix, i)
		}
		return b.String()
	}
	count := func(terms []string, prefix string) int {
		n := 0
		for _, x := range terms {
			if strings.HasPrefix(x, prefix) {
				n++
			}
		}
		return n
	}
	// 兩邊都多：各 12，尾段取最後面的詞
	got := recallTermsFor("", []string{many("hh", 40), many("tt", 40)})
	if len(got) != 24 || count(got, "hh") != 12 || count(got, "tt") != 12 || got[0] != "hh000" || got[23] != "tt039" {
		t.Errorf("both sides full: %v", got)
	}
	// 頭段只有 3 個：剩下名額給尾段
	got = recallTermsFor("", []string{many("hh", 3), many("tt", 40)})
	if count(got, "hh") != 3 || count(got, "tt") != 21 {
		t.Errorf("short head: %v", got)
	}
	// 尾段只有 2 個：剩下名額給頭段
	got = recallTermsFor("", []string{many("hh", 40), many("tt", 2)})
	if count(got, "hh") != 22 || count(got, "tt") != 2 {
		t.Errorf("short tail: %v", got)
	}
	// 未截斷：與 RecallTerms 相同
	p := many("xx", 40)
	if !reflect.DeepEqual(recallTermsFor(p, nil), RecallTerms(p)) {
		t.Error("unsplit prompt should keep RecallTerms behavior")
	}
}
