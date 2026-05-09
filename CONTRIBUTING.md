<!--
SPDX-FileCopyrightText: 2026 TorrPlay

SPDX-License-Identifier: MIT
-->

# Contributing to TorrPlay

First off, thank you for considering contributing to TorrPlay! It's people like you that make TorrPlay such a great tool.

## Where do I go from here?

If you've noticed a bug or have a feature request, [make one](https://github.com/torrplay/torrplay/issues/new)! It's generally best if you get confirmation of your bug or approval for your feature request this way before starting to code.

### Fork & create a branch

If this is something you think you can fix, then [fork TorrPlay](https://github.com/torrplay/torrplay/fork) and create a branch with a descriptive name.

A good branch name would be (where issue #42 is the ticket you're working on):

```sh
git checkout -b 42-add-awesome-new-feature
```

### Get the project running

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

### Code Structure

Here is a brief overview of the project structure to help you get started:

*   **`client/`**: Contains the Next.js frontend application. All UI-related changes go here. It also includes configurations for Capacitor (for mobile) and Tauri (for desktop).
*   **`cmd/torrplay/`**: The main entry point for the Go backend server.
*   **`internal/`**: This directory holds the core logic of the application.
    *   **`api/`**: OpenAPI specification and generated code.
    *   **`auth/`**: Authentication logic (Basic and Bearer).
    *   **`controller/`**: The main business logic, including torrent management, qBittorrent compatibility, and API endpoint handlers. This is where most of the core functionality resides.
    *   **`database/`**: The database layer, currently using `bbolt`.
    *   **`downloader/`**: The torrent downloading and piece management logic.
    *   **`httpserver/`**: The HTTP server setup and middleware.
*   **`pkg/torrplay/`**: The core application logic packaged as a library, which can be bound for use in mobile applications using `gomobile`.
*   **`api/`**: Contains the OpenAPI specification for the REST API.
*   **`web/`**: Contains the Go web server and serves the static files generated from the `client` directory.

A good place to start is by looking at `internal/controller/endpoints.go` to see how the API endpoints are handled and then tracing the logic from there.

### Writing and Running Tests

We value the stability and reliability of TorrPlay, and tests are a crucial part of that. All contributions should be accompanied by tests, whether you're fixing a bug or adding a new feature.

#### Backend Tests

The backend is written in Go. You can run all backend tests from the root of the project:

```sh
go test ./...
```

When you add a new feature or fix a bug in the backend, please add a corresponding test case in the appropriate `_test.go` file.

#### Frontend Tests

The frontend is a Next.js application and uses `vitest` for testing. To run the frontend tests, navigate to the `client` directory and run the test command:

```sh
cd client
pnpm install
pnpm test
```

This will run all the tests for the UI components and frontend logic. Please add tests for any new components or functionality you add to the client.

### Style Guide

#### Go Backend

Please format your Go code with `gofmt` before committing.

#### Frontend

The frontend uses ESLint to enforce a consistent coding style. Please run `pnpm run lint` in the `client` directory to check for any linting errors.

### Commit Message Guidelines

We follow the [Conventional Commits](https://www.conventionalcommits.org/) specification. This allows for a more readable commit history and helps in automating changelog generation.

Each commit message should have the following format:

```
<type>[optional scope]: <description>

[optional body]

[optional footer]
```

**Example:**

```
feat(api): add new endpoint for torrent stats

A new endpoint `/api/v1/torrents/{hash}/stats` has been added to provide
real-time statistics for a torrent.

Fixes #42
```

### Push your changes & create a pull request

Push your changes to your fork and then [create a pull request](https://github.com/torrplay/torrplay/compare).

## Code of Conduct

We have a [Code of Conduct](./CODE_OF_CONDUCT.md), please follow it in all your interactions with the project.

## License

By contributing to TorrPlay, you agree that your contributions will be licensed under its [MIT License](./LICENSE).
