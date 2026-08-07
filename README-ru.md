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

**Смотрите торренты онлайн, не дожидаясь загрузки.**

[English version](README.md)

TorrPlay — это приложение для онлайн-просмотра торрентов с удобным веб-интерфейсом. Просто вставьте magnet-ссылку, выберите нужный видеофайл и наслаждайтесь просмотром.

<p align="center">
  <a href="docs/main.png"><img src="docs/main.png" width="360" alt="TorrPlay main screen"></a>
  <a href="docs/settings.png"><img src="docs/settings.png" width="360" alt="TorrPlay settings"></a>
</p>

<p align="center">
  <a href="https://torrplay.github.io/ru">Документация</a> ·
  <a href="https://torrplay.vercel.app/demo">Демо</a>
</p>

## Быстрый старт

```bash
docker run -d \
  --name torrplay \
  -p 8090:8090 \
  -v $(pwd)/data:/app/data \
  --restart unless-stopped \
  ghcr.io/torrplay/torrplay:latest \
  --data-dir /app/data
```

Откройте `http://localhost:8090` в браузере.

Другие варианты: [Скачать готовые сборки](https://torrplay.github.io/ru/download/) · [Запуск в Docker](https://torrplay.github.io/ru/quick-start/running-with-docker/) · [Сборка из исходного кода](https://torrplay.github.io/ru/quick-start/building-from-source/)

## Возможности

- **Просмотр без загрузки** — начните смотреть сразу, пока файл загружается
- **Встроенный плеер** — поддерживаются MP4, MKV, WebM и другие форматы прямо в браузере
- **Интерфейс** — фильтры, сортировка и поиск по торрентам
- **DLNA** — трансляция на Smart TV и другие устройства
- **Мобильная версия** — есть приложение для Android
- **Совместимость с qBittorrent API** — работает с Prowlarr, Radarr, Sonarr
- **Замена TorrServer** — совместимость с клиентами TorrServer
- **Два режима хранения** — в оперативной памяти или на диске

## Ссылки

- [Скачать](https://torrplay.github.io/ru/download)
- [Релизы](https://github.com/torrplay/torrplay/releases)
- [Документация](https://torrplay.github.io/ru/docs)
- [Справочник API](https://torrplay.github.io/ru/docs/api)

## Лицензия

[MIT](LICENSE)
