# Activity Monitor

A lightweight macOS process monitor with a Go backend and an embedded pixel-art web UI.

## Features

- Low-overhead process sampling with cached snapshots.
- macOS-friendly process list powered by `ps`, `sysctl`, and `vm_stat`.
- Single binary deployment; the frontend is embedded in the Go executable.
- Pixel-style dashboard with search, sortable columns, summary cards, and responsive layout.

## Run

```bash
go run ./server
```

Open <http://localhost:8080>.

Set a custom bind address with:

```bash
ADDR=:9090 go run ./server
```

## Build

```bash
go build -o activity-monitor ./server
./activity-monitor
```

## API

- `GET /api/processes` returns the latest cached process snapshot.
- `GET /healthz` returns a readiness probe response.