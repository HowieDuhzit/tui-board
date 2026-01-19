package sources

import (
	"context"
	"time"

	"tui-board/internal/config"
	"tui-board/internal/types"
)

type Source interface {
	ID() string
	Panel() string
	Interval() time.Duration
	Fetch(ctx context.Context) ([]types.Item, error)
}

type Factory func(config.SourceConfig) (Source, error)

func New(sc config.SourceConfig) (Source, error) {
	switch sc.Type {
	case "rss", "atom":
		return NewRSS(sc)
	case "json", "api":
		return NewJSON(sc)
	default:
		return nil, ErrUnknownSourceType{Type: sc.Type}
	}
}

type ErrUnknownSourceType struct {
	Type string
}

func (e ErrUnknownSourceType) Error() string {
	return "unknown source type: " + e.Type
}
