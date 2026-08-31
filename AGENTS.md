<!--
SPDX-FileCopyrightText: 2026 TorrPlay

SPDX-License-Identifier: MIT
-->

# Repository Agent Instructions

## Commit Messages & History Hygiene

- **Conventional Commits**: Use `type(scope): concise imperative summary`.
- **Specific Scopes**: Use a specific package or subsystem as the scope when appropriate, for example `fix(controller): ...`, `feat(stream): ...`, or `refactor(httpserver): ...`. Omit the scope for genuinely cross-cutting changes, as in `test: ...`.
- **Subject Formatting**: Keep the subject concise, lowercase after the colon, and without a trailing period.
- **Atomic Commits**: Treat one cohesive change and its supporting refactors and tests as one commit, even when it touches multiple packages. Split changes only when they are independently meaningful and leave the repository correct at each boundary.
- **Body Requirements**: For a non-trivial commit, add a body after a blank line and use `-` bullets. Write each bullet as a complete sentence ending with a period.
- **Content Focus**: Use body bullets to describe observable behavior, important implementation or safety details, and relevant test coverage. Do not narrate file-by-file edits.
- **Timestamp Symmetry**: When rebasing, amending, or squashing commits, ensure `GIT_COMMITTER_DATE` matches `GIT_AUTHOR_DATE`.
- **Atomic Buildability**: Ensure every commit compiles and verifies cleanly: run backend compilation (`go build ./...`), unit tests (`go test ./pkg/... ./internal/...`), linter checks (`golangci-lint run` or `make lint`), and OpenAPI zero-diff check (`go generate ./...`) when modifying backend or schema files; run frontend tests, linting, and build verification (`pnpm --prefix client test -- --run`, `pnpm --prefix client lint`, and `pnpm --prefix client build`) when modifying `client/`.

Example:

```text
fix(controller): move debug profiler to loopback-only listener

- Remove profiling routes from the public HTTP router.
- Serve profiling endpoints from a dedicated loopback listener.
- Start and stop the profiler when the debug setting changes.
- Update the HTTP server logger without restarting the public listener.
- Add coverage for listener isolation, settings transitions, and live logger replacement.
```

## Build & Code Generation

- **OpenAPI Schema Sync**: Run `go generate ./...` whenever modifying `api/api.yaml` to regenerate `internal/api/api.gen.go` and its embedded `swaggerSpec`.
- **Client Type Parity**: Keep TypeScript definitions in `client/lib/types/api.ts` synchronized with OpenAPI schema updates.
- **Zero Diff Verification**: Verify that running `go generate ./...` produces no unstaged changes before committing.

## Testing & Quality Standards

- **Backend Unit Tests & Linting**:
  - Run Go package test suites:
    ```bash
    go test ./pkg/... ./internal/...
    ```
  - Run static analysis and linting across Go packages using `golangci-lint`:
    ```bash
    golangci-lint run
    ```
- **Frontend Tests & Linting**: Run client verification suites:
  ```bash
  pnpm --prefix client test -- --run
  pnpm --prefix client lint
  pnpm --prefix client build
  ```
- **Goroutine Leak Detection**: Use `testutil.VerifyTestMain(m)` inside `TestMain` across test suites to catch leaked goroutines via `goleak`.
- **Test Coverage & Regression Prevention**:
  - Accompany every bug fix, refactor, and new feature with dedicated unit and integration tests covering edge cases, state transitions, and error paths.
  - Run package test coverage profiles to verify complete path coverage:
    ```bash
    go test -cover ./pkg/... ./internal/...
    ```
- **Benchmarks & Performance**: Run Go benchmarks to evaluate hot-path allocations and throughput in storage and stream management:
  ```bash
  go test -run=^$ -bench=. -benchmem ./pkg/storage ./pkg/stream
  ```

## Core Architectural Invariants

- **Lock Ordering & Concurrency**:
  - Never acquire outer/parent mutexes while holding inner resource locks (e.g. storage client vs piece mutexes).
  - Never hold `Controller.mu` across blocking network calls, long-running database operations, or torrent lifecycle drops.
- **Streaming Endpoints & Deadlines**:
  - Disable HTTP read and write deadlines using `http.NewResponseController(w)` on streaming endpoints before calling `http.ServeContent`.
- **Storage Eviction Protection & Dynamic Readahead**:
  - Maintain active range windows (`SetActiveRange` / `ClearActiveRange`) for active streaming readers to protect prefetched pieces from LRU memory eviction.
  - Dynamically scale and redistribute readahead budgets across active readers in `pkg/stream`.
- **Profiler & Debug Listener Isolation**:
  - Debug profiling endpoints (`/debug/pprof`) must only be served from dedicated loopback listeners (`127.0.0.1`) and never exposed on public HTTP routers.
- **Database Transactions & Settings Lifecycle**:
  - Always use `db.View(...)` for read-only BBolt database operations; reserve `db.Update(...)` strictly for mutations.
  - Always bootstrap and update settings using `settings.Merge(existing, settings.Default())` to prevent losing defaults or schema changes.

## Ecosystem Compatibility & Protocol Standards

- **TorrServer API Compatibility**:
  - Preserve legacy TorrServer compatibility endpoints (`/torrents`, `/stream`, `TSViewed`, `TSPlaylist`, `TSTorrentAddFile`, `TSPieceInfo`) alongside `/api/v1` routes to maintain interoperability with external players (Kodi, VLC, TorrServe mobile clients).
- **CORS & UPnP GENA Methods**:
  - Always allow custom UPnP eventing HTTP verbs (`SUBSCRIBE`, `UNSUBSCRIBE`, `NOTIFY`) in CORS configuration and router methods to ensure local DLNA event subscriptions work reliably.

## DLNA & UPnP Media Server Standards

- **Container Hierarchy & Validation**:
  - Use structured IDs (`0` for root, `4` for Categories, `category:<name>` for category containers).
  - Validate IDs with `isItemID` rather than naive path splitting to preserve compatibility with category names containing slashes.
- **Streaming Headers & Renderer Compatibility**:
  - Attach `contentFeatures.dlna.org` and `transferMode.dlna.org: Streaming` headers to media streaming responses.
- **Synchronous Service Lifecycle**:
  - Ensure `Service.Stop()` releases mutex locks before waiting on `broadcastDone.Wait()` to prevent deadlocks and eliminate leaked background broadcast goroutines.

## Downloader & Torrent Metadata Invariants

- **Streaming Bandwidth Prioritization**:
  - Automatically pause background downloading whenever active stream readers are acquired on any torrent.
- **Metadata Caching & Instant Resolution**:
  - Use `loadTorrentSpec` with pre-loaded `InfoBytes` on `.torrent` uploads for instant 0ms `GotInfo()` resolution.
  - Preserve `InfoBytes` across in-memory to file storage migrations.

## Frontend Patterns & Media Engine Standards

- **Split Container/Layout Pattern**: Separate stateful container components from pure presentation layouts (e.g. `SettingsDialog` vs `SettingsDialogLayout`, `TorrentStatsDialog` vs `TorrentStatsDialogLayout`) to allow isolated UI unit testing without mocking stores or network layers.
- **Accessibility & Focus Restoration**:
  - Restore keyboard focus to triggering elements when dismissing dialogs using `DialogTriggerContext` or Radix `onCloseAutoFocus`.
  - Apply the HTML `inert` attribute to background containers while modal dialogs are active.
- **Audio Demuxing & Decoding**:
  - Use `MkvAudioSyncEngine` with Mediabunny and WASM decoders for formats not natively supported by browsers (AC-3, E-AC-3, DTS).
  - Route standard browser audio codecs (AAC, MP3, Opus, FLAC) directly to native HTML5 audio elements to minimize CPU and battery overhead.
  - Probe audio tracks for every supported container, not only Matroska, to preserve multi-track selection for MP4 and WebM as well as MKV.
  - Select the container's declared default audio track using Mediabunny track disposition metadata. Treat that track as the browser-owned native track and route other selections through the Web Audio sync engine.
- **Embedded Subtitle Extraction**:
  - Keep the bounded Matroska/WebM range reader in `client/lib/mkv-subtitles.ts`. Mediabunny provides audio demuxing but does not currently expose equivalent high-level input subtitle cue extraction.
  - Preserve seek-index and bounded-range behavior so subtitle selection and seeking never require downloading or rescanning the entire media file.
- **Vidstack Media Element Access**:
  - Resolve Vidstack's underlying video element only through `client/lib/vidstack-media.ts`.
  - Keep provider and DOM compatibility fallbacks inside that adapter when upgrading Vidstack.
- **Interactive Demo Mode & Mock Parity**:
  - Maintain the standalone `/demo` route (`client/app/demo`) for zero-backend client testing and visual verification.
  - Reuse identical presentation layouts (`SettingsDialogLayout`, `TorrentStatsDialogLayout`, `TorrentPlayerDialogLayout`) in `/demo` to preserve full visual, functional, and accessibility parity with the live client.
  - Provide realistic mock torrents, dynamic download simulation, reader positions, and audio track fixtures in `client/lib/demo-*`.
  - Mirror all new settings and torrent features in `/demo` state handlers with dedicated unit test coverage.

## Cross-Platform Client Targets

- **Android / Capacitor**: Account for mobile safe area insets and handle magnet/file intents via Capacitor intent plugins.
- **Desktop / Tauri**: Use `@tauri-apps/plugin-opener` for native desktop file dialogs and shell interactions with progressive browser fallback.
