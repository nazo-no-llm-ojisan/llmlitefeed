package storage

import (
	"testing"
	"time"
)

func openTestDB(t *testing.T) (Store, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatalf("Open(%q) returned error: %v", dir, err)
	}
	t.Cleanup(func() { store.Close() })
	return store, dir
}

func TestOpen_creates_tables(t *testing.T) {
	store, path := openTestDB(t)
	_ = path

	db := store.(*sqliteStore).db
	tables := []string{"sources", "articles"}
	for _, name := range tables {
		var count int
		err := db.QueryRow(
			"SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", name,
		).Scan(&count)
		if err != nil {
			t.Fatalf("query for table %q: %v", name, err)
		}
		if count != 1 {
			t.Errorf("table %q: expected 1, got %d", name, count)
		}
	}
}

func TestSource_Insert_and_GetByURL(t *testing.T) {
	store, _ := openTestDB(t)

	id, err := store.Sources().Insert("Test Source", "https://example.com/rss")
	if err != nil {
		t.Fatalf("Insert returned error: %v", err)
	}
	if id != 1 {
		t.Errorf("first Insert id = %d, want 1", id)
	}

	src, err := store.Sources().GetByURL("https://example.com/rss")
	if err != nil {
		t.Fatalf("GetByURL returned error: %v", err)
	}
	if src.Name != "Test Source" {
		t.Errorf("src.Name = %q, want %q", src.Name, "Test Source")
	}
	if src.URL != "https://example.com/rss" {
		t.Errorf("src.URL = %q, want %q", src.URL, "https://example.com/rss")
	}
}

func TestSource_Insert_duplicate_URL(t *testing.T) {
	store, _ := openTestDB(t)

	_, err := store.Sources().Insert("First", "https://example.com/dup")
	if err != nil {
		t.Fatalf("first Insert returned error: %v", err)
	}

	_, err = store.Sources().Insert("Second", "https://example.com/dup")
	if err == nil {
		t.Error("expected error on duplicate URL, got nil")
	}
}

func TestSource_List(t *testing.T) {
	store, _ := openTestDB(t)

	store.Sources().Insert("A", "https://a.example.com/rss")
	store.Sources().Insert("B", "https://b.example.com/rss")

	srcs, err := store.Sources().List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(srcs) != 2 {
		t.Errorf("List len = %d, want 2", len(srcs))
	}
}

func TestSource_Delete(t *testing.T) {
	store, _ := openTestDB(t)

	id, _ := store.Sources().Insert("ToDelete", "https://del.example.com/rss")

	if err := store.Sources().Delete(id); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}

	_, err := store.Sources().GetByURL("https://del.example.com/rss")
	if err == nil {
		t.Error("expected error after Delete, got nil")
	}
}

func TestSource_GetByURL_invalid_timestamp(t *testing.T) {
	store, dir := openTestDB(t)
	db := store.(*sqliteStore).db

	// 不正なタイムスタンプで直接 INSERT
	_, err := db.Exec(
		"INSERT INTO sources (name, url, created_at, updated_at) VALUES (?, ?, ?, ?)",
		"Bad", "https://bad.example.com/rss", "not-a-timestamp", "also-not-a-timestamp",
	)
	if err != nil {
		t.Fatalf("direct insert failed: %v", err)
	}
	_ = dir

	_, err = store.Sources().GetByURL("https://bad.example.com/rss")
	if err == nil {
		t.Error("expected error for invalid created_at, got nil")
	}
}

func TestSource_List_invalid_timestamp(t *testing.T) {
	store, _ := openTestDB(t)
	db := store.(*sqliteStore).db

	// 不正な created_at で行を直接挿入
	_, err := db.Exec(
		"INSERT INTO sources (name, url, created_at, updated_at) VALUES (?, ?, ?, ?)",
		"Bad2", "https://bad2.example.com/rss", "garbage", "2026-07-14T10:00:00Z",
	)
	if err != nil {
		t.Fatalf("direct insert failed: %v", err)
	}

	_, err = store.Sources().List()
	if err == nil {
		t.Error("expected error for invalid created_at in List, got nil")
	}
}

func TestArticle_Insert_and_List(t *testing.T) {
	store, _ := openTestDB(t)

	srcID, _ := store.Sources().Insert("Test", "https://src.example.com/rss")

	ts1 := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	ts2 := time.Date(2026, 7, 14, 11, 0, 0, 0, time.UTC)

	a1 := Article{
		SourceID:      srcID,
		CanonicalURL:  "https://src.example.com/a",
		CanonicalHash: "hash-a",
		Title:         "Article A",
		PublishedAt:   &ts1,
		FetchedAt:     ts1,
	}
	a2 := Article{
		SourceID:      srcID,
		CanonicalURL:  "https://src.example.com/b",
		CanonicalHash: "hash-b",
		Title:         "Article B",
		PublishedAt:   &ts2,
		FetchedAt:     ts2,
	}

	id1, err := store.Articles().Insert(a1)
	if err != nil {
		t.Fatalf("Insert a1 returned error: %v", err)
	}
	if id1 != 1 {
		t.Errorf("id1 = %d, want 1", id1)
	}

	id2, err := store.Articles().Insert(a2)
	if err != nil {
		t.Fatalf("Insert a2 returned error: %v", err)
	}
	if id2 != 2 {
		t.Errorf("id2 = %d, want 2", id2)
	}

	articles, err := store.Articles().List(10, 0)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(articles) != 2 {
		t.Fatalf("List len = %d, want 2", len(articles))
	}

	// fetched_at DESC: B should come first
	if articles[0].Title != "Article B" {
		t.Errorf("articles[0].Title = %q, want %q", articles[0].Title, "Article B")
	}
	if articles[1].Title != "Article A" {
		t.Errorf("articles[1].Title = %q, want %q", articles[1].Title, "Article A")
	}
}

func TestArticle_Insert_duplicate_hash(t *testing.T) {
	store, _ := openTestDB(t)
	srcID, _ := store.Sources().Insert("Test", "https://src.example.com/rss")

	ts := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	a1 := Article{
		SourceID: srcID, CanonicalURL: "https://src.example.com/a",
		CanonicalHash: "same-hash", Title: "First", FetchedAt: ts,
	}
	store.Articles().Insert(a1)

	// ExistsByHash
	exists, err := store.Articles().ExistsByHash("same-hash")
	if err != nil {
		t.Fatalf("ExistsByHash returned error: %v", err)
	}
	if !exists {
		t.Error("ExistsByHash returned false, want true")
	}

	exists, err = store.Articles().ExistsByHash("no-such-hash")
	if err != nil {
		t.Fatalf("ExistsByHash for missing returned error: %v", err)
	}
	if exists {
		t.Error("ExistsByHash returned true for unknown hash, want false")
	}

	// duplicate Insert
	a2 := Article{
		SourceID: srcID, CanonicalURL: "https://src.example.com/a2",
		CanonicalHash: "same-hash", Title: "Second", FetchedAt: ts,
	}
	_, err = store.Articles().Insert(a2)
	if err == nil {
		t.Error("expected error on duplicate hash, got nil")
	}
}

func TestArticle_ListBySource(t *testing.T) {
	store, _ := openTestDB(t)

	src1, _ := store.Sources().Insert("Src1", "https://one.example.com/rss")
	src2, _ := store.Sources().Insert("Src2", "https://two.example.com/rss")

	ts := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	store.Articles().Insert(Article{
		SourceID: src1, CanonicalURL: "https://one.example.com/a",
		CanonicalHash: "h1", Title: "One", FetchedAt: ts,
	})
	store.Articles().Insert(Article{
		SourceID: src2, CanonicalURL: "https://two.example.com/a",
		CanonicalHash: "h2", Title: "Two", FetchedAt: ts,
	})

	articles, err := store.Articles().ListBySource(src1)
	if err != nil {
		t.Fatalf("ListBySource returned error: %v", err)
	}
	if len(articles) != 1 {
		t.Fatalf("ListBySource len = %d, want 1", len(articles))
	}
	if articles[0].Title != "One" {
		t.Errorf("articles[0].Title = %q, want %q", articles[0].Title, "One")
	}
}

func TestArticle_fetched_at_DESC_order(t *testing.T) {
	store, _ := openTestDB(t)
	srcID, _ := store.Sources().Insert("Test", "https://src.example.com/rss")

	ts := func(h int) time.Time {
		return time.Date(2026, 7, 14, h, 0, 0, 0, time.UTC)
	}

	store.Articles().Insert(Article{
		SourceID: srcID, CanonicalURL: "https://a.example.com/1",
		CanonicalHash: "hash-1", Title: "Oldest", FetchedAt: ts(9),
	})
	store.Articles().Insert(Article{
		SourceID: srcID, CanonicalURL: "https://a.example.com/2",
		CanonicalHash: "hash-2", Title: "Middle", FetchedAt: ts(10),
	})
	store.Articles().Insert(Article{
		SourceID: srcID, CanonicalURL: "https://a.example.com/3",
		CanonicalHash: "hash-3", Title: "Newest", FetchedAt: ts(11),
	})

	articles, _ := store.Articles().List(10, 0)
	if len(articles) != 3 {
		t.Fatalf("len = %d, want 3", len(articles))
	}
	if articles[0].Title != "Newest" {
		t.Errorf("[0] = %q, want Newest", articles[0].Title)
	}
	if articles[1].Title != "Middle" {
		t.Errorf("[1] = %q, want Middle", articles[1].Title)
	}
	if articles[2].Title != "Oldest" {
		t.Errorf("[2] = %q, want Oldest", articles[2].Title)
	}
}

func TestOpen_sets_pragmas(t *testing.T) {
	store, _ := openTestDB(t)
	db := store.(*sqliteStore).db

	tests := []struct {
		pragma string
		want   string
	}{
		{"foreign_keys", "1"},
		{"journal_mode", "wal"},
		{"busy_timeout", "5000"},
	}
	for _, tt := range tests {
		var got string
		err := db.QueryRow("PRAGMA " + tt.pragma).Scan(&got)
		if err != nil {
			t.Fatalf("PRAGMA %s: %v", tt.pragma, err)
		}
		if got != tt.want {
			t.Errorf("PRAGMA %s = %q, want %q", tt.pragma, got, tt.want)
		}
	}
}

func TestArticle_Insert_invalid_source_id_rejected(t *testing.T) {
	store, _ := openTestDB(t)

	ts := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	a := Article{
		SourceID:      999,
		CanonicalURL:  "https://example.com/orphan",
		CanonicalHash: "orphan-1",
		Title:         "Orphan",
		FetchedAt:     ts,
	}
	_, err := store.Articles().Insert(a)
	if err == nil {
		t.Error("expected foreign key error for invalid source_id, got nil")
	}
}

func TestArticle_fetched_at_order_with_timezones(t *testing.T) {
	store, _ := openTestDB(t)
	srcID, _ := store.Sources().Insert("Test", "https://src.example.com/rss")

	jst := time.FixedZone("JST", 9*60*60)

	t3 := time.Date(2026, 7, 14, 3, 0, 0, 0, time.UTC) // 03:00Z
	t4 := time.Date(2026, 7, 14, 13, 0, 0, 0, jst)     // 13:00 JST = 04:00Z

	store.Articles().Insert(Article{
		SourceID: srcID, CanonicalURL: "https://a.example.com/1",
		CanonicalHash: "tz-1", Title: "Earlier", FetchedAt: t3,
	})
	store.Articles().Insert(Article{
		SourceID: srcID, CanonicalURL: "https://a.example.com/2",
		CanonicalHash: "tz-2", Title: "Later", FetchedAt: t4,
	})

	articles, _ := store.Articles().List(10, 0)
	if len(articles) != 2 {
		t.Fatalf("len = %d, want 2", len(articles))
	}
	if articles[0].Title != "Later" {
		t.Errorf("[0] = %q, want Later", articles[0].Title)
	}
	if articles[1].Title != "Earlier" {
		t.Errorf("[1] = %q, want Earlier", articles[1].Title)
	}
}
