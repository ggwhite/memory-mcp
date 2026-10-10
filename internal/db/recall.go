package db

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// RecallOptions 依單則訊息內容檢索相關記憶的參數。
type RecallOptions struct {
	Prompt          string
	Parts           []string       // Prompt 是截斷後的頭尾拼接時，分別是頭、尾兩段
	Project         string         // 目前 session 的 project，同 project 排序小幅加權
	ExcludePersonal bool           // 排除 personal-* project
	Exclude         map[int64]bool // 已注入過的 id
	Limit           int            // default 3
}

// RecallResult 一筆檢索結果。Score 為 -bm25 加上 2 字 CJK 詞命中加分（越大越相關）；
// Units 為訊息中有幾個「互不相連的片段」出現在這筆記憶裡（英數詞各算一個，
// CJK 段裡被命中窗口覆蓋的連續字元算一個）。
type RecallResult struct {
	Memory
	Score float64
	Units int
}

const (
	recallDefaultLimit = 3
	recallCandidates   = 100
	recallMaxTerms     = 24
	// 門檻用真實 DB 樣本校準，寧可漏不可亂給：
	// 分數要過 recallMinScore、至少命中訊息中兩個獨立片段，且不低於第一名的 recallMinRatio。
	recallMinScore = 10.0
	recallMinUnits = 2
	recallMinRatio = 0.6
	// recallBigramWeight 每個命中的 2 字 CJK 詞（不在已命中的 3 字詞內）加的分數。
	recallBigramWeight = 2.0
	// recallProjectBoost 同 project 的排序加權，只影響排序、不影響門檻。
	recallProjectBoost = 1.15
)

// recallStopBigrams 窗口含其中任一字串就丟掉：高頻虛詞、口語贅字。
var recallStopBigrams = []string{
	"怎麼", "怎么", "怎樣", "什麼", "什么", "為什", "为什", "是不", "不是", "可以", "一下",
	"如何", "今天", "明天", "昨天", "這個", "这个", "那個", "那个", "我們", "我们", "你們",
	"他們", "一個", "一个", "上次", "之前", "之後", "以後", "然後", "現在", "现在", "還是",
	"还是", "就是", "已經", "已经", "的話", "有沒", "沒有", "没有", "要不", "能不", "幫我",
	"帮我", "請問", "一樣", "這樣", "那樣", "這邊", "那邊", "這裡", "那裡", "這段", "為何",
	"知道", "覺得", "應該", "看看", "自己", "直接", "一直", "問題", "原因", "東西", "時候",
	"有點", "順便", "檢查", "說明",
	"嗎", "吗", "呢", "吧", "啊", "喔", "哦", "呀", "嘛", "了", "的", "又", "請", "幫",
}

// recallStopWords 英數詞元停用詞。
var recallStopWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "this": true, "that": true,
	"what": true, "how": true, "why": true, "can": true, "you": true, "are": true,
	"was": true, "did": true, "does": true, "not": true, "from": true, "have": true,
	"please": true, "about": true, "into": true, "there": true, "then": true,
}

// recallSegment 訊息切出的一段：英數詞（含內部 -_.）或 CJK 連續段。
type recallSegment struct {
	cjk   bool
	runes []rune
}

// recallSegments 把訊息切成英數連續段與 CJK 連續段（轉小寫），其他字元當分隔。
func recallSegments(prompt string) []recallSegment {
	var segs []recallSegment
	runes := []rune(strings.ToLower(prompt))
	for i := 0; i < len(runes); {
		switch r := runes[i]; {
		case isCJK(r):
			j := i
			for j < len(runes) && isCJK(runes[j]) {
				j++
			}
			segs = append(segs, recallSegment{cjk: true, runes: runes[i:j]})
			i = j
		case isWordRune(r):
			j := i
			for j < len(runes) && (isWordRune(runes[j]) || isJoiner(runes[j])) {
				j++
			}
			w := strings.Trim(string(runes[i:j]), "-_.")
			if utf8.RuneCountInString(w) >= 3 && !recallStopWords[w] {
				segs = append(segs, recallSegment{runes: []rune(w)})
			}
			i = j
		default:
			i++
		}
	}
	return segs
}

// windows 回傳 CJK 段中長度 n、不含停用詞的窗口起點。
func (s recallSegment) windows(n int) []int {
	var out []int
	for k := 0; k+n <= len(s.runes); k++ {
		if !hasStopBigram(string(s.runes[k : k+n])) {
			out = append(out, k)
		}
	}
	return out
}

// RecallTerms 回傳拿去 FTS5 trigram MATCH 的查詢詞：英數段 ≥3 字元直接用，
// CJK 段取所有不含停用詞的 3 字窗口。2 字 CJK 詞 trigram 無法 MATCH，不在此列。
func RecallTerms(prompt string) []string {
	return termsOf(prompt, recallMaxTerms)
}

// termsOf 依出現順序切出不重複的查詢詞，最多 limit 個。
func termsOf(prompt string, limit int) []string {
	var terms []string
	seen := make(map[string]bool)
	add := func(t string) {
		if !seen[t] && len(terms) < limit {
			seen[t] = true
			terms = append(terms, t)
		}
	}
	for _, s := range recallSegments(prompt) {
		if !s.cjk {
			add(string(s.runes))
			continue
		}
		for _, k := range s.windows(3) {
			add(string(s.runes[k : k+3]))
		}
	}
	return terms
}

// recallTermsFor 未截斷時同 RecallTerms；截斷過（parts 為頭、尾兩段）時名額頭尾各半，
// 一邊用不完的名額給另一邊，避免多樣性高的 log 把另一端的提問擠掉。頭段取最前面的詞、
// 尾段取最後面的詞（離訊息兩端最近的地方最可能是提問）。
func recallTermsFor(prompt string, parts []string) []string {
	if len(parts) != 2 {
		return RecallTerms(prompt)
	}
	half := recallMaxTerms / 2
	head := termsOf(parts[0], recallMaxTerms)
	nh0 := min(len(head), half)
	inHead := make(map[string]bool, nh0)
	for _, t := range head[:nh0] {
		inHead[t] = true
	}
	var tail []string
	for _, t := range termsOf(parts[1], recallMaxTerms*2) {
		if !inHead[t] {
			tail = append(tail, t)
		}
	}
	nt := min(len(tail), recallMaxTerms-nh0)
	nh := min(len(head), recallMaxTerms-nt)

	out := make([]string, 0, recallMaxTerms)
	seen := make(map[string]bool)
	for _, t := range append(head[:nh:nh], tail[len(tail)-nt:]...) {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// scoreUnits 計算一筆記憶命中了訊息中幾個獨立片段，並回傳 2 字詞加分。
// 2 字詞只在已撈到的候選內做子字串比對，不用來撈候選。
func scoreUnits(segs []recallSegment, hay string) (units int, bonus float64) {
	for _, s := range segs {
		if !s.cjk {
			if strings.Contains(hay, string(s.runes)) {
				units++
			}
			continue
		}
		covered := make([]bool, len(s.runes))
		for _, k := range s.windows(3) {
			if strings.Contains(hay, string(s.runes[k:k+3])) {
				covered[k], covered[k+1], covered[k+2] = true, true, true
			}
		}
		for _, k := range s.windows(2) {
			if covered[k] && covered[k+1] {
				continue
			}
			if strings.Contains(hay, string(s.runes[k:k+2])) {
				covered[k], covered[k+1] = true, true
				bonus += recallBigramWeight
			}
		}
		for k, c := range covered {
			if c && (k == 0 || !covered[k-1]) {
				units++
			}
		}
	}
	return units, bonus
}

func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r)
}

func isWordRune(r rune) bool {
	return r < 0x2E80 && (unicode.IsLetter(r) || unicode.IsDigit(r))
}

func isJoiner(r rune) bool {
	return r == '-' || r == '_' || r == '.'
}

func hasStopBigram(w string) bool {
	for _, s := range recallStopBigrams {
		if strings.Contains(w, s) {
			return true
		}
	}
	return false
}

// Recall 以 FTS5 trigram OR 查詢撈候選、依 bm25（加 2 字詞加分）排序，只回過門檻的
// 前 Limit 筆。不呼叫 embedding 服務；ctx 逾時會中斷查詢。
func (d *DB) Recall(ctx context.Context, opts RecallOptions) ([]RecallResult, error) {
	cands, err := d.recallCandidates(ctx, opts)
	if err != nil {
		return nil, err
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = recallDefaultLimit
	}
	// 比例門檻以排除已注入 id 之前的最高分為基準，避免第一名被去重後次名自己達標。
	var top float64
	for _, c := range cands {
		top = max(top, c.Score)
	}
	var out []RecallResult
	for _, c := range cands {
		if opts.Exclude[c.ID] {
			continue
		}
		if c.Score >= recallMinScore && c.Units >= recallMinUnits && c.Score >= top*recallMinRatio {
			out = append(out, c)
		}
	}
	rankScore := func(c RecallResult) float64 {
		if opts.Project != "" && c.Project == opts.Project {
			return c.Score * recallProjectBoost
		}
		return c.Score
	}
	sort.SliceStable(out, func(i, j int) bool { return rankScore(out[i]) > rankScore(out[j]) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// recallCandidates 回傳未套門檻的候選（已排除 personal，未排除已注入 id），依 bm25 排序。
func (d *DB) recallCandidates(ctx context.Context, opts RecallOptions) ([]RecallResult, error) {
	terms := recallTermsFor(opts.Prompt, opts.Parts)
	if len(terms) == 0 {
		return nil, nil
	}
	segs := recallSegments(opts.Prompt)
	phrases := make([]string, len(terms))
	for i, t := range terms {
		phrases[i] = ftsQuery(t)
	}
	query := `
		SELECT m.id, m.type, m.content, m.tags, m.project, m.created, m.updated, f.rank
		FROM memories_fts f
		JOIN memories m ON m.id = f.rowid
		WHERE memories_fts MATCH ?`
	args := []any{strings.Join(phrases, " OR ")}
	if opts.ExcludePersonal {
		query += ` AND m.project NOT LIKE ?`
		args = append(args, personalProjectPrefix+"%")
	}
	query += ` ORDER BY f.rank LIMIT ?`
	args = append(args, recallCandidates+len(opts.Exclude))

	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("recall: %w", err)
	}
	defer rows.Close()

	var out []RecallResult
	for rows.Next() {
		var r RecallResult
		var created, updated string
		var rank float64
		if err := rows.Scan(&r.ID, &r.Type, &r.Content, &r.Tags, &r.Project, &created, &updated, &rank); err != nil {
			return nil, fmt.Errorf("recall scan: %w", err)
		}
		r.Created, _ = time.Parse(timeLayout, created)
		r.Updated, _ = time.Parse(timeLayout, updated)
		var bonus float64
		r.Units, bonus = scoreUnits(segs, strings.ToLower(r.Content+"\n"+r.Tags+"\n"+r.Project))
		r.Score = -rank + bonus
		out = append(out, r)
	}
	return out, rows.Err()
}

// PromptTerms 訊息切出的查詢詞，供比對短字串（例如筆記檔名）用。
type PromptTerms struct {
	strong []string // 英數詞、CJK 3 字窗口
	weak   []string // CJK 2 字窗口
}

// NewPromptTerms 依 recall 的切詞規則切出查詢詞；parts 意義同 RecallOptions.Parts。
func NewPromptTerms(prompt string, parts ...string) PromptTerms {
	pt := PromptTerms{strong: recallTermsFor(prompt, parts)}
	seen := make(map[string]bool)
	for _, s := range recallSegments(prompt) {
		if !s.cjk {
			continue
		}
		for _, k := range s.windows(2) {
			if w := string(s.runes[k : k+2]); !seen[w] {
				seen[w] = true
				pt.weak = append(pt.weak, w)
			}
		}
	}
	return pt
}

// Empty 沒有任何查詢詞。
func (pt PromptTerms) Empty() bool {
	return len(pt.strong) == 0 && len(pt.weak) == 0
}

// Coverage 回傳 target（不分大小寫）被查詢詞覆蓋的字元數：strong 只算 3 字以上的詞，
// all 另含 2 字 CJK 詞；n 為 target 的字元數。
func (pt PromptTerms) Coverage(target string) (strong, all, n int) {
	t := []rune(strings.ToLower(target))
	covered := make([]int8, len(t)) // 0 未覆蓋、1 只被 2 字詞覆蓋、2 被 3 字以上詞覆蓋
	mark := func(terms []string, level int8) {
		for _, term := range terms {
			tr := []rune(term)
			for i := 0; i+len(tr) <= len(t); i++ {
				if string(t[i:i+len(tr)]) != term {
					continue
				}
				for k := i; k < i+len(tr); k++ {
					covered[k] = max(covered[k], level)
				}
			}
		}
	}
	mark(pt.strong, 2)
	mark(pt.weak, 1)
	for _, c := range covered {
		if c == 2 {
			strong++
		}
		if c > 0 {
			all++
		}
	}
	return strong, all, len(t)
}
