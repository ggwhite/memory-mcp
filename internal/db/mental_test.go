package db

import (
	"strings"
	"testing"
)

func TestMentalModelCRUD(t *testing.T) {
	d := testDB(t)
	id, err := d.AddMentalModel("p", "  怎麼部署？ ")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddMentalModel("p", "   "); err == nil {
		t.Error("want error for empty question")
	}
	if _, err := d.AddMentalModel("", "q"); err == nil {
		t.Error("want error for empty project")
	}
	d.AddMentalModel("q", "other")

	ms, err := d.ListMentalModels(MentalModelFilter{Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1 || ms[0].ID != id || ms[0].Question != "怎麼部署？" || ms[0].Answer != "" || len(ms[0].SourceIDs) != 0 {
		t.Fatalf("list = %+v", ms)
	}
	all, _ := d.ListMentalModels(MentalModelFilter{AllProjects: true})
	if len(all) != 2 {
		t.Errorf("all = %d, want 2", len(all))
	}

	m := ms[0]
	m.Answer, m.SourceIDs, m.BuiltFromMaxID, m.Source = "答案 #3", []int64{3, 1, 3}, 7, "llm:fake"
	if err := d.SaveMentalModelAnswer(m); err != nil {
		t.Fatal(err)
	}
	got, _ := d.ListMentalModels(MentalModelFilter{Project: "p"})
	if got[0].Answer != "答案 #3" || len(got[0].SourceIDs) != 2 || got[0].SourceIDs[0] != 1 || got[0].BuiltFromMaxID != 7 || got[0].Source != "llm:fake" {
		t.Errorf("saved = %+v", got[0])
	}

	projects, err := d.MentalModelProjects()
	if err != nil || strings.Join(projects, ",") != "p,q" {
		t.Errorf("projects = %v, %v", projects, err)
	}

	if err := d.DeleteMentalModel(id); err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteMentalModel(id); err == nil {
		t.Error("want error deleting missing model")
	}
	if err := d.SaveMentalModelAnswer(m); err == nil {
		t.Error("want error saving deleted model")
	}
}

func TestMaxMemoryIDAndRecentMemories(t *testing.T) {
	d := testDB(t)
	if n, err := d.MaxMemoryID("p"); err != nil || n != 0 {
		t.Errorf("empty max = %d, %v", n, err)
	}
	d.Store(&Memory{Type: "summary", Content: "s1", Project: "p"})
	d.Store(&Memory{Type: "summary", Content: "s2", Project: "p"})
	d.Store(&Memory{Type: "feedback", Content: "f", Project: "p"})
	d.Store(&Memory{Type: "summary", Content: "other", Project: "q"})
	if n, _ := d.MaxMemoryID("p"); n != 3 {
		t.Errorf("max = %d, want 3", n)
	}
	ms, err := d.RecentMemories("p", "summary", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 || ms[0].Content != "s2" || ms[1].Content != "s1" {
		t.Errorf("recent = %+v", ms)
	}
}

func TestSearchProjectFilter(t *testing.T) {
	d := testDB(t)
	d.Store(&Memory{Type: "til", Content: "deploy with helm", Project: "p"})
	d.Store(&Memory{Type: "til", Content: "deploy with ansible", Project: "q"})
	rs, err := d.Search(SearchOptions{Query: "deploy", Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].Project != "p" {
		t.Errorf("results = %+v", rs)
	}
	rs, _ = d.Search(SearchOptions{Query: "deploy"})
	if len(rs) != 2 {
		t.Errorf("unfiltered = %d, want 2", len(rs))
	}
}

func TestSearchProjectFilterHybrid(t *testing.T) {
	d := testDB(t)
	d.SetEmbedder(&keyedEmbedder{vectors: map[string][]float32{"alpha": {1, 0}, "beta": {1, 0}, "gamma": {1, 0}}})
	d.Store(&Memory{Type: "til", Content: "alpha", Project: "p"})
	d.Store(&Memory{Type: "til", Content: "beta", Project: "q"})
	// 純語意命中：FTS 對不到 "gamma"，向量端要依 project 過濾。
	rs, err := d.Search(SearchOptions{Query: "gamma", Project: "p", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rs {
		if r.Project != "p" {
			t.Errorf("hybrid leaked project %q: %+v", r.Project, rs)
		}
	}
	if len(rs) != 1 {
		t.Errorf("hybrid results = %+v, want only alpha", rs)
	}
}

func TestProjectContextUnchangedWithoutMentalModels(t *testing.T) {
	d := testDB(t)
	seedGoldenContext(t, d)
	// 未 build（answer 空）的 model 與其他 project 的 model 都不能影響輸出。
	d.AddMentalModel("p", "還沒 build")
	id, _ := d.AddMentalModel("other", "q")
	d.SaveMentalModelAnswer(MentalModel{ID: id, Answer: "a", Source: "llm:fake"})
	out, err := d.Context(ContextOptions{Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if out != goldenContext {
		t.Fatalf("mental models changed context:\n got %q\nwant %q", out, goldenContext)
	}
}

func TestProjectContextMentalModels(t *testing.T) {
	d := testDB(t)
	seedGoldenContext(t, d)
	id, _ := d.AddMentalModel("p", "怎麼部署？")
	d.SaveMentalModelAnswer(MentalModel{ID: id, Answer: "先跑測試 #2\n\n" + strings.Repeat("長", 200), SourceIDs: []int64{2}, Source: "llm:fake"})
	out, err := d.Context(ContextOptions{Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	want := "### p mental models\n**Q:** 怎麼部署？\n先跑測試 #2\n" + strings.Repeat("長", contextSnippetRunes) + "…\n\n"
	if !strings.Contains(out, want) {
		t.Fatalf("missing mental model section %q:\n%s", want, out)
	}
	globalIdx := strings.Index(out, "### 全域 feedback")
	mentalIdx := strings.Index(out, "### p mental models")
	feedbackIdx := strings.Index(out, "### p feedback")
	if globalIdx >= mentalIdx || mentalIdx >= feedbackIdx {
		t.Errorf("mental models must sit between global feedback and project feedback:\n%s", out)
	}
}

func TestProjectContextMentalModelsOutliveSummariesInBudget(t *testing.T) {
	d := testDB(t)
	long := strings.Repeat("長", 150)
	for i := 0; i < 10; i++ {
		d.Store(&Memory{Type: "feedback", Content: "g" + long, Project: "global"})
	}
	for i := 0; i < 20; i++ {
		d.Store(&Memory{Type: "feedback", Content: "f" + long, Project: "p"})
	}
	for i := 0; i < 3; i++ {
		d.Store(&Memory{Type: "summary", Content: "s" + long, Project: "p"})
	}
	for i := 0; i < 10; i++ {
		d.Store(&Memory{Type: "knowledge", Content: "k" + long, Project: "p"})
	}
	answer := strings.TrimSuffix(strings.Repeat("a"+long+"\n", 8), "\n")
	for _, q := range []string{"問題一", "問題二"} {
		id, _ := d.AddMentalModel("p", q)
		d.SaveMentalModelAnswer(MentalModel{ID: id, Answer: answer, Source: "llm:fake"})
	}

	out, err := d.Context(ContextOptions{Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if n := blockLines(out, "### p mental models"); n != 18 {
		t.Errorf("mental model lines = %d, want 18 (2 × (Q + 8 answer lines)):\n%s", n, out)
	}
	if n := blockLines(out, "### p 其他記憶"); n != 0 {
		t.Errorf("others should be trimmed first, got %d lines", n)
	}
	if n := blockLines(out, "### p summary"); n >= 3 {
		t.Errorf("summaries should be trimmed before mental models, got %d lines", n)
	}
}

func TestProjectContextMentalModelsSkippedForPersonal(t *testing.T) {
	d := testDB(t)
	d.Store(&Memory{Type: "feedback", Content: "g", Project: "global"})
	id, _ := d.AddMentalModel("personal-x", "q")
	d.SaveMentalModelAnswer(MentalModel{ID: id, Answer: "secret answer", Source: "llm:fake"})
	out, _ := d.Context(ContextOptions{Project: "personal-x"})
	if strings.Contains(out, "secret answer") {
		t.Errorf("personal project mental model leaked:\n%s", out)
	}
}

// blockLines 回傳 title 段落到下一個空行之間的行數；段落不存在（例如被整段裁掉）時回 0。
func blockLines(out, title string) int {
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		if l != title {
			continue
		}
		n := 0
		for _, x := range lines[i+1:] {
			if x == "" {
				break
			}
			n++
		}
		return n
	}
	return 0
}
