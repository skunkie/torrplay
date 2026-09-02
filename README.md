<!--
SPDX-FileCopyrightText: 2026 TorrPlay

SPDX-License-Identifier: MIT
-->

# TorrPlay

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Version](https://img.shields.io/github/go-mod/go-version/torrplay/torrplay)](https://go.dev/)
[![Go Reference](https://pkg.go.dev/badge/github.com/torrplay/torrplay.svg)](https://pkg.go.dev/github.com/torrplay/torrplay)
[![Next.js Version](https://img.shields.io/badge/Next.js-15-black?style=flat&logo=next.js)](https://nextjs.org/)
[![Build Status](https://github.com/torrplay/torrplay/actions/workflows/release.yml/badge.svg)](https://github.com/torrplay/torrplay/actions)
[![Latest Version](https://img.shields.io/github/v/release/torrplay/torrplay)](https://github.com/torrplay/torrplay/releases)
[![Docker](https://img.shields.io/badge/ghcr.io-torrplay%2Ftorrplay-blue?logo=docker)](https://github.com/torrplay/torrplay/pkgs/container/torrplay)
[![GitHub Stars](https://img.shields.io/github/stars/torrplay/torrplay?style=social)](https://github.com/torrplay/torrplay/stargazers)

**Stream torrents instantly. No waiting for downloads.**

[Русская версия](README-ru.md)

TorrPlay is a torrent streaming application with a user-friendly interface. Simply paste a magnet link, pick a video file, and start watching.

<p align="center">
  <a href="docs/main.png"><img src="docs/main.png" width="360" alt="TorrPlay main screen"></a>
  <a href="docs/settings.png"><img src="docs/settings.png" width="360" alt="TorrPlay settings"></a>
</p>

<p align="center">
  <a href="https://torrplay.github.io">Docs</a> ·
  <a href="https://torrplay.vercel.app/demo">Live Demo</a>
</p>

## Quick Start

```bash
docker run -d \
  --name torrplay \
  -p 8090:8090 \
  -v $(pwd)/data:/app/data \
  --restart unless-stopped \
  ghcr.io/torrplay/torrplay:latest \
  --data-dir /app/data
```

Open `http://localhost:8090` in your browser.

More options: [Download prebuilt binaries](https://torrplay.github.io/download/) · [Running with Docker](https://torrplay.github.io/quick-start/running-with-docker/) · [Building from Source](https://torrplay.github.io/quick-start/building-from-source/)

## Highlights

- **Instant streaming** — watch while downloading
- **Built-in video player** — supports MP4, MKV, WebM and more in the browser
- **Web UI** — filters, sorting and search
- **DLNA** — cast to smart TVs and other devices
- **Mobile** — Android app available
- **qBittorrent API** — works with Prowlarr, Radarr, Sonarr
- **TorrServer compatible** — works with TorrServer clients
- **Dual storage** — RAM or disk
- **Model Context Protocol (MCP)** — control and stream torrents directly via AI assistants (Claude Desktop, Cursor, etc.)

## Links

- [Download](https://torrplay.github.io/download)
- [Releases](https://github.com/torrplay/torrplay/releases)
- [Docs](https://torrplay.github.io/docs)
- [API Reference](https://torrplay.github.io/docs/api)

## License

[MIT](LICENSE)
