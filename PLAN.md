# TUI Board Plan (MVP)

This plan is the source of truth for the build. Updates should keep this file in sync with progress.

## Phase 0: Project skeleton (0.5 day)
- [x] Initialize Go module + Bubble Tea + Lipgloss.
- [x] Define core types: Panel, Item, Source, Msg.
- [x] Basic layout grid matching the reference example (static placeholders).

## Phase 1: Data ingest (1-1.5 days)
- [x] Implement RSS/Atom source.
- [x] Implement JSON API source (generic fetch + mapping).
- [x] Add cache + refresh scheduler (per-source TTL, backoff, jitter).
- [x] Wire sources into panels.

## Phase 2: UI behavior (1 day)
- [x] Scrolling, selection, filter input.
- [x] Panel focus switching + footer commands.
- [x] Color styles + status bar clock.

## Phase 3: Notifications (1 day)
- [x] Define notification rules (severity, tags, keywords).
- [x] Bundle ntfy (local process or documented sidecar).
- [x] Push notifications from rules to ntfy topic.

## Phase 4: System panel (1 day)
- [x] CPU/MEM/DISK/NET stats via gopsutil or equivalent.
- [x] Service health list (from API or local checks).

## Phase 5: Polish (0.5-1 day)
- [x] Config YAML schema + sample file.
- [x] Error handling, offline states, retry UI badges.
- [x] Basic docs: setup, config, running.
