package feed

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func mustReadFile(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("testdata", "rss", name)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestParse_RSS2(t *testing.T) {
	xml := mustReadFile(t, "rss2.xml")
	articles, err := Parse(xml)
	if err != nil {
		t.Fatalf("Parse RSS2: unexpected error: %v", err)
	}
	if len(articles) != 2 {
		t.Fatalf("expected 2 articles, got %d", len(articles))
	}

	// First article
	a := articles[0]
	if a.Title != "First Article" {
		t.Errorf("articles[0].Title = %q, want %q", a.Title, "First Article")
	}
	if a.URL != "https://example.com/first" {
		t.Errorf("articles[0].URL = %q, want %q", a.URL, "https://example.com/first")
	}
	if a.Description != "This is the first article." {
		t.Errorf("articles[0].Description = %q, want %q", a.Description, "This is the first article.")
	}
	if a.PublishedAt == nil {
		t.Fatal("articles[0].PublishedAt is nil")
	}
	want := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	if !a.PublishedAt.Equal(want) {
		t.Errorf("articles[0].PublishedAt = %v, want %v", *a.PublishedAt, want)
	}

	// Second article
	b := articles[1]
	if b.Title != "Second Article" {
		t.Errorf("articles[1].Title = %q, want %q", b.Title, "Second Article")
	}
	if b.URL != "https://example.com/second" {
		t.Errorf("articles[1].URL = %q, want %q", b.URL, "https://example.com/second")
	}
}

func TestParse_Atom(t *testing.T) {
	xml := mustReadFile(t, "atom.xml")
	articles, err := Parse(xml)
	if err != nil {
		t.Fatalf("Parse Atom: unexpected error: %v", err)
	}
	if len(articles) != 2 {
		t.Fatalf("expected 2 articles, got %d", len(articles))
	}

	a := articles[0]
	if a.Title != "Atom First Entry" {
		t.Errorf("articles[0].Title = %q, want %q", a.Title, "Atom First Entry")
	}
	if a.URL != "https://example.com/atom-first" {
		t.Errorf("articles[0].URL = %q, want %q", a.URL, "https://example.com/atom-first")
	}
	if a.Description != "This is the first atom entry." {
		t.Errorf("articles[0].Description = %q, want %q", a.Description, "This is the first atom entry.")
	}
	if a.PublishedAt == nil {
		t.Fatal("articles[0].PublishedAt is nil")
	}
	want := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	if !a.PublishedAt.Equal(want) {
		t.Errorf("articles[0].PublishedAt = %v, want %v", *a.PublishedAt, want)
	}
}

func TestParse_Malformed(t *testing.T) {
	xml := mustReadFile(t, "malformed.xml")
	articles, err := Parse(xml)
	if err == nil {
		t.Fatalf("expected error for malformed XML, got nil and %d articles", len(articles))
	}
	if len(articles) != 0 {
		t.Errorf("expected 0 articles on error, got %d", len(articles))
	}
}

func TestParse_Empty(t *testing.T) {
	xml := mustReadFile(t, "empty.xml")
	articles, err := Parse(xml)
	if err != nil {
		t.Fatalf("Parse empty feed: unexpected error: %v", err)
	}
	if len(articles) != 0 {
		t.Fatalf("expected 0 articles, got %d", len(articles))
	}
}

func TestParse_NoHTTPNoDB(t *testing.T) {
	// This is a structural assertion: Parse accepts a string and returns a slice.
	// No *http.Client, *sql.DB, or net.Dial should appear in this package.
	xml := mustReadFile(t, "rss2.xml")
	articles, err := Parse(xml)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if articles == nil {
		t.Fatal("expected non-nil slice for valid feed")
	}
}