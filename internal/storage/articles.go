package storage

import (
	"database/sql"
	"fmt"
	"time"
)

type articleRepo struct {
	db *sql.DB
}

func (r *articleRepo) Insert(a Article) (int64, error) {
	var pubAt *string
	if a.PublishedAt != nil {
		v := a.PublishedAt.UTC().Format(time.RFC3339)
		pubAt = &v
	}
	fetchedAt := a.FetchedAt.UTC().Format(time.RFC3339)
	result, err := r.db.Exec(
		`INSERT INTO articles
		(source_id, canonical_url, canonical_hash, title, published_at, fetched_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		a.SourceID, a.CanonicalURL, a.CanonicalHash, a.Title, pubAt, fetchedAt,
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (r *articleRepo) ExistsByHash(hash string) (bool, error) {
	var count int
	err := r.db.QueryRow("SELECT count(*) FROM articles WHERE canonical_hash = ?", hash).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r *articleRepo) List(limit, offset int) ([]Article, error) {
	rows, err := r.db.Query(
		"SELECT id, source_id, canonical_url, canonical_hash, title, published_at, fetched_at FROM articles ORDER BY fetched_at DESC LIMIT ? OFFSET ?",
		limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanArticles(rows)
}

func (r *articleRepo) ListBySource(sourceID int64) ([]Article, error) {
	rows, err := r.db.Query(
		"SELECT id, source_id, canonical_url, canonical_hash, title, published_at, fetched_at FROM articles WHERE source_id = ? ORDER BY fetched_at DESC",
		sourceID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanArticles(rows)
}

func scanArticles(rows *sql.Rows) ([]Article, error) {
	var articles []Article
	for rows.Next() {
		var a Article
		var pubAt *string
		var fa string
		if err := rows.Scan(&a.ID, &a.SourceID, &a.CanonicalURL, &a.CanonicalHash, &a.Title, &pubAt, &fa); err != nil {
			return nil, err
		}
		var err error
		a.FetchedAt, err = time.Parse(time.RFC3339, fa)
		if err != nil {
			return nil, fmt.Errorf("parse fetched_at %q: %w", fa, err)
		}
		if pubAt != nil {
			t, err := time.Parse(time.RFC3339, *pubAt)
			if err != nil {
				return nil, fmt.Errorf("parse published_at %q: %w", *pubAt, err)
			}
			a.PublishedAt = &t
		}
		articles = append(articles, a)
	}
	return articles, rows.Err()
}
