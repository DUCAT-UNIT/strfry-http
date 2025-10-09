# Strfry HTTP API Extension

Fork of [strfry](https://github.com/hoytech/strfry) with HTTP REST API support.

## Why?

**Chainlink's Crypto Risk Engine (CRE) doesn't support WebSocket.** This adds HTTP endpoints so CRE can submit/retrieve Nostr events without modifying the core relay logic.

## Quick Start

**Config** (`strfry.conf`):
```conf
relay {
    http {
        enabled = true
        port = 8080
        bind = "0.0.0.0"
        cors = true
    }
}
```

**Build**:
```bash
git clone --recursive https://github.com/your-org/strfry && cd strfry
git submodule update --init
make setup-golpe
make -j$(nproc)
./strfry relay
```

**Docker**:
```yaml
ports:
  - "7777:7777"  # WebSocket
  - "8080:8080"  # HTTP
```

## API Endpoints

### POST /api/quotes
Submit a Nostr event.

```bash
curl -X POST http://localhost:8080/api/quotes \
  -H "Content-Type: application/json" \
  -d '{"id":"...","pubkey":"...","created_at":123,"kind":1,"tags":[],"content":"...","sig":"..."}'
```

Response: `{"ok": true, "message": "Quote accepted", "id": "..."}`

### GET /api/quotes/:id
Retrieve event by ID.

```bash
curl http://localhost:8080/api/quotes/abc123...
```

### GET /health
Health check: `{"status": "ok", "service": "strfry-http"}`

## Architecture

```
HTTP (CRE) ──┐
             ├──> Ingester ──> Validation ──> Database
WebSocket ───┘
```

Both protocols share the same validation, whitelist checking, and storage pipeline. See the main [strfry documentation](https://github.com/hoytech/strfry) for details on the core architecture.

## What's Different?

- ✅ Added HTTP REST API (ports 8080 + 7777)
- ✅ Same security/validation as WebSocket
- ✅ Zero changes to core strfry functionality
- ✅ Hot-reload config support for HTTP settings

## Config Options

| Option | Default | Description |
|--------|---------|-------------|
| `relay.http.enabled` | `false` | Enable HTTP API |
| `relay.http.port` | `8080` | HTTP port |
| `relay.http.bind` | `"127.0.0.1"` | Bind address |
| `relay.http.cors` | `true` | Enable CORS |

## See Also

- [strfry documentation](https://github.com/hoytech/strfry) - Core relay features
- [Plugin system](https://github.com/hoytech/strfry/blob/master/docs/plugins.md) - Write policies
- [Negentropy sync](https://github.com/hoytech/strfry/blob/master/docs/negentropy.md) - Set reconciliation

## License

GPLv3 - Same as [strfry](https://github.com/hoytech/strfry)
