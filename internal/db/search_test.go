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
