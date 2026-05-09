<!--
SPDX-FileCopyrightText: 2026 TorrPlay

SPDX-License-Identifier: MIT
-->

# TorrPlay

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

TorrPlay is a torrent streaming application written in Go, featuring memory-managed piece storage. It allows you to stream torrent content directly over HTTP without needing to download the entire file first.

## Features

*   **HTTP Streaming:** Stream video and other files directly from a torrent.
*   **Memory-Managed Storage:** Intelligently caches torrent pieces in memory to reduce disk I/O and improve performance.
*   **Web UI:** A simple web interface for managing torrents.
*   **RESTful API:** A comprehensive API for programmatic control. See the [OpenAPI specification](api/api.yaml).
*   **TorrServer Compatibility:** Includes compatibility endpoints for clients that use the TorrServer API.
*   **qBittorrent Compatibility:** Emulates the qBittorrent API for seamless integration with tools like Prowlarr and Radarr.
*   **Mobile Ready:** The core logic is structured as a library that can be bound for use in mobile applications with `gomobile`.

## Storage

TorrPlay offers two distinct storage backends for managing torrent data, allowing you to choose the best fit for your needs:

### Memory Storage (Default)

By default, TorrPlay uses an in-memory storage system. Torrent pieces are downloaded and cached in RAM up to a configurable limit. When this limit is reached, a Least Recently Used (LRU) eviction policy is triggered to discard the oldest pieces, making room for new ones.

*   **Pros:** High performance, reduced disk I/O, ideal for streaming.
*   **Cons:** Volatile (data is lost on restart), limited by available RAM.

### File Storage

Alternatively, you can configure TorrPlay to use file-based storage. In this mode, torrent pieces are saved directly to the filesystem. This is suitable for building a persistent library of content or for long-term seeding.

*   **Pros:** Persistent storage that survives application restarts, not limited by RAM.
*   **Cons:** Slower than memory storage, increased disk I/O.

> **Note for Windows Users:** To prevent potential file-locking issues, TorrPlay does not automatically delete torrent files from the filesystem on Windows when you delete a torrent or switch its storage backend to memory. You will need to manually remove these files if you wish to free up disk space.

## qBittorrent Compatibility

TorrPlay provides a qBittorrent-compatible API endpoint that allows it to be used with popular automation tools like Prowlarr, Sonarr, and Radarr. This feature enables you to add torrents to TorrPlay directly from these applications as if it were a qBittorrent client.

To add a torrent using the qBittorrent compatibility API, you can send a `POST` request to the `/api/v2/torrents/add` endpoint. The request should be a `multipart/form-data` request containing the torrent file or a magnet link.

### Example

Here is an example of how to add a torrent using `curl`:

```sh
curl -X POST http://localhost:8090/api/v2/torrents/add \
-F "urls=magnet:?xt=urn:btih:dd8255ecdc7ca55fb0bbf81323d87062db1f6d1c&dn=Big+Buck+Bunny" \
-F "category=Movies"
```

In this example:

*   `urls`: The magnet link of the torrent.
*   `category`: The category to assign to the torrent (e.g., `Movies`, `Series`).

## Metadata Fetching

TorrPlay can enrich your torrent library by fetching metadata such as posters, titles, and categories from external sources. This functionality is available through the command-line interface.

### How to Use

To update the metadata for your torrents, you can run TorrPlay with the following options:

*   `--backup <path>`: Path to the input backup file (default: `torrplay.backup`).
*   `--output <path>`: Path to the output file where the updated backup will be saved (default: `torrplay.backup`).
*   `--category`: Enable updating of categories.
*   `--poster`: Enable updating of posters.
*   `--title`: Enable updating of titles.
*   `--language <lang>`: The language for metadata fetching (e.g., `eng`, `spa`).
*   `--provider <provider>`: The metadata provider to use. Currently, only `tvdb` is supported.
*   `--api-key <key>`: Your API key for the chosen metadata provider.

### Example

Here is an example of how to update your backup file with posters and titles from TheTVDB:

```sh
./torrplay --backup torrplay.backup --poster --title --provider tvdb --api-key YOUR_TVDB_API_KEY
```

This command will read the `torrplay.backup` file, fetch the metadata, and write the updated data to the `torrplay.backup.updated` file.

## Authentication

TorrPlay secures its API endpoints using two primary authentication methods, which can be configured in the application settings:

1.  **Basic Authentication (`basic`)**: With this method, you must send your username and password with every API request. It applies to all endpoints, but it does not protect the streaming endpoints (`/api/v1/stream/*`), allowing for easy access from media players that do not handle complex authentication.

2.  **Bearer Token Authentication (`bearer`)**: This is a more secure, token-based method using JSON Web Tokens (JWT). When enabled, all endpoints are protected. Authentication for the streaming endpoints is handled seamlessly via a session cookie.

By default, authentication is disabled. You can enable and configure it through the `/api/v1/settings` endpoint.

### Obtaining a Token (Bearer Auth Only)

The `/oauth/token` endpoint is **only available when `bearer` authentication is enabled**. Its purpose is to exchange your username and password for an `access_token` and a session cookie.

To obtain a token, you must make a `POST` request to the `/oauth/token` endpoint with your credentials in the request body, using the `application/x-www-form-urlencoded` format.

```sh
curl -X POST http://localhost:8090/oauth/token \
-H "Content-Type: application/x-www-form-urlencoded" \
-d "grant_type=password&username=admin&password=your-password"
```

The endpoint will return a JSON response with the token and set a secure, `HttpOnly` session cookie in the browser.

### Making Authenticated API Requests

**Using a Bearer Token:**

For most API clients, include the JWT token in the `Authorization` header.

```sh
curl -X GET http://localhost:8090/api/v1/torrents \
-H "Authorization: Bearer your-jwt-token"
```

**Using Basic Authentication:**

If you have `basic` auth enabled, include the username and password with each request.

```sh
curl -u admin:your-password -X GET http://localhost:8090/api/v1/torrents
```

### Cookie Authentication for Streaming (Bearer Auth Feature)

When using `bearer` authentication, TorrPlay provides a seamless experience for browser-based streaming by using the session cookie obtained from the `/oauth/token` endpoint.

*   **How it works**: Your browser will automatically include this cookie in requests to the streaming endpoints (`/api/v1/stream/*`), handling authentication for you.
*   **`HttpOnly`**: The cookie is marked as `HttpOnly`, which means it cannot be accessed by client-side scripts, enhancing security against XSS attacks.

### Configuring Authentication via API

You can enable and configure authentication by sending a `PATCH` request to the `/api/v1/settings` endpoint. If authentication is already enabled, you must include the current credentials in your request.

**Enable Basic Authentication:**

```sh
curl -X PATCH http://localhost:8090/api/v1/settings \
-u current-user:current-password \
-H "Content-Type: application/json" \
-d '{
  "auth": {
    "enabled": true,
    "type": "basic",
    "username": "admin",
    "password": "your-new-password"
  }
}'
```

**Enable JWT (Bearer) Authentication:**

When you enable `bearer` authentication, a secure JWT secret is automatically generated and stored.

```sh
curl -X PATCH http://localhost:8090/api/v1/settings \
-u current-user:current-password \
-H "Content-Type: application/json" \
-d '{
  "auth": {
    "enabled": true,
    "type": "bearer",
    "username": "admin",
    "password": "your-new-password"
  }
}'
```

### Disabling Authentication (Recovery)

If you forget your credentials, you can temporarily disable authentication by setting the `TORRPLAY_DISABLE_AUTH` environment variable to `true`.

```sh
TORRPLAY_DISABLE_AUTH=true ./torrplay --data-dir=./data
```

This allows you to access the API without credentials to reset your settings. Once you have regained access, remove the environment variable and restart the application to re-enable security.

## Getting Started

### Prerequisites

*   Go (version 1.25 or later)
*   Make (for building the web client)

### Installation

1.  **Clone the repository:**
    ```sh
    git clone https://github.com/torrplay/torrplay.git
    cd torrplay
    ```

2.  **Build the web client static files:**
    ```sh
    make client
    ```

3.  **Build the application binary:**
    ```sh
    go build -o torrplay ./cmd/torrplay
    ```

### Running the Application

You can run the application using the binary you just built. It's recommended to specify a data directory for storing configuration and torrent files.

```sh
./torrplay --data-dir=./data
```

By default, the application runs on port `8090`. You can access the web UI by navigating to `http://localhost:8090` in your browser.

## Usage Example

You can interact with TorrPlay using its REST API. For example, to add a new torrent (Big Buck Bunny):

```sh
curl -X POST http://localhost:8090/api/v1/torrents \
-H "Content-Type: application/json" \
-d '{
  "magnet": "magnet:?xt=urn:btih:dd8255ecdc7ca55fb0bbf81323d87062db1f6d1c&dn=Big+Buck+Bunny"
}'
```

Once added, you can get the torrent details from the API to find the specific file path you want to stream. You can then stream it from a URL like:
`http://localhost:8090/api/v1/stream/{hash}?path={url_encoded_path}`

## Development

*   **Run tests:**
    ```sh
    go test ./...
    ```

*   **Build for Mobile (Android):**
    ```sh
    CGO_LDFLAGS="-static-libstdc++" gomobile bind -target=android -androidapi 24 -ldflags="-checklinkname=0" -o torrplay.aar ./pkg/torrplay
    ```

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.
