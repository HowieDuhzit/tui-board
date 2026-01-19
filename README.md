# TUI Board

A Go + Bubble Tea TUI notification board that pulls data from RSS and JSON APIs and can push notifications to a self-hosted ntfy server.

## Quick start

1. Copy `config.sample.yaml` to `config.yaml` and update sources.
2. (Optional) Install and run `ntfy` for push notifications.
3. Run the TUI.

```
go run ./cmd/tui-board -config config.yaml
```

## Config highlights

- `sources`: RSS/Atom or JSON API endpoints with per-source intervals.
- `panels`: Customize panel titles.
- `notifications`: Rules and ntfy connection settings.
- `theme`: Colors and panel icons.

### JSON API mapping

The JSON source expects an array of objects or an object with an `items` array. By default it reads common keys, and you can override with `mapping`:

```
mapping:
  id: id
  title: title
  summary: summary
  link: link
  time: time
  severity: severity
  tags: tags
```

`time` should be RFC3339. `tags` can be a comma-delimited string.

## Notifications

Set `notifications.enabled: true` and update `notifications.ntfy` to point to your self-hosted server. You can optionally let the app start ntfy by setting `server_command`.

Example:

```
notifications:
  enabled: true
  ntfy:
    base_url: http://localhost:8080
    topic: tui-board
    server_command: "ntfy serve --listen-http :8080"
```

## Key bindings

- `tab`: cycle panels
- `j/k`: move selection
- `f`: filter
- `r`: refresh sources
- `c`: open config selector
- `enter`: open item link via proxy reader (fallback to direct)
- `o`: open item link in external browser
- `s`: summarize selected item via Ollama
- `i`: open item detail
- `t`: speak last summary (TTS)
- `q`: quit

## Notes

- If `ntfy` is not installed, leave `server_command` empty.
- Some APIs may require headers; use `headers` in the source config.
- To show service health, add a JSON source that targets the `system` panel.
- GitHub release feeds can be added with `https://github.com/<owner>/<repo>/releases.atom`.
- Preset configs: `config.tech.yaml`, `config.crypto.yaml`, `config.games.yaml`, `config.news.yaml`.
- Summaries require Ollama running and the configured model pulled locally.
- TTS uses `tts.command` if set; otherwise falls back to OS TTS (`say`, `espeak`, `spd-say`).
- Coqui TTS example: `tts --text {text} --model_path {model_path} --pipe_out --out_path /tmp/tui-board-tts.wav` (plays via `aplay`/`paplay`/`ffplay`/`play`).
- Set `tts.model_path` to the local model file path; model names like `tts_models/en/ljspeech/vits` are also accepted and will be routed to `--model_name`.
- Notifications: enable `notifications.system` for local OS notifications, configure `notifications.ntfy` for mobile push, and `notifications.pushover` for Pushover.
- Per-source notification overrides are available under `sources[].notify` (enable/disable + Pushover device/sound).
