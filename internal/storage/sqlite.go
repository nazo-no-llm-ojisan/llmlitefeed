package storage

import (
	"database/sql"
	"embed"
	"fmt"
	"path/filepath"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

//go:embed migrations/*.sql
var migrations embed.FS

type sqliteStore struct {
	db *sql.DB
}

// Open は dataDir 配下の llmlitefeed.db を開き、マイグレーションを実行して Store を返す。
func Open(dataDir string) (Store, error) {
	db, err := sql.Open("sqlite3", filepath.Join(dataDir, "llmlitefeed.db"))
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	pragmas := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("pragma %s: %w", p, err)
		}
	}

	db.SetMaxOpenConns(1)

	s := &sqliteStore{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return s, nil
}

func (s *sqliteStore) migrate() error {
	data, err := migrations.ReadFile("migrations/001_initial.sql")
	if err != nil {
		return fmt.Errorf("read migration: %w", err)
	}
	if _, err := s.db.Exec(string(data)); err != nil {
		return fmt.Errorf("exec migration: %w", err)
	}
	return nil
}

func (s *sqliteStore) Sources() SourceRepository {
	return &sourceRepo{db: s.db}
}

func (s *sqliteStore) Articles() ArticleRepository {
	return &articleRepo{db: s.db}
}

func (s *sqliteStore) Close() error {
	return s.db.Close()
}
