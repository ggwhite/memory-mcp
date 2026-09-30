package db

import "testing"

func TestSearch(t *testing.T) {
	d := testDB(t)
	d.Store(&Memory{Type: "til", Content: "SQLite FTS5 full text search", Tags: "sqlite,search"})
	d.Store(&Memory{Type: "feedback", Content: "always use real database for testing", Tags: "testing"})
	d.Store(&Memory{Type: "til", Content: "Go error handling best practices", Tags: "go"})

	results, err := d.Search(SearchOptions{Query: "database testing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected results for 'database testing'")
	}
}

func TestSearchTypeFilter(t *testing.T) {
	d := testDB(t)
	d.Store(&Memory{Type: "til", Content: "database connection pooling"})
	d.Store(&Memory{Type: "feedback", Content: "database testing approach"})

	results, err := d.Search(SearchOptions{Query: "database", Type: "til"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("len = %d, want 1", len(results))
	}
	if results[0].Type != "til" {
		t.Fatalf("type = %q, want til", results[0].Type)
	}
}

func TestSearchLimit(t *testing.T) {
	d := testDB(t)
	d.Store(&Memory{Type: "til", Content: "database one"})
	d.Store(&Memory{Type: "til", Content: "database two"})
	d.Store(&Memory{Type: "til", Content: "database three"})

	results, err := d.Search(SearchOptions{Query: "database", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("len = %d, want 2", len(results))
	}
}

func TestSearchNoResults(t *testing.T) {
	d := testDB(t)
	d.Store(&Memory{Type: "til", Content: "something unrelated"})

	results, err := d.Search(SearchOptions{Query: "nonexistent"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("len = %d, want 0", len(results))
	}
}

func TestSearchByTags(t *testing.T) {
	d := testDB(t)
	d.Store(&Memory{Type: "til", Content: "some content", Tags: "docker,kubernetes"})
	d.Store(&Memory{Type: "til", Content: "other content", Tags: "go,testing"})

	results, err := d.Search(SearchOptions{Query: "kubernetes"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("len = %d, want 1", len(results))
	}
}

func TestFTSQuery(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"whitespace only", " \t　 ", ""},
		{"single word", "database", `"database"`},
		{"multiple words", "database testing", `"database" "testing"`},
		{"hyphen", "memory-mcp", `"memory-mcp"`},
		{"double quote escaped", `say "hi"`, `"say" """hi"""`},
		{"operators become literals", "a AND b OR NOT NEAR(c)", `"a" "AND" "b" "OR" "NOT" "NEAR(c)"`},
		{"mixed CJK and English", "memory-mcp 串連 summary", `"memory-mcp" "串連" "summary"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ftsQuery(tt.input); got != tt.want {
				t.Fatalf("ftsQuery(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSearchSpecialCharacters(t *testing.T) {
	d := testDB(t)
	d.Store(&Memory{Type: "summary", Content: "memory-mcp 自動摘要 summary hook 串連"})
	d.Store(&Memory{Type: "til", Content: "config key:value pair"})
	d.Store(&Memory{Type: "til", Content: `a quoted "phrase" here`})
	d.Store(&Memory{Type: "til", Content: "glob foo* pattern"})
	d.Store(&Memory{Type: "til", Content: "call fn(arg) now"})
	d.Store(&Memory{Type: "til", Content: "cats AND dogs OR birds NOT fish NEAR home"})

	tests := []struct {
		query string
		typ   string
		want  int
	}{
		{"memory-mcp", "", 1},
		{"key:value", "", 1},
		{`"phrase"`, "", 1},
		{`unterminated "quote`, "", 0},
		{"foo*", "", 1},
		{"fn(arg)", "", 1},
		{"(", "", 0},
		{")", "", 0},
		{"cats AND dogs", "", 1},
		{"birds OR", "", 1},
		{"NOT fish", "", 1},
		{"NEAR home", "", 1},
		{"memory-mcp 串連 summary hook", "summary", 1},
		{"memory-mcp 自動摘要 hook", "summary", 1},
		{"memory-mcp 自動摘要 hook", "til", 0},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			results, err := d.Search(SearchOptions{Query: tt.query, Type: tt.typ})
			if err != nil {
				t.Fatalf("Search(%q) error: %v", tt.query, err)
			}
			if len(results) != tt.want {
				t.Fatalf("Search(%q) len = %d, want %d", tt.query, len(results), tt.want)
			}
		})
	}
}

func TestSearchKeepsExistingSemantics(t *testing.T) {
	d := testDB(t)
	d.Store(&Memory{Type: "til", Content: "SQLite trigram 支援 CJK 全文搜尋"})
	d.Store(&Memory{Type: "til", Content: "database connection pooling"})

	tests := []struct {
		name  string
		query string
		want  int
	}{
		{"CJK substring", "全文搜尋", 1},
		{"CJK and English", "支援 CJK", 1},
		{"multi keyword all present", "database pooling", 1},
		{"multi keyword is AND", "database trigram", 0},
		{"empty query", "", 0},
		{"whitespace query", "   ", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := d.Search(SearchOptions{Query: tt.query})
			if err != nil {
				t.Fatalf("Search(%q) error: %v", tt.query, err)
			}
			if len(results) != tt.want {
				t.Fatalf("Search(%q) len = %d, want %d", tt.query, len(results), tt.want)
			}
		})
	}
}
