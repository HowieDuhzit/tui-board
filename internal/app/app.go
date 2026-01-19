package app

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	ollama "github.com/ollama/ollama/api"

	"tui-board/internal/config"
	"tui-board/internal/notifier"
	"tui-board/internal/sources"
	"tui-board/internal/system"
	"tui-board/internal/types"
)

type sourceResultMsg struct {
	ID    string
	Panel string
	Items []types.Item
	Err   error
}

type systemMsg struct {
	Snapshot system.Snapshot
	Err      error
}

type tickMsg time.Time
type readerLoadMsg struct {
	Title string
	Link  string
	Lines []string
	Err   error
	Kind  string
}
type openExternalMsg struct {
	Err error
}
type ttsMsg struct {
	Err error
}
type summaryMsg struct {
	Title string
	Lines []string
	Text  string
	Err   error
}

const (
	minWidth  = 80
	minHeight = 20
)

type panelState struct {
	title       string
	items       []types.Item
	selected    int
	lastUpdated time.Time
}

type Model struct {
	width  int
	height int
	now    time.Time

	cfg            config.Config
	sources        []sources.Source
	sourceNext     map[string]time.Time
	panelItems     map[string]map[string][]types.Item
	panels         map[string]*panelState
	panelOrder     []string
	activePanel    string
	panelInterval  map[string]time.Duration
	sourceConfigs  map[string]config.SourceConfig
	errors         map[string]error
	filterActive   bool
	filterInput    textinput.Model
	detailOpen     bool
	detailItem     *types.Item
	detailOffset   int
	readerOpen     bool
	readerTitle    string
	readerLines    []string
	readerOffset   int
	summaryOpen    bool
	summaryTitle   string
	summaryLines   []string
	summaryOffset  int
	summaryPending bool
	lastSummary    string
	summarySilent  bool
	menuOpen       bool
	menuItems      []string
	menuIndex      int
	menuError      string
	linkMsg        string
	linkMsgAt      time.Time
	configPath     string

	systemSnapshot system.Snapshot
	systemErr      error

	notifier *notifier.Notifier
}

func New(cfg config.Config, notifier *notifier.Notifier, srcs []sources.Source, configPath string) Model {
	panelOrder := []string{types.PanelAlerts, types.PanelNews, types.PanelMarkets, types.PanelGit, types.PanelSystem}
	panels := map[string]*panelState{
		types.PanelAlerts:  {title: panelTitle(cfg, types.PanelAlerts)},
		types.PanelNews:    {title: panelTitle(cfg, types.PanelNews)},
		types.PanelMarkets: {title: panelTitle(cfg, types.PanelMarkets)},
		types.PanelGit:     {title: panelTitle(cfg, types.PanelGit)},
		types.PanelSystem:  {title: panelTitle(cfg, types.PanelSystem)},
	}

	ti := textinput.New()
	ti.Prompt = "filter: "
	ti.CharLimit = 64
	ti.Placeholder = "type to filter"

	sourceNext := make(map[string]time.Time)
	panelItems := make(map[string]map[string][]types.Item)
	panelInterval := make(map[string]time.Duration)
	sourceConfigs := make(map[string]config.SourceConfig)
	for _, src := range srcs {
		sourceNext[src.ID()] = time.Now()
		if _, ok := panelItems[src.Panel()]; !ok {
			panelItems[src.Panel()] = make(map[string][]types.Item)
		}
		if existing, ok := panelInterval[src.Panel()]; !ok || src.Interval() < existing {
			panelInterval[src.Panel()] = src.Interval()
		}
	}
	for _, sc := range cfg.Sources {
		sourceConfigs[sc.ID] = sc
	}

	return Model{
		now:           time.Now(),
		cfg:           cfg,
		sources:       srcs,
		sourceNext:    sourceNext,
		panelItems:    panelItems,
		panels:        panels,
		panelOrder:    panelOrder,
		activePanel:   types.PanelNews,
		panelInterval: panelInterval,
		sourceConfigs: sourceConfigs,
		errors:        make(map[string]error),
		filterInput:   ti,
		detailOpen:    false,
		detailItem:    nil,
		detailOffset:  0,
		menuOpen:      false,
		menuItems:     nil,
		menuIndex:     0,
		menuError:     "",
		notifier:      notifier,
		configPath:    configPath,
	}
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{tick(), systemTick()}
	for _, src := range m.sources {
		cmds = append(cmds, fetchSourceCmd(src))
	}
	return tea.Batch(cmds...)
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func systemTick() tea.Cmd {
	return tea.Tick(5*time.Second, func(t time.Time) tea.Msg { return systemMsg{} })
}

func fetchSourceCmd(src sources.Source) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		items, err := src.Fetch(ctx)
		return sourceResultMsg{ID: src.ID(), Panel: src.Panel(), Items: items, Err: err}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tickMsg:
		m.now = time.Time(msg)
		if m.linkMsg != "" && time.Since(m.linkMsgAt) > 3*time.Second {
			m.linkMsg = ""
		}
		return m, m.pollSources()
	case readerLoadMsg:
		if msg.Err != nil {
			if msg.Kind == "proxy" {
				m.linkMsg = "proxy failed, trying direct..."
				m.linkMsgAt = time.Now()
				return m, fetchDirectCmd(msg.Title, msg.Link)
			}
			m.linkMsg = "open failed"
			m.linkMsgAt = time.Now()
			return m, nil
		}
		m.readerOpen = true
		m.readerTitle = msg.Title
		m.readerLines = msg.Lines
		m.readerOffset = 0
		m.detailOpen = false
	case openExternalMsg:
		if msg.Err != nil {
			m.linkMsg = "browser open failed"
		} else {
			m.linkMsg = "opened in browser"
		}
		m.linkMsgAt = time.Now()
	case ttsMsg:
		if msg.Err != nil {
			m.linkMsg = "tts failed: " + msg.Err.Error()
		} else {
			m.linkMsg = "tts complete"
		}
		m.linkMsgAt = time.Now()
	case summaryMsg:
		m.summaryPending = false
		silent := m.summarySilent
		m.summarySilent = false
		if msg.Err != nil {
			m.linkMsg = "summary failed: " + msg.Err.Error()
			m.linkMsgAt = time.Now()
			return m, nil
		}
		if silent {
			m.summaryOpen = false
			m.summaryTitle = ""
			m.summaryLines = nil
			m.summaryOffset = 0
		}
		m.lastSummary = msg.Text
		m.detailOpen = false
		if silent {
			return m, speakCmd(m.cfg.TTS, m.lastSummary)
		}
		m.summaryOpen = true
		m.summaryTitle = msg.Title
		m.summaryLines = msg.Lines
		m.summaryOffset = 0
		return m, speakCmd(m.cfg.TTS, m.lastSummary)
	case systemMsg:
		m = m.updateSystem()
		return m, systemTick()
	case sourceResultMsg:
		m.handleSourceResult(msg)
	case tea.KeyMsg:
		return m.handleKey(msg)
	case tea.MouseMsg:
		return m, nil
	}

	if m.filterActive {
		var cmd tea.Cmd
		m.filterInput, cmd = m.filterInput.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" || msg.String() == "q" {
		return m, tea.Quit
	}

	if m.menuOpen {
		switch msg.String() {
		case "esc":
			m.menuOpen = false
			m.menuError = ""
			return m, nil
		case "j", "down":
			m.menuIndex = clamp(m.menuIndex+1, 0, max(0, len(m.menuItems)-1))
			return m, nil
		case "k", "up":
			m.menuIndex = clamp(m.menuIndex-1, 0, max(0, len(m.menuItems)-1))
			return m, nil
		case "enter":
			if len(m.menuItems) == 0 {
				return m, nil
			}
			path := m.menuItems[m.menuIndex]
			cfg, err := config.Load(path)
			if err != nil {
				m.menuError = err.Error()
				return m, nil
			}
			srcs, err := buildSources(cfg.Sources)
			if err != nil {
				m.menuError = err.Error()
				return m, nil
			}
			m = m.applyConfig(cfg, srcs, path)
			m.menuOpen = false
			m.menuError = ""
			return m, m.forceRefresh()
		}
		return m, nil
	}

	if m.readerOpen {
		switch msg.String() {
		case "esc", "enter":
			m.readerOpen = false
			m.readerTitle = ""
			m.readerLines = nil
			m.readerOffset = 0
			return m, nil
		case "j", "down":
			m.readerOffset++
			return m, nil
		case "k", "up":
			m.readerOffset = max(0, m.readerOffset-1)
			return m, nil
		}
		return m, nil
	}

	if m.summaryOpen {
		switch msg.String() {
		case "esc", "enter":
			m.summaryOpen = false
			m.summaryTitle = ""
			m.summaryLines = nil
			m.summaryOffset = 0
			return m, nil
		case "t":
			return m, speakCmd(m.cfg.TTS, m.lastSummary)
		case "j", "down":
			m.summaryOffset++
			return m, nil
		case "k", "up":
			m.summaryOffset = max(0, m.summaryOffset-1)
			return m, nil
		}
		return m, nil
	}
	if m.summaryPending {
		switch msg.String() {
		case "esc":
			m.summaryPending = false
			return m, nil
		}
		return m, nil
	}

	if m.detailOpen {
		switch msg.String() {
		case "esc", "enter":
			m.detailOpen = false
			m.detailItem = nil
			m.detailOffset = 0
			return m, nil
		case "o":
			if m.detailItem != nil && m.detailItem.Link != "" {
				return m, openExternalCmd(m.detailItem.Link)
			}
		case "j", "down":
			m.detailOffset++
			return m, nil
		case "k", "up":
			m.detailOffset = max(0, m.detailOffset-1)
			return m, nil
		}
		return m, nil
	}

	if m.filterActive {
		switch msg.String() {
		case "esc", "enter":
			m.filterActive = false
			m.filterInput.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.filterInput, cmd = m.filterInput.Update(msg)
		return m, cmd
	}

	switch msg.String() {
	case "tab":
		m.activePanel = nextPanel(m.activePanel, m.panelOrder)
	case "f":
		m.filterActive = true
		m.filterInput.Focus()
	case "j", "down":
		m.moveSelection(1)
	case "k", "up":
		m.moveSelection(-1)
	case "g":
		m.jumpSelection(0)
	case "G":
		m.jumpSelection(1)
	case "r":
		return m, m.forceRefresh()
	case "enter":
		if item := m.selectedItem(m.activePanel); item != nil && item.Link != "" {
			return m, fetchProxyCmd(item.Title, item.Link)
		}
	case "o":
		if item := m.selectedItem(m.activePanel); item != nil && item.Link != "" {
			return m, openExternalCmd(item.Link)
		}
	case "s":
		if item := m.selectedItem(m.activePanel); item != nil {
			m.summaryPending = true
			m.summarySilent = false
			m.linkMsg = "summarizing..."
			m.linkMsgAt = time.Now()
			return m, summarizeCmd(m.cfg.Ollama, item)
		}
	case "t":
		if m.lastSummary != "" {
			return m, speakCmd(m.cfg.TTS, m.lastSummary)
		}
		if item := m.selectedItem(m.activePanel); item != nil {
			m.summaryPending = true
			m.summarySilent = true
			m.linkMsg = "summarizing..."
			m.linkMsgAt = time.Now()
			return m, summarizeCmd(m.cfg.Ollama, item)
		}
	case "c":
		m.menuOpen = true
		m.menuItems = listConfigFiles()
		m.menuIndex = 0
	case "i":
		if item := m.selectedItem(m.activePanel); item != nil {
			m.detailOpen = true
			m.detailItem = item
			m.detailOffset = 0
		}
	}
	return m, nil
}

func (m Model) pollSources() tea.Cmd {
	var cmds []tea.Cmd
	now := time.Now()
	for _, src := range m.sources {
		if next, ok := m.sourceNext[src.ID()]; ok && now.Before(next) {
			continue
		}
		m.sourceNext[src.ID()] = now.Add(src.Interval())
		cmds = append(cmds, fetchSourceCmd(src))
	}
	cmds = append(cmds, tick())
	return tea.Batch(cmds...)
}

func (m Model) forceRefresh() tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(m.sources))
	for _, src := range m.sources {
		m.sourceNext[src.ID()] = time.Now().Add(src.Interval())
		cmds = append(cmds, fetchSourceCmd(src))
	}
	return tea.Batch(cmds...)
}

func (m *Model) handleSourceResult(msg sourceResultMsg) {
	if msg.Err != nil {
		m.errors[msg.ID] = msg.Err
		return
	}
	delete(m.errors, msg.ID)
	if _, ok := m.panelItems[msg.Panel]; !ok {
		m.panelItems[msg.Panel] = make(map[string][]types.Item)
	}
	m.panelItems[msg.Panel][msg.ID] = msg.Items
	merged := mergeItems(m.panelItems[msg.Panel])
	panel := m.panels[msg.Panel]
	panel.items = merged
	panel.lastUpdated = time.Now()
	if panel.selected >= len(panel.items) {
		panel.selected = max(0, len(panel.items)-1)
	}

	if m.notifier != nil {
		var notify *config.SourceNotify
		if sc, ok := m.sourceConfigs[msg.ID]; ok {
			notify = &sc.Notify
		}
		m.notifier.Handle(msg.Panel, msg.ID, msg.Items, notify)
	}
}

func (m Model) updateSystem() Model {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	snap, err := system.Sample(ctx, "/", 500*time.Millisecond)
	m.systemSnapshot = snap
	m.systemErr = err
	return m
}

func (m Model) View() string {
	if m.width < minWidth || m.height < minHeight {
		return "Resize the window to at least 80x20."
	}

	bar := m.statusBar()
	footer := m.footerBar()

	if m.menuOpen {
		contentHeight := max(0, m.height-2)
		menu := m.menuView(m.width, contentHeight)
		return lipgloss.JoinVertical(lipgloss.Top, bar, menu, footer)
	}

	if m.summaryOpen {
		contentHeight := max(0, m.height-2)
		view := m.readerView(m.summaryTitle, m.summaryLines, m.width, contentHeight)
		return lipgloss.JoinVertical(lipgloss.Top, bar, view, footer)
	}
	if m.summaryPending && !m.summarySilent {
		contentHeight := max(0, m.height-2)
		view := m.readerView("SUMMARY", []string{"(summarizing...)"}, m.width, contentHeight)
		return lipgloss.JoinVertical(lipgloss.Top, bar, view, footer)
	}

	if m.readerOpen {
		contentHeight := max(0, m.height-2)
		reader := m.readerView(m.readerTitle, m.readerLines, m.width, contentHeight)
		return lipgloss.JoinVertical(lipgloss.Top, bar, reader, footer)
	}

	if m.detailOpen && m.detailItem != nil {
		contentHeight := max(0, m.height-2)
		detail := m.detailView(*m.detailItem, m.width, contentHeight)
		return lipgloss.JoinVertical(lipgloss.Top, bar, detail, footer)
	}

	availableHeight := m.height - 2
	if availableHeight < 4 {
		return bar + "\n" + footer
	}

	topHeight := int(float64(availableHeight) * 0.6)
	if topHeight < 6 {
		topHeight = availableHeight / 2
	}
	bottomHeight := availableHeight - topHeight

	leftW := int(float64(m.width) * 0.27)
	rightW := int(float64(m.width) * 0.27)
	centerW := m.width - leftW - rightW
	if centerW < 30 {
		centerW = 30
		if leftW > rightW {
			leftW = max(20, m.width-centerW-rightW)
		} else {
			rightW = max(20, m.width-centerW-leftW)
		}
	}

	alerts := m.panelBox(types.PanelAlerts, leftW, topHeight)
	news := m.panelBox(types.PanelNews, centerW, topHeight)
	markets := m.panelBox(types.PanelMarkets, rightW, topHeight)

	gitci := m.panelBox(types.PanelGit, leftW+centerW, bottomHeight)
	systems := m.panelBox(types.PanelSystem, rightW, bottomHeight)

	topRow := lipgloss.JoinHorizontal(lipgloss.Top, alerts, news, markets)
	bottomRow := lipgloss.JoinHorizontal(lipgloss.Top, gitci, systems)

	return lipgloss.JoinVertical(lipgloss.Top, bar, topRow, bottomRow, footer)
}

func (m Model) statusBar() string {
	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.cfg.Theme.BarFG)).
		Background(lipgloss.Color(m.cfg.Theme.BarBG)).
		Padding(0, 1)
	content := fmt.Sprintf("NOTIFBOARD > host: %s > profile: %s > refresh: %s > %s", m.cfg.Host, m.cfg.Profile, m.cfg.Refresh, m.now.Format("15:04:05"))
	return style.Width(m.width).Render(content)
}

func (m Model) footerBar() string {
	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.cfg.Theme.Dim)).
		Background(lipgloss.Color(m.cfg.Theme.BarBG)).
		Padding(0, 1)
	content := ":cmd  [r]refresh  [f]filter  [c]config  [tab]cycle  [j/k]move  [enter]open  [o]browser  [s]summary  [i]info  [q]quit"
	if m.filterActive {
		content = m.filterInput.View()
	}
	if m.detailOpen {
		content = ":view  [j/k]scroll  [o]browser  [enter/esc]back"
	}
	if m.readerOpen {
		content = ":read  [j/k]scroll  [enter/esc]back"
	}
	if m.summaryOpen {
		content = ":summary  [j/k]scroll  [enter/esc]back"
	}
	if m.summaryPending {
		content = ":summary  [esc]cancel"
	}
	if m.lastSummary != "" && !m.summaryOpen && !m.summaryPending {
		content = content + "  [t]speak"
	}
	if m.menuOpen {
		content = ":config  [j/k]move  [enter]load  [esc]close"
	}
	if m.linkMsg != "" {
		content = content + "  " + m.linkMsg
	}
	return style.Width(m.width).Render(content)
}

func (m Model) panelBox(panelID string, width, height int) string {
	panel := m.panels[panelID]
	border := asciiBorder()
	contentWidth := max(0, width-4)
	contentHeight := max(0, height-2)
	borderColor := m.cfg.Theme.Border
	if panelID == m.activePanel {
		borderColor = m.cfg.Theme.BorderActive
	}
	box := lipgloss.NewStyle().
		Border(border).
		BorderForeground(lipgloss.Color(borderColor)).
		Padding(0, 1).
		Width(contentWidth).
		Height(contentHeight)

	status := m.panelStatus(panelID)
	icon := panelIcon(m.cfg, panelID)
	headerText := " " + icon + " " + panel.title
	header := lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.cfg.Theme.Header)).
		Background(lipgloss.Color(m.cfg.Theme.HeaderBG)).
		Bold(true).
		Render(headerText)

	content := m.panelContent(panelID, contentWidth, contentHeight)
	if panelID == m.activePanel {
		header = lipgloss.NewStyle().
			Foreground(lipgloss.Color(m.cfg.Theme.HeaderActive)).
			Background(lipgloss.Color(m.cfg.Theme.HeaderActiveBG)).
			Bold(true).
			Render(headerText + " [*]")
	}
	if status != "" {
		header = header + " " + renderStatusBadge(m.cfg.Theme, status)
	}

	body := lipgloss.NewStyle().Foreground(lipgloss.Color(m.cfg.Theme.Text)).Render(content)
	return box.Render(lipgloss.JoinVertical(lipgloss.Top, header, body))
}

func (m Model) panelContent(panelID string, width, height int) string {
	panel := m.panels[panelID]
	lines := []string{}

	items := m.filteredItems(panelID)
	systemLines := []string{}
	systemPad := 0
	if panelID == types.PanelSystem {
		systemLines = renderSystem(m.systemSnapshot, m.systemErr, m.cfg.Theme)
		if len(systemLines) > 0 {
			systemPad = len(systemLines) + 1
		}
	}

	metaLine := m.panelMeta(panelID, len(items))
	metaPad := 0
	if metaLine != "" {
		metaPad = 1
	}
	maxRows := max(1, height-2-systemPad-metaPad)
	start := clamp(panel.selected-maxRows/2, 0, max(0, len(items)-maxRows))
	end := min(len(items), start+maxRows)
	if metaLine != "" {
		lines = append(lines, metaLine)
	}
	for i := start; i < end; i++ {
		item := items[i]
		selected := (i == panel.selected)
		lines = append(lines, renderItem(panelID, item, width-2, selected, m.cfg.Theme))
	}

	if len(lines) == 0 {
		if errLines := m.panelErrorLines(panelID); len(errLines) > 0 {
			lines = append(lines, errLines...)
		}
	}
	if len(items) == 0 && panelID != types.PanelSystem {
		lines = append(lines, "(no data)")
	}
	if len(systemLines) > 0 {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, systemLines...)
	}

	return strings.Join(lines, "\n")
}

func (m Model) filteredItems(panelID string) []types.Item {
	panel := m.panels[panelID]
	if panel == nil {
		return nil
	}
	if !m.filterActive && m.filterInput.Value() == "" {
		return panel.items
	}
	query := strings.ToLower(strings.TrimSpace(m.filterInput.Value()))
	if query == "" {
		return panel.items
	}
	filtered := make([]types.Item, 0, len(panel.items))
	for _, item := range panel.items {
		text := strings.ToLower(item.Title + " " + item.Summary + " " + strings.Join(item.Tags, " "))
		if strings.Contains(text, query) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func (m *Model) moveSelection(delta int) {
	panel := m.panels[m.activePanel]
	if panel == nil || len(panel.items) == 0 {
		return
	}
	if m.filterInput.Value() != "" {
		filtered := m.filteredItems(m.activePanel)
		if len(filtered) == 0 {
			return
		}
		currentID := ""
		if panel.selected >= 0 && panel.selected < len(panel.items) {
			currentID = panel.items[panel.selected].ID
		}
		currentFiltered := 0
		if currentID != "" {
			for i, item := range filtered {
				if item.ID == currentID {
					currentFiltered = i
					break
				}
			}
		}
		next := clamp(currentFiltered+delta, 0, len(filtered)-1)
		targetID := filtered[next].ID
		for i, item := range panel.items {
			if item.ID == targetID {
				panel.selected = i
				return
			}
		}
		return
	}
	panel.selected = clamp(panel.selected+delta, 0, len(panel.items)-1)
}

func (m *Model) jumpSelection(pos int) {
	panel := m.panels[m.activePanel]
	if panel == nil || len(panel.items) == 0 {
		return
	}
	if m.filterInput.Value() != "" {
		filtered := m.filteredItems(m.activePanel)
		if len(filtered) == 0 {
			return
		}
		target := filtered[0]
		if pos != 0 {
			target = filtered[len(filtered)-1]
		}
		for i, item := range panel.items {
			if item.ID == target.ID {
				panel.selected = i
				return
			}
		}
		return
	}
	if pos == 0 {
		panel.selected = 0
		return
	}
	panel.selected = len(panel.items) - 1
}

func mergeItems(src map[string][]types.Item) []types.Item {
	var merged []types.Item
	for _, items := range src {
		merged = append(merged, items...)
	}
	sort.SliceStable(merged, func(i, j int) bool {
		return merged[i].Timestamp.After(merged[j].Timestamp)
	})
	return merged
}

func renderItem(panelID string, item types.Item, width int, selected bool, theme config.Theme) string {
	prefix := "  "
	if selected {
		prefix = "> "
	}
	stamp := item.Timestamp.Format("15:04")
	if item.Timestamp.IsZero() {
		stamp = "--:--"
	}
	severityMark := severityMarker(item.Severity)
	label := fmt.Sprintf("%s %s %s", stamp, severityMark, item.Title)
	tags := ""
	if len(item.Tags) > 0 {
		tags = "[" + strings.Join(item.Tags, ",") + "]"
	}

	textWidth := max(0, width-2)
	base := truncate(label, textWidth)
	tagText := ""
	if tags != "" {
		tagPadding := 1
		tagWidth := max(0, textWidth-len(base)-tagPadding)
		if tagWidth > 0 {
			tagText = truncate(tags, tagWidth)
		}
	}

	if selected {
		style := lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.SelectedFG)).
			Background(lipgloss.Color(theme.SelectedBG)).
			Bold(true)
		if tagText != "" {
			base = base + " " + tagText
		}
		return prefix + style.Render(base)
	}

	textStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Text))
	markerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(severityColor(theme, item.Severity))).Bold(true)
	tagStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Dim))

	baseStyled := textStyle.Render(base)
	baseStyled = strings.Replace(baseStyled, severityMark, markerStyle.Render(severityMark), 1)
	if tagText != "" {
		baseStyled += " " + tagStyle.Render(tagText)
	}

	return prefix + baseStyled
}

func renderSystem(snapshot system.Snapshot, err error, theme config.Theme) []string {
	if err != nil {
		return []string{"system: error"}
	}
	lines := []string{
		fmt.Sprintf("CPU  %s  %2.0f%%", barColored(snapshot.CPUPercent, theme), snapshot.CPUPercent),
		fmt.Sprintf("MEM  %s  %2.0f%% (%.1f/%.1fG)", barColored(snapshot.MemUsedPct, theme), snapshot.MemUsedPct, snapshot.MemUsedGB, snapshot.MemTotalGB),
		fmt.Sprintf("DISK %s  %2.0f%% (%s)", barColored(snapshot.DiskUsedPct, theme), snapshot.DiskUsedPct, snapshot.DiskPath),
		fmt.Sprintf("NET  in %.1fMbps  out %.1fMbps", snapshot.NetInMbps, snapshot.NetOutMbps),
	}
	return lines
}

func bar(pct float64) string {
	blocks := 10
	filled := int((pct / 100) * float64(blocks))
	if filled > blocks {
		filled = blocks
	}
	return strings.Repeat("#", filled) + strings.Repeat(".", blocks-filled)
}

func barColored(pct float64, theme config.Theme) string {
	blocks := 10
	filled := int((pct / 100) * float64(blocks))
	if filled > blocks {
		filled = blocks
	}
	filledStr := strings.Repeat("#", filled)
	emptyStr := strings.Repeat(".", blocks-filled)
	levelColor := theme.Ok
	if pct >= 80 {
		levelColor = theme.Crit
	} else if pct >= 60 {
		levelColor = theme.Warn
	}
	filledStyled := lipgloss.NewStyle().Foreground(lipgloss.Color(levelColor)).Render(filledStr)
	emptyStyled := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Dim)).Render(emptyStr)
	return filledStyled + emptyStyled
}

func severityMarker(level string) string {
	switch strings.ToLower(level) {
	case "crit", "critical":
		return "!!"
	case "warn", "warning":
		return "!"
	case "ok":
		return "+"
	case "info":
		return "i"
	default:
		return "-"
	}
}

func severityColor(theme config.Theme, level string) string {
	switch strings.ToLower(level) {
	case "crit", "critical":
		return theme.Crit
	case "warn", "warning":
		return theme.Warn
	case "ok":
		return theme.Ok
	case "info":
		return theme.Info
	default:
		return theme.Dim
	}
}

func truncate(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if len(text) <= width {
		return text
	}
	if width <= 3 {
		return text[:width]
	}
	return text[:width-3] + "..."
}

func panelTitle(cfg config.Config, panel string) string {
	if cfg.Panels != nil {
		if p, ok := cfg.Panels[panel]; ok && p.Title != "" {
			return p.Title
		}
	}
	switch panel {
	case types.PanelAlerts:
		return "ALERTS"
	case types.PanelNews:
		return "NEWS / FEED"
	case types.PanelMarkets:
		return "MARKETS"
	case types.PanelGit:
		return "GIT / CI"
	case types.PanelSystem:
		return "SERVERS / SYSTEM"
	default:
		return strings.ToUpper(panel)
	}
}

func panelIcon(cfg config.Config, panel string) string {
	if cfg.Theme.Icons != nil {
		if icon, ok := cfg.Theme.Icons[panel]; ok && icon != "" {
			return icon
		}
	}
	return "-"
}

func nextPanel(current string, order []string) string {
	for i, panel := range order {
		if panel == current {
			return order[(i+1)%len(order)]
		}
	}
	if len(order) == 0 {
		return current
	}
	return order[0]
}

func asciiBorder() lipgloss.Border {
	return lipgloss.Border{
		Top:         "-",
		Bottom:      "-",
		Left:        "|",
		Right:       "|",
		TopLeft:     "+",
		TopRight:    "+",
		BottomLeft:  "+",
		BottomRight: "+",
	}
}

func (m Model) panelStatus(panelID string) string {
	var parts []string
	if m.panelHasError(panelID) {
		parts = append(parts, "err")
	}
	panel := m.panels[panelID]
	interval := m.panelInterval[panelID]
	if interval == 0 {
		interval = 30 * time.Second
	}
	if panel != nil {
		if panel.lastUpdated.IsZero() {
			parts = append(parts, "wait")
		} else if m.now.Sub(panel.lastUpdated) > interval*2 {
			parts = append(parts, "stale")
		}
	}
	return strings.Join(parts, ",")
}

func (m Model) panelMeta(panelID string, count int) string {
	panel := m.panels[panelID]
	updated := "--"
	if panel != nil && !panel.lastUpdated.IsZero() {
		age := time.Since(panel.lastUpdated)
		if age < time.Minute {
			updated = fmt.Sprintf("%ds", int(age.Seconds()))
		} else {
			updated = fmt.Sprintf("%dm", int(age.Minutes()))
		}
	}
	meta := fmt.Sprintf("items:%d  upd:%s", count, updated)
	if m.panelHasError(panelID) {
		meta += "  err"
	}
	color := m.cfg.Theme.Dim
	if panelID == m.activePanel {
		color = m.cfg.Theme.Accent
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(meta)
}

func renderStatusBadge(theme config.Theme, status string) string {
	if status == "" {
		return ""
	}
	parts := strings.Split(status, ",")
	var rendered []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		label := strings.ToUpper(part)
		color := theme.Dim
		switch part {
		case "err":
			color = theme.Crit
		case "stale":
			color = theme.Warn
		case "wait":
			color = theme.Dim
		}
		rendered = append(rendered, lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Bold(true).Render("["+label+"]"))
	}
	return strings.Join(rendered, " ")
}

func renderSeverityBadge(theme config.Theme, level string) string {
	if level == "" {
		return ""
	}
	color := severityColor(theme, level)
	label := strings.ToUpper(level)
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Bold(true).Render("[SEV:" + label + "]")
}

func (m Model) selectedItem(panelID string) *types.Item {
	panel := m.panels[panelID]
	if panel == nil || len(panel.items) == 0 {
		return nil
	}
	if panel.selected < 0 || panel.selected >= len(panel.items) {
		return nil
	}
	if m.filterInput.Value() != "" || m.filterActive {
		filtered := m.filteredItems(panelID)
		if len(filtered) == 0 {
			return nil
		}
		selectedID := panel.items[panel.selected].ID
		for _, item := range filtered {
			if item.ID == selectedID {
				copy := item
				return &copy
			}
		}
		copy := filtered[0]
		return &copy
	}
	if panel.selected < 0 || panel.selected >= len(panel.items) {
		return nil
	}
	copy := panel.items[panel.selected]
	return &copy
}

func (m Model) detailView(item types.Item, width, height int) string {
	border := asciiBorder()
	box := lipgloss.NewStyle().
		Border(border).
		BorderForeground(lipgloss.Color(m.cfg.Theme.BorderActive)).
		Padding(1, 2).
		Width(max(0, width-4)).
		Height(max(0, height-2))

	badge := renderSeverityBadge(m.cfg.Theme, item.Severity)
	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.cfg.Theme.HeaderActive)).
		Bold(true).
		Render(item.Title)

	meta := fmt.Sprintf("source:%s  time:%s", item.Source, item.Timestamp.Format(time.RFC822))
	if item.Timestamp.IsZero() {
		meta = fmt.Sprintf("source:%s  time:--", item.Source)
	}
	metaLine := lipgloss.NewStyle().Foreground(lipgloss.Color(m.cfg.Theme.Dim)).Render(meta)

	divider := lipgloss.NewStyle().Foreground(lipgloss.Color(m.cfg.Theme.Border)).Render(strings.Repeat("-", max(0, width-8)))

	lines := []string{title}
	if badge != "" {
		lines = append(lines, badge)
	}
	lines = append(lines, metaLine, divider)

	summary := cleanSummary(item.Summary)
	if summary != "" {
		lines = append(lines, wrapParagraphs(summary, width-8)...)
		lines = append(lines, "")
	}
	if item.Link != "" {
		label := lipgloss.NewStyle().Foreground(lipgloss.Color(m.cfg.Theme.Dim)).Render("link:")
		value := lipgloss.NewStyle().Foreground(lipgloss.Color(m.cfg.Theme.Accent)).Render(item.Link)
		lines = append(lines, label+" "+value)
	}
	if len(item.Tags) > 0 {
		lines = append(lines, "")
		label := lipgloss.NewStyle().Foreground(lipgloss.Color(m.cfg.Theme.Dim)).Render("tags:")
		lines = append(lines, label+" "+strings.Join(item.Tags, ", "))
	}

	contentHeight := max(0, height-4)
	start := clamp(m.detailOffset, 0, max(0, len(lines)-contentHeight))
	end := min(len(lines), start+contentHeight)
	visible := strings.Join(lines[start:end], "\n")
	return box.Render(visible)
}

func wrapLines(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{""}
	}
	lines := []string{}
	line := words[0]
	for _, w := range words[1:] {
		if len(line)+1+len(w) > width {
			lines = append(lines, line)
			line = w
			continue
		}
		line += " " + w
	}
	lines = append(lines, line)
	return lines
}

func proxyURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw
	}
	return "https://r.jina.ai/http://" + strings.TrimPrefix(raw, "https://")
}

func fetchProxyCmd(title, link string) tea.Cmd {
	return func() tea.Msg {
		return fetchReader(title, link, "proxy")
	}
}

func fetchDirectCmd(title, link string) tea.Cmd {
	return func() tea.Msg {
		return fetchReader(title, link, "direct")
	}
}

func openExternalCmd(url string) tea.Cmd {
	return func() tea.Msg {
		if url == "" {
			return openExternalMsg{Err: fmt.Errorf("empty url")}
		}
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			cmd = exec.Command("open", url)
		case "windows":
			cmd = exec.Command("cmd", "/c", "start", url)
		default:
			cmd = exec.Command("xdg-open", url)
		}
		if err := cmd.Start(); err != nil {
			return openExternalMsg{Err: err}
		}
		return openExternalMsg{}
	}
}

func listConfigFiles() []string {
	var files []string
	patterns := []string{"config*.yaml", "config*.yml"}
	seen := map[string]bool{}
	for _, p := range patterns {
		matches, _ := filepath.Glob(p)
		for _, m := range matches {
			if !seen[m] {
				seen[m] = true
				files = append(files, m)
			}
		}
	}
	sort.Strings(files)
	return files
}

func buildSources(cfgs []config.SourceConfig) ([]sources.Source, error) {
	var srcs []sources.Source
	for _, sc := range cfgs {
		src, err := sources.New(sc)
		if err != nil {
			return nil, err
		}
		srcs = append(srcs, src)
	}
	return srcs, nil
}

func (m Model) applyConfig(cfg config.Config, srcs []sources.Source, path string) Model {
	panelOrder := []string{types.PanelAlerts, types.PanelNews, types.PanelMarkets, types.PanelGit, types.PanelSystem}
	panels := map[string]*panelState{
		types.PanelAlerts:  {title: panelTitle(cfg, types.PanelAlerts)},
		types.PanelNews:    {title: panelTitle(cfg, types.PanelNews)},
		types.PanelMarkets: {title: panelTitle(cfg, types.PanelMarkets)},
		types.PanelGit:     {title: panelTitle(cfg, types.PanelGit)},
		types.PanelSystem:  {title: panelTitle(cfg, types.PanelSystem)},
	}
	sourceNext := make(map[string]time.Time)
	panelItems := make(map[string]map[string][]types.Item)
	panelInterval := make(map[string]time.Duration)
	sourceConfigs := make(map[string]config.SourceConfig)
	for _, src := range srcs {
		sourceNext[src.ID()] = time.Now()
		if _, ok := panelItems[src.Panel()]; !ok {
			panelItems[src.Panel()] = make(map[string][]types.Item)
		}
		if existing, ok := panelInterval[src.Panel()]; !ok || src.Interval() < existing {
			panelInterval[src.Panel()] = src.Interval()
		}
	}
	for _, sc := range cfg.Sources {
		sourceConfigs[sc.ID] = sc
	}
	m.cfg = cfg
	m.sources = srcs
	m.sourceNext = sourceNext
	m.panelItems = panelItems
	m.panels = panels
	m.panelOrder = panelOrder
	m.activePanel = types.PanelNews
	m.panelInterval = panelInterval
	m.sourceConfigs = sourceConfigs
	m.errors = make(map[string]error)
	m.filterActive = false
	m.filterInput.SetValue("")
	m.detailOpen = false
	m.readerOpen = false
	m.configPath = path
	if m.notifier != nil {
		m.notifier.UpdateConfig(cfg.Notifications)
	}
	return m
}

func (m Model) menuView(width, height int) string {
	border := asciiBorder()
	box := lipgloss.NewStyle().
		Border(border).
		BorderForeground(lipgloss.Color(m.cfg.Theme.BorderActive)).
		Padding(1, 2).
		Width(max(0, width-4)).
		Height(max(0, height-2))

	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.cfg.Theme.HeaderActive)).
		Bold(true).
		Render("CONFIG SELECT")

	lines := []string{title, ""}
	if len(m.menuItems) == 0 {
		lines = append(lines, "(no config files found)")
	} else {
		for i, path := range m.menuItems {
			prefix := "  "
			if i == m.menuIndex {
				prefix = "> "
			}
			name := path
			if name == m.configPath {
				name = name + " [current]"
			}
			line := prefix + name
			if i == m.menuIndex {
				line = lipgloss.NewStyle().
					Foreground(lipgloss.Color(m.cfg.Theme.SelectedFG)).
					Background(lipgloss.Color(m.cfg.Theme.SelectedBG)).
					Bold(true).
					Render(line)
			}
			lines = append(lines, line)
		}
	}
	if m.menuError != "" {
		lines = append(lines, "", lipgloss.NewStyle().Foreground(lipgloss.Color(m.cfg.Theme.Crit)).Render("error: "+m.menuError))
	}

	contentHeight := max(0, height-4)
	start := clamp(0, 0, max(0, len(lines)-contentHeight))
	end := min(len(lines), start+contentHeight)
	visible := strings.Join(lines[start:end], "\n")
	return box.Render(visible)
}

func summarizeCmd(cfg config.OllamaConfig, item *types.Item) tea.Cmd {
	return func() tea.Msg {
		if item == nil {
			return summaryMsg{Err: fmt.Errorf("no item")}
		}
		if cfg.BaseURL == "" || cfg.Model == "" {
			return summaryMsg{Err: fmt.Errorf("ollama not configured")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		base, err := url.Parse(cfg.BaseURL)
		if err != nil {
			return summaryMsg{Err: err}
		}
		client := ollama.NewClient(base, http.DefaultClient)
		prompt := buildSummaryPrompt(item)
		req := &ollama.GenerateRequest{
			Model:  cfg.Model,
			Prompt: prompt,
		}
		stream := false
		req.Stream = &stream
		req.Options = map[string]any{
			"temperature": 0.2,
			"top_p":       0.9,
			"num_predict": 256,
		}
		var out strings.Builder
		err = client.Generate(ctx, req, func(resp ollama.GenerateResponse) error {
			out.WriteString(resp.Response)
			return nil
		})
		if err != nil {
			return summaryMsg{Err: err}
		}
		text := strings.TrimSpace(out.String())
		if text == "" {
			return summaryMsg{Err: fmt.Errorf("empty summary")}
		}
		lines := wrapParagraphs(text, 80)
		return summaryMsg{Title: "SUMMARY: " + item.Title, Lines: lines, Text: text}
	}
}

func buildSummaryPrompt(item *types.Item) string {
	body := cleanSummary(item.Summary)
	if body == "" {
		body = item.Title
	}
	return "Summarize the following item. Output plain text only (no markdown, no URLs). Use this format exactly:\n" +
		"TLDR: <one sentence>\n" +
		"KEY POINTS:\n" +
		"- <bullet>\n" +
		"- <bullet>\n" +
		"- <bullet>\n" +
		"- <bullet>\n" +
		"WHY IT MATTERS: <one short sentence>\n\n" +
		"Title: " + item.Title + "\n\n" +
		"Content:\n" + body + "\n"
}

func speakCmd(cfg config.TTSConfig, text string) tea.Cmd {
	return func() tea.Msg {
		if strings.TrimSpace(text) == "" {
			return ttsMsg{Err: fmt.Errorf("empty text")}
		}
		if cfg.Command != "" {
			command := normalizeTTSCommand(cfg.Command, cfg.ModelPath)
			parts, pipeOut := parseTTSCommand(command, text, cfg.ModelPath)
			if len(parts) > 0 {
				if pipeOut {
					return ttsMsg{Err: runWithPipeOut(parts)}
				}
				cmd := exec.Command(parts[0], parts[1:]...)
				return ttsMsg{Err: cmd.Run()}
			}
		}
		return ttsMsg{Err: speakFallback(text)}
	}
}

func speakFallback(text string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("say", text).Run()
	case "windows":
		cmd := exec.Command("powershell", "-Command", "Add-Type -AssemblyName System.Speech; $speak = New-Object System.Speech.Synthesis.SpeechSynthesizer; $speak.Speak('"+escapePS(text)+"')")
		return cmd.Run()
	default:
		if _, err := exec.LookPath("espeak"); err == nil {
			return exec.Command("espeak", text).Run()
		}
		if _, err := exec.LookPath("spd-say"); err == nil {
			return exec.Command("spd-say", text).Run()
		}
		return fmt.Errorf("no system tts found")
	}
}

func escapePS(text string) string {
	replacer := strings.NewReplacer("'", "''")
	return replacer.Replace(text)
}

func parseTTSCommand(command, text, modelPath string) ([]string, bool) {
	const token = "__TTS_TEXT__"
	const tokenModelPath = "__TTS_MODEL_PATH__"
	hasToken := strings.Contains(command, "{text}")
	hasModelPath := strings.Contains(command, "{model_path}")
	if hasToken {
		command = strings.ReplaceAll(command, "{text}", token)
	}
	if hasModelPath {
		command = strings.ReplaceAll(command, "{model_path}", tokenModelPath)
	}
	parts := strings.Fields(command)
	out := make([]string, 0, len(parts))
	skipNextModelPath := false
	for _, part := range parts {
		if part == "--model_path" && modelPath == "" {
			skipNextModelPath = true
			continue
		}
		if part == token {
			out = append(out, text)
		} else if part == tokenModelPath {
			if modelPath != "" && !skipNextModelPath {
				out = append(out, modelPath)
			}
			skipNextModelPath = false
		} else {
			out = append(out, part)
		}
	}
	if !hasToken {
		out = append(out, text)
	}
	if modelPath != "" && !hasModelPath && len(out) > 0 && out[0] == "tts" && !containsArg(out, "--model_path") {
		out = append(out, "--model_path", modelPath)
	}
	pipeOut := false
	for _, part := range out {
		if part == "--pipe_out" {
			pipeOut = true
			break
		}
	}
	return out, pipeOut
}

func normalizeTTSCommand(command, modelPath string) string {
	if modelPath == "" {
		return command
	}
	if isLikelyModelName(modelPath) {
		return strings.ReplaceAll(command, "--model_path", "--model_name")
	}
	return command
}

func isLikelyModelName(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if strings.HasPrefix(value, "tts_models/") || strings.HasPrefix(value, "vocoder_models/") {
		return true
	}
	if strings.Contains(value, "/") && !strings.Contains(value, ".") && !strings.HasPrefix(value, "/") {
		return true
	}
	return false
}

func runWithPipeOut(cmdArgs []string) error {
	if len(cmdArgs) == 0 {
		return fmt.Errorf("empty command")
	}
	ttsCmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	stdout, err := ttsCmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := ttsCmd.Start(); err != nil {
		return err
	}

	player, err := pickAudioPlayer()
	if err != nil {
		_, _ = io.Copy(io.Discard, stdout)
		_ = ttsCmd.Wait()
		return err
	}
	playerCmd := exec.Command(player[0], player[1:]...)
	playerIn, err := playerCmd.StdinPipe()
	if err != nil {
		_ = ttsCmd.Wait()
		return err
	}
	if err := playerCmd.Start(); err != nil {
		_ = ttsCmd.Wait()
		return err
	}
	_, _ = io.Copy(playerIn, stdout)
	_ = playerIn.Close()
	_ = ttsCmd.Wait()
	return playerCmd.Wait()
}

func pickAudioPlayer() ([]string, error) {
	candidates := [][]string{
		{"aplay"},
		{"paplay"},
		{"ffplay", "-nodisp", "-autoexit", "-"},
		{"play", "-q", "-"},
	}
	for _, cmd := range candidates {
		if _, err := exec.LookPath(cmd[0]); err == nil {
			return cmd, nil
		}
	}
	return nil, fmt.Errorf("no audio player found")
}

func containsArg(args []string, name string) bool {
	for _, arg := range args {
		if arg == name {
			return true
		}
	}
	return false
}

func fetchReader(title, link, kind string) tea.Msg {
	if link == "" {
		return readerLoadMsg{Err: fmt.Errorf("empty url"), Kind: kind, Title: title, Link: link}
	}
	url := link
	if kind == "proxy" {
		url = proxyURL(link)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return readerLoadMsg{Err: err, Kind: kind, Title: title, Link: link}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return readerLoadMsg{Err: err, Kind: kind, Title: title, Link: link}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return readerLoadMsg{Err: fmt.Errorf("%s status %d", kind, resp.StatusCode), Kind: kind, Title: title, Link: link}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return readerLoadMsg{Err: err, Kind: kind, Title: title, Link: link}
	}
	lines := strings.Split(string(body), "\n")
	if kind == "proxy" {
		lines = filterProxyLines(lines)
	} else {
		clean := cleanSummary(strings.Join(lines, "\n"))
		if clean != "" {
			lines = strings.Split(clean, "\n")
		}
	}
	return readerLoadMsg{Title: title, Lines: lines, Kind: kind, Link: link}
}

func filterProxyLines(lines []string) []string {
	out := []string{}
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "Title:") || strings.HasPrefix(trim, "URL Source:") || strings.HasPrefix(trim, "Markdown Content:") {
			continue
		}
		out = append(out, line)
	}
	return out
}

func (m Model) readerView(title string, lines []string, width, height int) string {
	border := asciiBorder()
	box := lipgloss.NewStyle().
		Border(border).
		BorderForeground(lipgloss.Color(m.cfg.Theme.BorderActive)).
		Padding(1, 2).
		Width(max(0, width-4)).
		Height(max(0, height-2))

	titleLine := lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.cfg.Theme.HeaderActive)).
		Bold(true).
		Render(title)
	divider := lipgloss.NewStyle().Foreground(lipgloss.Color(m.cfg.Theme.Border)).Render(strings.Repeat("-", max(0, width-8)))

	wrapped := wrapProxyLines(lines, width-8)
	header := []string{titleLine, divider}
	all := append(header, wrapped...)

	contentHeight := max(0, height-4)
	start := clamp(m.readerOffset, 0, max(0, len(all)-contentHeight))
	end := min(len(all), start+contentHeight)
	visible := strings.Join(all[start:end], "\n")
	return box.Render(visible)
}

func wrapProxyLines(lines []string, width int) []string {
	if width <= 0 {
		return lines
	}
	out := []string{}
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if line == "" {
			out = append(out, "")
			continue
		}
		if len(line) <= width || !strings.Contains(line, " ") {
			out = append(out, line)
			continue
		}
		out = append(out, wrapLines(line, width)...)
	}
	return out
}

func wrapParagraphs(text string, width int) []string {
	paragraphs := strings.Split(text, "\n")
	lines := []string{}
	for _, p := range paragraphs {
		p = strings.TrimSpace(p)
		if p == "" {
			if len(lines) > 0 && lines[len(lines)-1] != "" {
				lines = append(lines, "")
			}
			continue
		}
		lines = append(lines, wrapLines(p, width)...)
	}
	return lines
}

func cleanSummary(text string) string {
	if text == "" {
		return ""
	}
	replacements := []string{
		"<br>", "\n",
		"<br/>", "\n",
		"<br />", "\n",
		"</p>", "\n",
		"<p>", "\n",
		"</div>", "\n",
		"<div>", "\n",
	}
	for i := 0; i < len(replacements); i += 2 {
		text = strings.ReplaceAll(text, replacements[i], replacements[i+1])
		text = strings.ReplaceAll(text, strings.ToUpper(replacements[i]), replacements[i+1])
	}
	text = stripTags(text)
	text = html.UnescapeString(text)
	lines := strings.Split(text, "\n")
	out := []string{}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			if len(out) > 0 && out[len(out)-1] != "" {
				out = append(out, "")
			}
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func stripTags(input string) string {
	var b strings.Builder
	inTag := false
	for _, r := range input {
		if r == '<' {
			inTag = true
			continue
		}
		if r == '>' {
			inTag = false
			continue
		}
		if !inTag {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (m Model) panelHasError(panelID string) bool {
	for _, src := range m.sources {
		if src.Panel() != panelID {
			continue
		}
		if err, ok := m.errors[src.ID()]; ok && err != nil {
			return true
		}
	}
	return false
}

func (m Model) panelErrorLines(panelID string) []string {
	var lines []string
	for _, src := range m.sources {
		if src.Panel() != panelID {
			continue
		}
		if err, ok := m.errors[src.ID()]; ok && err != nil {
			lines = append(lines, fmt.Sprintf("error: %v", err))
		}
	}
	return lines
}

func clamp(val, minVal, maxVal int) int {
	if val < minVal {
		return minVal
	}
	if val > maxVal {
		return maxVal
	}
	return val
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
