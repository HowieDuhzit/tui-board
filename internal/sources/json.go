package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"tui-board/internal/config"
	"tui-board/internal/types"
)

type JSONSource struct {
	id       string
	panel    string
	interval time.Duration
	url      string
	tags     []string
	headers  map[string]string
	mapping  map[string]string
}

func NewJSON(sc config.SourceConfig) (*JSONSource, error) {
	if sc.URL == "" {
		return nil, fmt.Errorf("json source %q missing url", sc.ID)
	}
	mapping := map[string]string{
		"id":       "id",
		"title":    "title",
		"summary":  "summary",
		"link":     "link",
		"time":     "time",
		"severity": "severity",
		"tags":     "tags",
	}
	for k, v := range sc.Mapping {
		mapping[k] = v
	}
	return &JSONSource{
		id:       sc.ID,
		panel:    sc.Panel,
		interval: config.ParseInterval(sc.Interval, 30*time.Second),
		url:      sc.URL,
		tags:     sc.Tags,
		headers:  sc.Headers,
		mapping:  mapping,
	}, nil
}

func (j *JSONSource) ID() string              { return j.id }
func (j *JSONSource) Panel() string           { return j.panel }
func (j *JSONSource) Interval() time.Duration { return j.interval }

func (j *JSONSource) Fetch(ctx context.Context) ([]types.Item, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, j.url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range j.headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("json source %q status %d", j.id, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var raw any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	itemsRaw := normalizeItems(raw)
	items := make([]types.Item, 0, len(itemsRaw))
	for _, entry := range itemsRaw {
		item := j.mapItem(entry)
		items = append(items, item)
	}
	return items, nil
}

func normalizeItems(raw any) []map[string]any {
	switch v := raw.(type) {
	case []any:
		items := make([]map[string]any, 0, len(v))
		for _, elem := range v {
			if m, ok := elem.(map[string]any); ok {
				items = append(items, m)
			}
		}
		return items
	case map[string]any:
		if list, ok := v["items"].([]any); ok {
			items := make([]map[string]any, 0, len(list))
			for _, elem := range list {
				if m, ok := elem.(map[string]any); ok {
					items = append(items, m)
				}
			}
			return items
		}
	}
	return nil
}

func (j *JSONSource) mapItem(entry map[string]any) types.Item {
	get := func(key string, fallback string) string {
		if key == "" {
			return fallback
		}
		if val, ok := entry[key]; ok {
			switch v := val.(type) {
			case string:
				return v
			case float64:
				return fmt.Sprintf("%.0f", v)
			case bool:
				if v {
					return "true"
				}
				return "false"
			}
		}
		return fallback
	}
	pick := func(name, fallback string) string {
		return get(j.mapping[name], fallback)
	}

	id := pick("id", "")
	if id == "" {
		id = pick("link", "")
	}
	if id == "" {
		id = pick("title", "")
	}

	timestamp := time.Now()
	if raw := pick("time", ""); raw != "" {
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			timestamp = t
		}
	}

	tags := append([]string{}, j.tags...)
	if tagKey := j.mapping["tags"]; tagKey != "" {
		if val, ok := entry[tagKey]; ok {
			switch v := val.(type) {
			case []any:
				for _, t := range v {
					if s, ok := t.(string); ok {
						tags = append(tags, strings.TrimSpace(s))
					}
				}
			case string:
				if strings.Contains(v, ",") {
					parts := strings.Split(v, ",")
					for _, p := range parts {
						tags = append(tags, strings.TrimSpace(p))
					}
				} else {
					tags = append(tags, v)
				}
			}
		}
	}

	severity := strings.ToLower(strings.TrimSpace(pick("severity", "info")))

	return types.Item{
		ID:        id,
		Title:     strings.TrimSpace(pick("title", "")),
		Summary:   strings.TrimSpace(pick("summary", "")),
		Link:      strings.TrimSpace(pick("link", "")),
		Timestamp: timestamp,
		Tags:      tags,
		Severity:  severity,
		Source:    j.id,
	}
}
