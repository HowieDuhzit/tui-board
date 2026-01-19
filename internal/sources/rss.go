package sources

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"

	"tui-board/internal/config"
	"tui-board/internal/types"
)

type RSSSource struct {
	id       string
	panel    string
	interval time.Duration
	url      string
	tags     []string
}

func NewRSS(sc config.SourceConfig) (*RSSSource, error) {
	if sc.URL == "" {
		return nil, fmt.Errorf("rss source %q missing url", sc.ID)
	}
	return &RSSSource{
		id:       sc.ID,
		panel:    sc.Panel,
		interval: config.ParseInterval(sc.Interval, 30*time.Second),
		url:      sc.URL,
		tags:     sc.Tags,
	}, nil
}

func (r *RSSSource) ID() string              { return r.id }
func (r *RSSSource) Panel() string           { return r.panel }
func (r *RSSSource) Interval() time.Duration { return r.interval }

func (r *RSSSource) Fetch(ctx context.Context) ([]types.Item, error) {
	parser := gofeed.NewParser()
	feed, err := parser.ParseURLWithContext(r.url, ctx)
	if err != nil {
		return nil, err
	}
	items := make([]types.Item, 0, len(feed.Items))
	for _, it := range feed.Items {
		if it == nil {
			continue
		}
		timestamp := time.Now()
		if it.PublishedParsed != nil {
			timestamp = *it.PublishedParsed
		} else if it.UpdatedParsed != nil {
			timestamp = *it.UpdatedParsed
		}
		id := it.GUID
		if id == "" {
			id = it.Link
		}
		if id == "" {
			id = strings.TrimSpace(it.Title)
		}
		items = append(items, types.Item{
			ID:        id,
			Title:     strings.TrimSpace(it.Title),
			Summary:   strings.TrimSpace(it.Description),
			Link:      it.Link,
			Timestamp: timestamp,
			Tags:      append([]string{}, append(r.tags, it.Categories...)...),
			Severity:  "info",
			Source:    r.id,
		})
	}
	return items, nil
}
