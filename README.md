# System Monitor

A lightweight, local-first DevOps system monitoring dashboard. Single Go
binary backend, zero-build vanilla-JS frontend, real-time metrics over
WebSockets.

![status](https://img.shields.io/badge/status-local--only-22d3ee)

## Overview

System Monitor watches CPU, memory, disk, network, temperature, and
processes on the machine it runs on, and streams updates to any browser
tab pointed at it once per second over a single WebSocket connection. It's
built to be cheap to run continuously: no polling from the frontend, no
database, no external services — just one process and one socket per
connected client.

The frontend assets (`index.html` / `app.js` / `styles.css`) are embedded
into the compiled binary with `go:embed`, so the whole thing ships and
runs as **one executable** with no accompanying files required.

## Features

- **Real-time metrics** over WebSocket (`/ws`), refreshed ~1×/second
  - CPU: overall %, per-core %, load average (1/5/15m), logical core count
  - Memory: total / used / free / usage %
  - Disk: total / used / free / usage % for a configurable mount, plus
    read/write throughput when the OS exposes disk I/O counters
  - Network: live download/upload throughput, cumulative bytes, and a
    per-interface breakdown
  - Temperature: all sensors gopsutil can see, with a clean
    "unavailable" state on hosts without exposed sensors (common in
    containers, VMs, and some laptops)
  - System info: OS, kernel version, hostname, architecture, uptime
  - Processes: top processes by CPU usage (PID, name, CPU %, RSS memory,
    memory %, status), capped to keep payloads small
- **Process termination**, gated behind a confirmation dialog, PID
  validation, and hard-coded protection for PID 1 and the monitor's own
  PID — never shells out, only signals a resolved OS process handle
- **Auto-reconnecting** frontend WebSocket client with exponential backoff
  and a live connection-status indicator
- **REST fallback** endpoints for anything that doesn't want a persistent
  socket
- **Dark, minimal, responsive UI** built with Tailwind (via CDN) and
  Chart.js — no React/Vue/build step
- Defensive collection throughout: a missing sensor, a process that exits
  mid-inspection, or an unsupported metric on the current OS degrades
  gracefully instead of crashing the server

## Architecture

```
system-monitor/
├── cmd/server/main.go       # entrypoint: config, HTTP server, wiring, graceful shutdown
├── internal/
│   ├── collector/           # 1×/sec metrics sampling (CPU/mem/disk/net/temp/procs)
│   ├── websocket/           # broadcast hub + per-client read/write pumps
│   ├── process/              # validated process termination
│   ├── api/                 # REST + /ws route handlers (thin, delegate only)
│   └── system/               # static host identity (hostname, kernel, uptime)
├── web/                     # frontend, embedded into the binary via go:embed
│   ├── index.html
│   ├── app.js
│   ├── styles.css
│   └── embed.go
├── go.mod
└── README.md
```

**Data flow:** `collector.Run` ticks once per `UPDATE_INTERVAL`, gathers a
full `Metrics` snapshot defensively (each subsystem is isolated — one
failing sensor never blocks the others), stores it as the "latest"
snapshot for REST reads, and hands it to `hub.Broadcast`. The hub
marshals it to JSON exactly once and fans it out to every connected
client's buffered send channel. A client that can't keep up (full buffer)
is dropped rather than stalling the broadcast for everyone else. Each
client runs two goroutines (`readPump`/`writePump`) that exit cleanly on
disconnect — no goroutine leaks under normal churn.

## Installation

Requirements: Go 1.24+.

```bash
git clone <this-repo>
cd system-monitor
go mod tidy
```

`go mod tidy` will resolve and download:

- [`github.com/gin-gonic/gin`](https://github.com/gin-gonic/gin) — HTTP routing
- [`github.com/gorilla/websocket`](https://github.com/gorilla/websocket) — WebSocket transport
- [`github.com/shirou/gopsutil/v3`](https://github.com/shirou/gopsutil) — cross-platform system metrics

## Build

```bash
go build -o system-monitor ./cmd/server
```

This produces a single, self-contained binary (frontend included).

## Run

```bash
go run ./cmd/server
# or, after building:
./system-monitor
```

Then open **http://127.0.0.1:8080**.

### Configuration

All configuration is via environment variables, all optional:

| Variable          | Default     | Description                                   |
|-------------------|-------------|------------------------------------------------|
| `HOST`             | `127.0.0.1` | Bind address. Change to `0.0.0.0` to expose beyond localhost — see Security notes first. |
| `PORT`             | `8080`      | Bind port                                       |
| `UPDATE_INTERVAL`  | `1s`        | Collection/broadcast interval (Go duration string, e.g. `500ms`, `2s`; a bare integer is treated as seconds) |
| `DISK_PATH`        | `/`         | Mount point/path to report disk usage for       |

Example:

```bash
HOST=0.0.0.0 PORT=9090 UPDATE_INTERVAL=2s ./system-monitor
```

## Development

- `go run ./cmd/server` for iterative backend work — restart to pick up
  Go changes.
- Frontend files under `web/` are embedded at **compile time**. During
  frontend-only iteration you can serve `web/` directly with any static
  file server for instant reload, then re-run `go build` once you're
  happy so the embed picks up your changes for the shipped binary.
- `go vet ./...` and `gofmt -l .` before sending changes.

## API Documentation

### `GET /api/system`
Returns the most recently collected full metrics snapshot (same shape as
the WebSocket payload, see below). Useful for a one-shot poll instead of
holding a socket open.

### `GET /api/processes`
Returns `{ "processes": [...] }` — the process list from the latest
snapshot.

### `POST /api/process/terminate`
Body: `{ "pid": 1234 }`

Response: `{ "pid": 1234, "success": true|false, "message": "..." }`

Validation performed server-side, in order:
1. `pid` must be a positive integer
2. `pid` may not be `1` (init)
3. `pid` may not be the monitoring server's own PID
4. The process must currently exist (`IsRunning`)
5. Termination is attempted gracefully first; permission errors are
   reported back verbatim rather than silently retried with elevated
   force

No shell is ever invoked — termination goes through the OS process API
only.

### `GET /api/health`
Returns `{ "status": "ok", "uptimeSeconds": N, "connectedPeers": N }`.

### `GET /api/info`
Returns static system info plus server start time.

## WebSocket Documentation

### `GET /ws`
Upgrades to a WebSocket connection. The server pushes a JSON `Metrics`
object roughly once per `UPDATE_INTERVAL`; the client is not expected to
send anything (control frames like ping/pong are handled automatically).

Example payload:

```json
{
  "timestamp": 1737400000000,
  "cpu": { "usage": 42.3, "perCore": [40.1, 44.5], "cores": 8, "load1": 1.2, "load5": 1.0, "load15": 0.9, "available": true },
  "memory": { "total": 16000000000, "used": 8200000000, "free": 7800000000, "usage": 51.2, "available": true },
  "disk": { "total": 500000000000, "used": 230000000000, "free": 270000000000, "usage": 46.0, "path": "/", "io": { "readBytesPerSec": 0, "writeBytesPerSec": 0 }, "available": true },
  "network": { "download": 1250000, "upload": 350000, "totalReceived": 0, "totalTransmitted": 0, "interfaces": [], "available": true },
  "temperatures": [{ "sensor": "coretemp_core_0", "temperature": 54.0 }],
  "processes": [{ "pid": 1234, "name": "chrome", "cpuPercent": 12.4, "memBytes": 210000000, "memPercent": 3.1, "status": "running" }],
  "system": { "os": "linux", "platform": "ubuntu", "kernelVersion": "6.8.0", "hostname": "dev-box", "architecture": "amd64", "uptimeSeconds": 93000, "bootTime": 0 }
}
```

The frontend reconnects automatically with exponential backoff
(1s → 15s cap) if the socket drops, and shows a live/connecting/
reconnecting indicator in the header.

## Security Notes

This tool is designed for **local use**:

- Binds to `127.0.0.1` by default — not reachable from other machines
  unless you explicitly set `HOST=0.0.0.0` (or another externally
  reachable address).
- Never executes shell commands or user-provided strings. Process
  termination goes strictly through validated PIDs and the OS process
  API.
- PID 1 and the monitor's own PID can never be targeted for termination.
- All API input is validated server-side before use.
- If you do expose this beyond localhost, put it behind your own
  authentication/reverse proxy — there is no built-in auth, since it's
  designed to be a trusted local tool, not a multi-tenant service.

## Troubleshooting

- **"Temperature data unavailable"** — normal on many VMs, containers,
  and some laptops/desktops where the OS doesn't expose sensor data to
  userspace. The rest of the dashboard is unaffected.
- **Disk I/O shows nothing on first sample** — I/O throughput is a delta
  between two samples, so the very first tick after startup has nothing
  to diff against; it appears on the second tick.
- **Process list looks short / some processes missing entirely** — the
  list is capped (currently top 50 by CPU, with the top 20 shown in the
  UI) to keep broadcast payloads small, and processes you don't have
  permission to inspect are skipped rather than shown with blank fields.
- **"permission denied" on terminate** — the monitor process doesn't have
  permission to signal that PID (e.g. it's owned by another user or
  running with elevated privileges). Run the monitor with sufficient
  privileges if you need to manage those processes, or leave them alone.
- **WebSocket stuck on "Reconnecting…"** — confirm the server process is
  still running and that nothing (proxy, firewall, browser extension) is
  blocking WebSocket upgrades to the configured `HOST:PORT`.

## Cross-Platform Limitations

The collector is built around `gopsutil`, which supports Linux, macOS,
and Windows, but coverage of some metrics varies by OS:

- **Linux** — fully supported, including per-core CPU, disk I/O counters,
  and (where exposed) sensor temperatures. Primary target platform.
- **macOS** — CPU/memory/disk/network/process metrics work; temperature
  sensor access is limited/unavailable on many configurations and will
  correctly show "Temperature data unavailable" rather than fail.
- **Windows** — CPU/memory/disk/network/process metrics work through
  gopsutil's Windows backends; temperature and some disk I/O counters may
  be unavailable depending on hardware/driver support.

Everywhere a metric can't be read on the current platform, the collector
returns a safe empty/zero value (and an `"available": false` flag where
applicable) instead of erroring out, so the dashboard keeps running with
whatever data the OS can actually provide.

## License

Provided as-is for local development and operations tooling use.
