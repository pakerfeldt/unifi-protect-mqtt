# AGENTS.md

This file provides guidance for AI coding agents working in this repository.

## Project Overview

Go application that bridges UniFi Protect smart detection events (person/animal) to MQTT
via WebSocket streaming. Module: `github.com/pa/unifi-protect-mqtt`, Go 1.25.2.

## Build / Lint / Test Commands

```bash
# Build
go build ./...

# Vet (lint)
go vet ./...

# Run
go run .

# Tidy dependencies
go mod tidy

# Run all tests (none exist yet)
go test ./...

# Run a single test
go test -run TestFunctionName ./internal/protect/

# Run tests with verbose output
go test -v -run TestFunctionName ./internal/protect/
```

There is no Makefile, Dockerfile, or CI pipeline. Use plain `go` commands.

## Project Structure

```
main.go                          # Entry point, wiring, signal handling
internal/
  config/config.go               # .env loading, validation, Config struct
  protect/
    client.go                    # HTTP/WS client: auth, bootstrap, event streaming
    types.go                     # Data types (Bootstrap, EventPayload, SmartDetectEvent)
    ws_decoder.go                # Binary WebSocket protocol decoder
  mqtt/
    publisher.go                 # MQTT publishing, topic sanitization
  proxy/
    server.go                    # HTTP proxy for auth-free thumbnail/video access
feature_suggestions.md           # Future feature ideas
```

All packages live under `internal/` — keep it that way. Each package has a single
responsibility. Data types for a package go in `types.go`.

## Code Style

### Imports

Three groups separated by blank lines, each alphabetically ordered:

1. Standard library
2. Third-party packages
3. Internal packages

```go
import (
    "context"
    "fmt"

    "github.com/rs/zerolog"

    "github.com/pa/unifi-protect-mqtt/internal/config"
)
```

Use a single import line (no parens) when importing only one package.
Alias packages only when the name is generic or collides (e.g. `paho` for the MQTT client).

### Formatting

- `gofmt` / `goimports` standard formatting
- Align struct field tags in columns within each struct
- No blank lines between struct fields unless separating logical groups

### Naming Conventions

- **Exported types:** PascalCase — `Client`, `Publisher`, `SmartDetectEvent`
- **Unexported types:** camelCase — `frameType`, `payloadFormat`
- **Exported acronyms:** fully capitalized — `WSMessage`, `MQTTBroker`, `ThumbnailURL`, `LastUpdateID`
- **Unexported acronyms:** mixed — `csrfToken`, `wsURL`
- **Constants (unexported):** camelCase — `headerSize`, `actionFrame`, `cooldown`
- **Constructors:** `New` prefix — `NewClient`, `NewPublisher`
- **Variables:** short names for small scopes (`cfg`, `ctx`, `req`, `resp`, `msg`),
  descriptive for broader scopes (`cameraNames`, `dedupeKey`, `thumbnailURL`)

### Error Handling

Always wrap errors with `fmt.Errorf` using `%w` and a lowercase context prefix:

```go
return nil, fmt.Errorf("creating login request: %w", err)
return nil, fmt.Errorf("bootstrap request: %w", err)
```

Use `errors.New()` for simple sentinel/validation errors:

```go
return nil, errors.New("message too short")
```

For required-field validation, return a descriptive `fmt.Errorf` without wrapping:

```go
return nil, fmt.Errorf("UNIFI_HOST is required")
```

Fatal errors at startup use `logger.Fatal().Err(err).Msg(...)`.
Runtime errors use `logger.Error().Err(err).Msg(...)` and continue.

### JSON Tags

- **Own types** (published to MQTT): `snake_case` — `"event_id"`, `"camera_name"`, `"thumbnail_url"`
- **Types mirroring UniFi API responses**: `camelCase` — `"authUserId"`, `"smartDetectTypes"`, `"lastUpdateId"`

### Logging (zerolog)

- Pass `*zerolog.Logger` pointer to constructors, store in struct
- Use structured fields via method chaining: `.Str()`, `.Int()`, `.Err()`
- Log field names use `snake_case`: `"event_id"`, `"mqtt_broker"`, `"topic_prefix"`
- Log messages are lowercase, concise, present tense: `"authenticated with UniFi Protect"`

```go
c.logger.Info().
    Str("type", detectType).
    Str("camera", cameraName).
    Int("score", payload.Score).
    Msg("smart detection")
```

**Log levels:**
- `Fatal` — unrecoverable startup errors only
- `Error` — runtime failures (publish, reconnect, WS errors)
- `Warn` — degraded state (connection lost)
- `Info` — lifecycle events (connected, authenticated, detection events)
- `Debug` — deduplication skips, detailed publish info

### Comments

- Exported types and functions get `// Name does X.` godoc comments
- Inline comments explain "why" not "what"
- End-of-line `//` for brief struct field annotations
- Multi-line block comments for protocol documentation

### Context

- `context.Context` is always the first parameter
- Created in `main.go` with `context.WithCancel` + signal handling
- Used with `http.NewRequestWithContext` and `dialer.DialContext`
- Checked via `ctx.Done()` in `select` for clean shutdown

### Concurrency

- `sync.Mutex` with `Lock()` / `defer Unlock()` — separate mutexes for separate concerns
- Buffered channels for event bridging: `make(chan T, 64)`
- Send-only channel type `chan<-` in function signatures
- All goroutines must respect `ctx.Done()` for shutdown
- Reconnection uses `time.After` backoff (currently 5s fixed)

## Configuration

Custom `.env` parser (no third-party library). Priority: `.env` file > OS env var > default.
The `.env` file contains secrets — never commit it.

| Variable             | Required | Default                 |
|----------------------|----------|-------------------------|
| UNIFI_HOST           | Yes      | —                       |
| UNIFI_USERNAME       | Yes      | —                       |
| UNIFI_PASSWORD       | Yes      | —                       |
| UNIFI_EXTERNAL_HOST  | No       | `""`                    |
| MQTT_BROKER          | No       | `tcp://localhost:1883`  |
| MQTT_USERNAME        | No       | `""`                    |
| MQTT_PASSWORD        | No       | `""`                    |
| MQTT_TOPIC_PREFIX    | No       | `unifi/protect`         |
| PROXY_LISTEN_ADDR    | No       | `:8080`                 |
| PROXY_BASE_URL       | No       | `http://localhost:8080`  |
| REPLAY_LAST_EVENTS   | No       | `0`                     |

## Dependencies

| Package                              | Alias  | Purpose                    |
|--------------------------------------|--------|----------------------------|
| `github.com/gorilla/websocket`       | —      | WebSocket client           |
| `github.com/eclipse/paho.mqtt.golang`| `paho` | MQTT client                |
| `github.com/rs/zerolog`              | —      | Structured logging         |

Keep dependencies minimal. The UniFi Protect API client is hand-rolled — there is no
third-party UniFi library. TLS verification is disabled (`InsecureSkipVerify: true`)
because UniFi controllers use self-signed certificates.
