package storage

import "time"

// Store はデータストアのインターフェース。
type Store interface {
	Sources() SourceRepository
	Articles() ArticleRepository
	Close() error
}

// Source は RSS フィードソース。
type Source struct {
	ID        int64
	Name      string
	URL       string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Article は取得・保存された記事。
type Article struct {
	ID            int64
	SourceID      int64
	CanonicalURL  string
	CanonicalHash string
	Title         string
	PublishedAt   *time.Time
	FetchedAt     time.Time
}

// SourceRepository はソースの CRUD 操作。
type SourceRepository interface {
	Insert(name, url string) (int64, error)
	GetByURL(url string) (*Source, error)
	List() ([]Source, error)
	Delete(id int64) error
}

// ArticleRepository は記事の CRUD 操作。
type ArticleRepository interface {
	Insert(article Article) (int64, error)
	ExistsByHash(hash string) (bool, error)
	List(limit, offset int) ([]Article, error)
	ListBySource(sourceID int64) ([]Article, error)
}
