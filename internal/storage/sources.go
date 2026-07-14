package storage

import (
	"database/sql"
	"fmt"
	"time"
)

type sourceRepo struct {
	db *sql.DB
}

func (r *sourceRepo) Insert(name, url string) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := r.db.Exec(
		"INSERT INTO sources (name, url, created_at, updated_at) VALUES (?, ?, ?, ?)",
		name, url, now, now,
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (r *sourceRepo) GetByURL(url string) (*Source, error) {
	row := r.db.QueryRow(
		"SELECT id, name, url, created_at, updated_at FROM sources WHERE url = ?",
		url,
	)
	var s Source
	var ca, ua string
	if err := row.Scan(&s.ID, &s.Name, &s.URL, &ca, &ua); err != nil {
		return nil, err
	}
	createdAt, err := time.Parse(time.RFC3339, ca)
	if err != nil {
		return nil, fmt.Errorf("parse created_at %q: %w", ca, err)
	}
	updatedAt, err := time.Parse(time.RFC3339, ua)
	if err != nil {
		return nil, fmt.Errorf("parse updated_at %q: %w", ua, err)
	}
	s.CreatedAt = createdAt
	s.UpdatedAt = updatedAt
	return &s, nil
}

func (r *sourceRepo) List() ([]Source, error) {
	rows, err := r.db.Query("SELECT id, name, url, created_at, updated_at FROM sources ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var srcs []Source
	for rows.Next() {
		var s Source
		var ca, ua string
		if err := rows.Scan(&s.ID, &s.Name, &s.URL, &ca, &ua); err != nil {
			return nil, err
		}
		createdAt, err := time.Parse(time.RFC3339, ca)
		if err != nil {
			return nil, fmt.Errorf("parse created_at %q: %w", ca, err)
		}
		updatedAt, err := time.Parse(time.RFC3339, ua)
		if err != nil {
			return nil, fmt.Errorf("parse updated_at %q: %w", ua, err)
		}
		s.CreatedAt = createdAt
		s.UpdatedAt = updatedAt
		srcs = append(srcs, s)
	}
	return srcs, rows.Err()
}

func (r *sourceRepo) Delete(id int64) error {
	_, err := r.db.Exec("DELETE FROM sources WHERE id = ?", id)
	return err
}
