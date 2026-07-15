package feed

import (
	"encoding/xml"
	"fmt"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"
)

// RawArticle は RSS/Atom フィードから正規化された記事を表す。
// HTTP や SQLite に依存しない純粋なデータ構造。
type RawArticle struct {
	Title       string
	URL         string
	Description string
	PublishedAt *time.Time
}

// Parse は生の XML 文字列を RSS/Atom フィードとして解析し、
// 正規化された RawArticle スライスを返す。
//
// HTTP リクエストもデータベースアクセスも行わない純粋関数。
// 不正な XML はエラーを返し、パニックしない。
// 空のフィード（item/entry なし）は空スライスと nil error を返す。
func Parse(rawXML string) ([]RawArticle, error) {
	if strings.TrimSpace(rawXML) == "" {
		return []RawArticle{}, nil
	}

	// まず XML として well-formed か検証（gofeed は一部の malformed を黙吞みする可能性があるため）
	dec := xml.NewDecoder(strings.NewReader(rawXML))
	for {
		tok, err := dec.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return nil, fmt.Errorf("feed: invalid XML: %w", err)
		}
		if tok == nil {
			break
		}
	}

	fp := gofeed.NewParser()
	feed, err := fp.ParseString(rawXML)
	if err != nil {
		return nil, fmt.Errorf("feed: parse failed: %w", err)
	}

	articles := make([]RawArticle, 0, len(feed.Items))
	for _, item := range feed.Items {
		art := RawArticle{
			Title:       item.Title,
			URL:         item.Link,
			Description: item.Description,
		}

		// PublishedParsed があれば優先、なければ UpdatedParsed
		if item.PublishedParsed != nil {
			utc := item.PublishedParsed.UTC()
			art.PublishedAt = &utc
		} else if item.UpdatedParsed != nil {
			utc := item.UpdatedParsed.UTC()
			art.PublishedAt = &utc
		}

		articles = append(articles, art)
	}

	return articles, nil
}