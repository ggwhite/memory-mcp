package db

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEvidenceHashOrderIndependent(t *testing.T) {
	if EvidenceHash([]int64{3, 1, 2}) != EvidenceHash([]int64{1, 2, 3}) {
		t.Error("hash must not depend on order")
	}
	if EvidenceHash([]int64{1, 2}) == EvidenceHash([]int64{1, 2, 3}) {
		t.Error("different evidence must hash differently")
	}
}

func TestSaveAndListObservations(t *testing.T) {
	d := testDB(t)
	ids, err := d.SaveObservations([]Observation{
		{Project: "p", Type: "feedback", Content: "用繁中", Evidence: []int64{2, 1}, Source: "heuristic"},
		{Project: "q", Type: "feedback", Content: "other", Evidence: []int64{5, 6}, Source: "llm:fake"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != 1 || ids[1] != 2 {
		t.Fatalf("ids = %v", ids)
	}
	got, err := d.ListObservations(ObservationFilter{Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got = %+v", got)
	}
	o := got[0]
	if o.ProofCount != 2 || o.EvidenceHash != EvidenceHash([]int64{1, 2}) || !o.Active || o.Source != "heuristic" {
		t.Errorf("observation = %+v", o)
	}
	if len(o.Evidence) != 2 || o.Evidence[0] != 1 || o.Evidence[1] != 2 {
		t.Errorf("evidence = %v, want sorted [1 2]", o.Evidence)
	}

	// 更新：ID 有值時覆寫內容與證據。
	o.Content = "改過"
	o.Evidence = []int64{1, 2, 3}
	if _, err := d.SaveObservations([]Observation{o}); err != nil {
		t.Fatal(err)
	}
	got, _ = d.ListObservations(ObservationFilter{Project: "p"})
	if got[0].Content != "改過" || got[0].ProofCount != 3 {
		t.Errorf("after update = %+v", got[0])
	}

	all, _ := d.ListObservations(ObservationFilter{AllProjects: true})
	if len(all) != 2 {
		t.Errorf("all = %d, want 2", len(all))
	}
}

func TestSaveObservationSanitizes(t *testing.T) {
	d := testDB(t)
	if _, err := d.SaveObservations([]Observation{{Project: "p", Type: "feedback", Content: "key sk-ant-abcdefghijklmnopqrstuvwxyz0123", Evidence: []int64{1, 2}, Source: "heuristic"}}); err != nil {
		t.Fatal(err)
	}
	got, _ := d.ListObservations(ObservationFilter{Project: "p"})
	if strings.Contains(got[0].Content, "abcdefghijklmnop") {
		t.Errorf("content not sanitized: %q", got[0].Content)
	}
}

func TestDeactivateObservation(t *testing.T) {
	d := testDB(t)
	d.SaveObservations([]Observation{{Project: "p", Type: "feedback", Content: "x", Evidence: []int64{1, 2}, Source: "heuristic"}})
	if err := d.DeactivateObservation(1); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.ListObservations(ObservationFilter{Project: "p"}); len(got) != 0 {
		t.Errorf("inactive should be hidden by default: %+v", got)
	}
	got, _ := d.ListObservations(ObservationFilter{Project: "p", IncludeInactive: true})
	if len(got) != 1 || got[0].Active {
		t.Errorf("inactive = %+v", got)
	}
	if err := d.DeactivateObservation(1); err == nil {
		t.Error("deactivating an inactive observation should fail")
	}
	if err := d.DeactivateObservation(99); err == nil {
		t.Error("missing observation should fail")
	}
}

func TestConsolidationCandidates(t *testing.T) {
	d := testDB(t)
	a, _ := d.Store(&Memory{Type: "feedback", Content: "a", Project: "p"})
	d.Store(&Memory{Type: "feedback", Content: "b", Project: "p"})
	d.Store(&Memory{Type: "til", Content: "c", Project: "p"})
	d.Store(&Memory{Type: "feedback", Content: "d", Project: "q"})
	if err := d.upsertEmbedding(a, []float32{1, 0}, "m"); err != nil {
		t.Fatal(err)
	}
	cs, err := d.ConsolidationCandidates("p", "feedback")
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 2 || cs[0].Content != "a" || cs[1].Content != "b" {
		t.Fatalf("candidates = %+v", cs)
	}
	if len(cs[0].Vector) != 2 || cs[0].Model != "m" || cs[1].Vector != nil {
		t.Errorf("vectors = %+v / %+v", cs[0], cs[1])
	}
	projects, err := d.MemoryProjects("feedback")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(projects, ",") != "p,q" {
		t.Errorf("projects = %v", projects)
	}
}

// seedGoldenContext 用固定 created 建立記憶，讓 context 輸出可以逐 byte 比對。
func seedGoldenContext(t *testing.T, d *DB) {
	t.Helper()
	day := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, m := range []Memory{
		{Type: "feedback", Content: "global rule", Project: "global"},
		{Type: "feedback", Content: "回覆用英文", Project: "p"},
		{Type: "feedback", Content: "回覆一律用英文", Project: "p"},
		{Type: "summary", Content: "did things", Project: "p"},
		{Type: "til", Content: "a til", Project: "p"},
	} {
		m.Created = day
		if _, err := d.StoreMerged(&m, nil); err != nil {
			t.Fatal(err)
		}
	}
}

// goldenContext 是加入 observations 之前 projectContext 對 seedGoldenContext 的輸出。
const goldenContext = "## Memory context（project: p）\n\n" +
	"### 全域 feedback\n- #1 feedback 2026-01-02 global rule\n\n" +
	"### p feedback\n- #3 feedback 2026-01-02 回覆一律用英文\n- #2 feedback 2026-01-02 回覆用英文\n\n" +
	"### p summary\n- #4 summary 2026-01-02 did things\n\n" +
	"### p 其他記憶\n- #5 til 2026-01-02 a til\n\n" +
	"全文用 memory_get(ids)，前後脈絡用 memory_timeline(id)。\n" +
	"(約 98 tokens)\n"

func TestProjectContextUnchangedWithoutObservations(t *testing.T) {
	d := testDB(t)
	seedGoldenContext(t, d)
	out, err := d.Context(ContextOptions{Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if out != goldenContext {
		t.Fatalf("empty observations table changed context:\n got %q\nwant %q", out, goldenContext)
	}

	// 已停用的 observation 也不能影響輸出。
	d.SaveObservations([]Observation{{Project: "p", Type: "feedback", Content: "用英文", Evidence: []int64{2, 3}, Source: "heuristic"}})
	if err := d.DeactivateObservation(1); err != nil {
		t.Fatal(err)
	}
	out, _ = d.Context(ContextOptions{Project: "p"})
	if out != goldenContext {
		t.Fatalf("inactive observation changed context:\n got %q\nwant %q", out, goldenContext)
	}
}

func TestProjectContextObservations(t *testing.T) {
	d := testDB(t)
	seedGoldenContext(t, d)
	d.SaveObservations([]Observation{
		{Project: "p", Type: "feedback", Content: "回覆一律用英文", Evidence: []int64{3, 2}, Source: "heuristic"},
		{Project: "global", Type: "feedback", Content: strings.Repeat("長", 200), Evidence: []int64{1, 99}, Source: "llm:fake"},
	})
	out, err := d.Context(ContextOptions{Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	wantLine := "- o1 ×2 回覆一律用英文 (ev: #2 #3)"
	if !strings.Contains(out, "### p observations\n"+wantLine+"\n") {
		t.Errorf("missing project observation line %q:\n%s", wantLine, out)
	}
	if !strings.Contains(out, "### 全域 observations\n- o2 ×2 "+strings.Repeat("長", contextSnippetRunes)+"… (ev: #1 #99)\n") {
		t.Errorf("global observation should be snippet-trimmed:\n%s", out)
	}
	for _, hidden := range []string{"- #1 ", "- #2 ", "- #3 "} {
		if strings.Contains(out, hidden) {
			t.Errorf("evidence line %q should be hidden:\n%s", hidden, out)
		}
	}
	if strings.Contains(out, "### p feedback") || strings.Contains(out, "### 全域 feedback") {
		t.Errorf("feedback sections should be empty once all lines are evidence:\n%s", out)
	}
	for _, keep := range []string{"- #4 summary", "- #5 til"} {
		if !strings.Contains(out, keep) {
			t.Errorf("missing %q:\n%s", keep, out)
		}
	}
	gIdx, pIdx, sIdx := strings.Index(out, "### 全域 observations"), strings.Index(out, "### p observations"), strings.Index(out, "### p summary")
	if gIdx >= pIdx || pIdx >= sIdx {
		t.Errorf("observations should be rendered first (global, project):\n%s", out)
	}

	// 停用後證據行回來。
	d.DeactivateObservation(1)
	out, _ = d.Context(ContextOptions{Project: "p"})
	if !strings.Contains(out, "- #2 feedback") || !strings.Contains(out, "- #3 feedback") || strings.Contains(out, "o1 ") {
		t.Errorf("deactivated observation should restore evidence lines:\n%s", out)
	}
}

// 被隱藏的證據不能吃掉段落上限：feedback 段仍顯示最新 20 筆非證據記憶。
func TestProjectContextHiddenEvidenceKeepsLimit(t *testing.T) {
	d := testDB(t)
	for i := 0; i < 25; i++ {
		d.Store(&Memory{Type: "feedback", Content: "fb", Project: "p"})
	}
	d.SaveObservations([]Observation{{Project: "p", Type: "feedback", Content: "fb", Evidence: []int64{25, 24, 23}, Source: "heuristic"}})
	out, _ := d.Context(ContextOptions{Project: "p"})
	if n := sectionLines(out, "### p feedback"); n != 20 {
		t.Errorf("feedback lines = %d, want 20:\n%s", n, out)
	}
}

func TestObservationsMigrationOnOldDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.db.Exec(`DROP TABLE observations`); err != nil {
		t.Fatal(err)
	}
	d.Close()
	d2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()
	if _, err := d2.ListObservations(ObservationFilter{AllProjects: true}); err != nil {
		t.Errorf("reopened DB should have observations table: %v", err)
	}
}
