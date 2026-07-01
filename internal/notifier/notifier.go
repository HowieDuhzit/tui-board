package notifier

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"tui-board/internal/config"
	"tui-board/internal/types"
)

type Notifier struct {
	cfg       config.NotificationConfig
	seen      map[string]time.Time
	lastRule  map[int]time.Time
	mu        sync.Mutex
	serverCmd *exec.Cmd
}

func New(cfg config.NotificationConfig) *Notifier {
	return &Notifier{
		cfg:      cfg,
		seen:     make(map[string]time.Time),
		lastRule: make(map[int]time.Time),
	}
}

func (n *Notifier) StartServer() error {
	if !n.cfg.Enabled {
		return nil
	}
	if n.cfg.Ntfy.ServerCommand == "" {
		return nil
	}
	parts := strings.Fields(n.cfg.Ntfy.ServerCommand)
	if len(parts) == 0 {
		return nil
	}
	cmd := exec.Command(parts[0], parts[1:]...)
	if err := cmd.Start(); err != nil {
		return err
	}
	n.serverCmd = cmd
	return nil
}

func (n *Notifier) UpdateConfig(cfg config.NotificationConfig) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.cfg = cfg
}

func (n *Notifier) Close() {
	if n.serverCmd == nil || n.serverCmd.Process == nil {
		return
	}
	_ = n.serverCmd.Process.Kill()
}

func (n *Notifier) Handle(panel string, sourceID string, items []types.Item, notify *config.SourceNotify) {
	if !n.cfg.Enabled {
		return
	}
	if notify != nil && notify.Enabled != nil && !*notify.Enabled {
		return
	}
	for i, rule := range n.cfg.Rules {
		if rule.Panel != "" && rule.Panel != panel {
			continue
		}
		if !n.ruleReady(i, rule) {
			continue
		}
		count := 0
		for _, item := range items {
			if n.seenItem(item.ID) {
				continue
			}
			if !rule.AllowAll && !matchRule(rule, item) {
				continue
			}
			n.sendAll(item, notify)
			count++
			if rule.MaxItems > 0 && count >= rule.MaxItems {
				break
			}
		}
		if count > 0 {
			n.markRule(i)
		}
	}
}

func (n *Notifier) ruleReady(idx int, rule config.Rule) bool {
	if rule.Cooldown == "" {
		return true
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	last, ok := n.lastRule[idx]
	if !ok {
		return true
	}
	d, err := time.ParseDuration(rule.Cooldown)
	if err != nil {
		return true
	}
	return time.Since(last) >= d
}

func (n *Notifier) markRule(idx int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.lastRule[idx] = time.Now()
}

func (n *Notifier) seenItem(id string) bool {
	if id == "" {
		return false
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.seen[id]; ok {
		return true
	}
	n.seen[id] = time.Now()
	return false
}

func (n *Notifier) sendAll(item types.Item, notify *config.SourceNotify) {
	if n.cfg.Ntfy.BaseURL != "" && n.cfg.Ntfy.Topic != "" {
		n.sendNtfy(item, notify)
	}
	if n.cfg.System {
		_ = sendSystemNotification(item)
	}
	if n.cfg.Pushover.Enabled {
		n.sendPushover(item, notify)
	}
}

func (n *Notifier) sendNtfy(item types.Item, notify *config.SourceNotify) {
	url := strings.TrimRight(n.cfg.Ntfy.BaseURL, "/") + "/" + n.cfg.Ntfy.Topic
	payload := []byte(formatMessage(item))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return
	}
	req.Header.Set("Title", item.Title)
	tags := item.Tags
	if notify != nil && len(notify.NtfyTags) > 0 {
		tags = notify.NtfyTags
	}
	if len(tags) > 0 {
		req.Header.Set("Tags", strings.Join(tags, ","))
	}
	_, _ = http.DefaultClient.Do(req)
}

func formatMessage(item types.Item) string {
	parts := []string{item.Title}
	if item.Summary != "" {
		parts = append(parts, item.Summary)
	}
	if item.Link != "" {
		parts = append(parts, item.Link)
	}
	return strings.Join(parts, "\n")
}

func (n *Notifier) sendPushover(item types.Item, notify *config.SourceNotify) {
	if n.cfg.Pushover.UserKey == "" || n.cfg.Pushover.APIToken == "" {
		return
	}
	form := url.Values{}
	form.Set("token", n.cfg.Pushover.APIToken)
	form.Set("user", n.cfg.Pushover.UserKey)
	form.Set("title", item.Title)
	form.Set("message", formatMessage(item))
	form.Set("priority", mapPriority(item.Severity))
	if notify != nil {
		if notify.Device != "" {
			form.Set("device", notify.Device)
		}
		if notify.Sound != "" {
			form.Set("sound", notify.Sound)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.pushover.net/1/messages.json", strings.NewReader(form.Encode()))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_, _ = http.DefaultClient.Do(req)
}

func mapPriority(severity string) string {
	switch strings.ToLower(severity) {
	case "crit", "critical":
		return "1"
	case "warn", "warning":
		return "0"
	default:
		return "-1"
	}
}

func sendSystemNotification(item types.Item) error {
	title := item.Title
	body := formatMessage(item)
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("osascript", "-e", "display notification "+shellQuote(body)+" with title "+shellQuote(title)).Run()
	case "windows":
		return exec.Command("powershell", "-Command", "New-BurntToastNotification -Text "+shellQuote(title)+","+shellQuote(body)).Run()
	default:
		if _, err := exec.LookPath("notify-send"); err == nil {
			return exec.Command("notify-send", title, body).Run()
		}
	}
	return fmt.Errorf("system notifications unavailable")
}

func shellQuote(s string) string {
	return "\"" + strings.ReplaceAll(s, "\"", "\\\"") + "\""
}

func matchRule(rule config.Rule, item types.Item) bool {
	if rule.MinLevel != "" {
		minLevel, err := SeverityLevel(rule.MinLevel)
		if err == nil {
			itemLevel, err := SeverityLevel(item.Severity)
			if err == nil && itemLevel < minLevel {
				return false
			}
		}
	}
	if len(rule.Severity) > 0 && !contains(rule.Severity, item.Severity) {
		return false
	}
	if len(rule.Tags) > 0 && !anyTag(rule.Tags, item.Tags) {
		return false
	}
	if len(rule.Keywords) > 0 {
		hit := false
		text := strings.ToLower(item.Title + " " + item.Summary)
		for _, kw := range rule.Keywords {
			if strings.Contains(text, strings.ToLower(kw)) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	return true
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if strings.EqualFold(item, value) {
			return true
		}
	}
	return false
}

func anyTag(ruleTags, itemTags []string) bool {
	for _, t := range itemTags {
		if contains(ruleTags, t) {
			return true
		}
	}
	return false
}

func SeverityLevel(level string) (int, error) {
	switch strings.ToLower(level) {
	case "crit", "critical":
		return 3, nil
	case "warn", "warning":
		return 2, nil
	case "info":
		return 1, nil
	default:
		return 0, fmt.Errorf("unknown severity: %s", level)
	}
}
