package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"tui-board/internal/app"
	"tui-board/internal/config"
	"tui-board/internal/notifier"
	"tui-board/internal/sources"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config yaml")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Println("config error:", err)
		os.Exit(1)
	}

	var srcs []sources.Source
	for _, sc := range cfg.Sources {
		src, err := sources.New(sc)
		if err != nil {
			fmt.Println("source error:", err)
			os.Exit(1)
		}
		srcs = append(srcs, src)
	}

	note := notifier.New(cfg.Notifications)
	if err := note.StartServer(); err != nil {
		fmt.Println("notifier error:", err)
		os.Exit(1)
	}
	defer note.Close()

	p := tea.NewProgram(app.New(cfg, note, srcs, *configPath), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Println("error:", err)
		os.Exit(1)
	}
}
