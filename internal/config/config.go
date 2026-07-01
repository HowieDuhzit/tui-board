package config

import (
	"errors"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Host          string              `yaml:"host"`
	Profile       string              `yaml:"profile"`
	Refresh       string              `yaml:"refresh"`
	Sources       []SourceConfig      `yaml:"sources"`
	Notifications NotificationConfig  `yaml:"notifications"`
	Panels        map[string]PanelCfg `yaml:"panels"`
	Theme         Theme               `yaml:"theme"`
	Ollama        OllamaConfig        `yaml:"ollama"`
	TTS           TTSConfig           `yaml:"tts"`
}

type PanelCfg struct {
	Title string `yaml:"title"`
}

type SourceConfig struct {
	ID       string            `yaml:"id"`
	Type     string            `yaml:"type"`
	URL      string            `yaml:"url"`
	Panel    string            `yaml:"panel"`
	Interval string            `yaml:"interval"`
	Tags     []string          `yaml:"tags"`
	Headers  map[string]string `yaml:"headers"`
	Mapping  map[string]string `yaml:"mapping"`
	Notify   SourceNotify      `yaml:"notify"`
}

type NotificationConfig struct {
	Enabled  bool           `yaml:"enabled"`
	System   bool           `yaml:"system"`
	Ntfy     NtfyConfig     `yaml:"ntfy"`
	Pushover PushoverConfig `yaml:"pushover"`
	Rules    []Rule         `yaml:"rules"`
}

type NtfyConfig struct {
	BaseURL       string `yaml:"base_url"`
	Topic         string `yaml:"topic"`
	ServerCommand string `yaml:"server_command"`
}

type PushoverConfig struct {
	Enabled  bool   `yaml:"enabled"`
	UserKey  string `yaml:"user_key"`
	APIToken string `yaml:"api_token"`
}

type SourceNotify struct {
	Enabled  *bool    `yaml:"enabled"`
	Device   string   `yaml:"device"`
	Sound    string   `yaml:"sound"`
	NtfyTags []string `yaml:"ntfy_tags"`
}

type Rule struct {
	Panel    string   `yaml:"panel"`
	Severity []string `yaml:"severity"`
	Tags     []string `yaml:"tags"`
	Keywords []string `yaml:"keywords"`
	MinLevel string   `yaml:"min_level"`
	MaxItems int      `yaml:"max_items"`
	Cooldown string   `yaml:"cooldown"`
	AllowAll bool     `yaml:"allow_all"`
}

type OllamaConfig struct {
	BaseURL string `yaml:"base_url"`
	Model   string `yaml:"model"`
}

type TTSConfig struct {
	Command   string `yaml:"command"`
	ModelPath string `yaml:"model_path"`
}

type Theme struct {
	BarFG          string            `yaml:"bar_fg"`
	BarBG          string            `yaml:"bar_bg"`
	Border         string            `yaml:"border"`
	BorderActive   string            `yaml:"border_active"`
	Header         string            `yaml:"header"`
	HeaderActive   string            `yaml:"header_active"`
	HeaderBG       string            `yaml:"header_bg"`
	HeaderActiveBG string            `yaml:"header_active_bg"`
	Text           string            `yaml:"text"`
	Dim            string            `yaml:"dim"`
	SelectedFG     string            `yaml:"selected_fg"`
	SelectedBG     string            `yaml:"selected_bg"`
	Crit           string            `yaml:"crit"`
	Warn           string            `yaml:"warn"`
	Info           string            `yaml:"info"`
	Ok             string            `yaml:"ok"`
	Accent         string            `yaml:"accent"`
	Icons          map[string]string `yaml:"icons"`
}

func Load(path string) (Config, error) {
	if path == "" {
		path = "config.yaml"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Default(), nil
		}
		return Config{}, err
	}
	cfg := Default()
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func Default() Config {
	return Config{
		Host:    "localhost",
		Profile: "default",
		Refresh: "5s",
		Panels: map[string]PanelCfg{
			"alerts":  {Title: "ALERTS"},
			"news":    {Title: "NEWS / FEED"},
			"markets": {Title: "MARKETS"},
			"git":     {Title: "GIT / CI"},
			"system":  {Title: "SERVERS / SYSTEM"},
		},
		Notifications: NotificationConfig{
			Enabled: false,
			System:  false,
			Ntfy: NtfyConfig{
				BaseURL: "http://localhost:8080",
				Topic:   "tui-board",
			},
			Pushover: PushoverConfig{
				Enabled: false,
			},
		},
		Theme: Theme{
			BarFG:          "252",
			BarBG:          "235",
			Border:         "238",
			BorderActive:   "250",
			Header:         "250",
			HeaderActive:   "229",
			HeaderBG:       "235",
			HeaderActiveBG: "238",
			Text:           "247",
			Dim:            "244",
			SelectedFG:     "234",
			SelectedBG:     "229",
			Crit:           "196",
			Warn:           "214",
			Info:           "39",
			Ok:             "82",
			Accent:         "81",
			Icons: map[string]string{
				"alerts":  "!",
				"news":    "*",
				"markets": "$",
				"git":     "~",
				"system":  "#",
			},
		},
		Ollama: OllamaConfig{
			BaseURL: "http://localhost:11434",
			Model:   "llama3.1:8b",
		},
		TTS: TTSConfig{
			Command:   "",
			ModelPath: "",
		},
	}
}

func ParseInterval(raw string, fallback time.Duration) time.Duration {
	if raw == "" {
		return fallback
	}
	if d, err := time.ParseDuration(raw); err == nil {
		return d
	}
	return fallback
}
