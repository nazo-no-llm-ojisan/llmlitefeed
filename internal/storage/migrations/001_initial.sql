CREATE TABLE IF NOT EXISTS sources (
    id         INTEGER PRIMARY KEY,
    name       TEXT NOT NULL,
    url        TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS articles (
    id             INTEGER PRIMARY KEY,
    source_id      INTEGER NOT NULL,
    canonical_url  TEXT NOT NULL,
    canonical_hash TEXT NOT NULL UNIQUE,
    title          TEXT NOT NULL,
    published_at   TEXT,
    fetched_at     TEXT NOT NULL,
    FOREIGN KEY (source_id) REFERENCES sources(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_articles_fetched_at ON articles(fetched_at DESC);
CREATE INDEX IF NOT EXISTS idx_articles_source_id ON articles(source_id);
