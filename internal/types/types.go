package types

import "time"

const (
	PanelAlerts  = "alerts"
	PanelNews    = "news"
	PanelMarkets = "markets"
	PanelGit     = "git"
	PanelSystem  = "system"
)

type Item struct {
	ID        string
	Title     string
	Summary   string
	Link      string
	Timestamp time.Time
	Tags      []string
	Severity  string
	Source    string
}
