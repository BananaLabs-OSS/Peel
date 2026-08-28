# Peel

UDP relay proxy with dynamic routing.

From [BananaLabs OSS](https://github.com/bananalabs-oss).

## Overview

Peel is a transparent UDP relay that routes player traffic to game servers based on their public IP address. Routes are updated dynamically via HTTP API, enabling seamless server transfers without client reconnection. Live sessions are keyed by transport and complete source endpoint (`udp:IP:port`), so multiple players behind the same home NAT keep independent relay sockets and reply paths.

## Quick Start

```bash
go run ./pulp-deployment -app ./application/pulp.app.toml
```

## Composition boundary

Peel is defined by `application/peel.lua` and its public compatibility API. The compiled Pulp engines are reusable and do not know about players, Bananasplit, or game engines:

- `routed-udp-relay` moves opaque datagrams between source endpoints and route targets. Its canonical source is `pulp-engines/routed-udp-relay-host-cell`; Peel's endpoint-key and resolver policy remain in Lua.
- `routing-state` persists generic `key → target` routes, grouped sessions, and expiring negative-cache entries. Its canonical source is `pulp-engines/routing-state-sqlite-cell`.
- `http-json` performs generic JSON-over-HTTP requests. Its canonical source is `pulp-engines/http-json-cell`.
- `peel-api` preserves Peel's existing HTTP contract and dispatches commands to Lua.

Lua turns those engines into Peel: it derives a public-IP route key from a source endpoint, asks Bananasplit for a backend, applies retry suppression, translates the compatibility API into generic contracts, and coordinates route/session changes. No per-packet payload crosses Lua; only new-session and control-plane decisions do.

## Configuration

| Setting            | Location | Default |
| ------------------ | -------- | ------- |
| UDP listen address | `pulp-engines/routed-udp-relay-host-cell/pulp.cell.toml` → `listen_addr` | `:5520` |
| HTTP API address   | `api-cell/pulp.cell.toml` → `api_addr`, overridden by `HTTP_PORT` | `:8080` |
| Route resolver URL | `pulp-engines/routed-udp-relay-host-cell/pulp.cell.toml` → `route_resolver_url` | `http://localhost:3001/route-request` |
| Socket buffer size | `pulp-engines/routed-udp-relay-host-cell/pulp.cell.toml` → `buffer_size` | `8388608` (8 MiB) |
| Service token | `api-cell/pulp.cell.toml` → `service_token`, overridden by `SERVICE_TOKEN` | empty |

**Docker Compose:**

```yaml
peel:
  image: localhost/peel:local
  ports:
    - "5530:5520/udp"
    - "8080:8080"
```

## How It Works

```
Player (192.168.1.50) → Peel (:5520) → Backend (game server)
                            ↑
                      Route table
                  192.168.1.50 → 10.99.0.10:5520
```

Players connect to Peel's public address. Peel looks up their IP in the route table and forwards traffic to the appropriate backend.

## API Reference

| Method   | Endpoint               | Description                     |
| -------- | ---------------------- | ------------------------------- |
| `GET`    | `/health`              | Health check                    |
| `GET`    | `/routes`              | List all routes                 |
| `POST`   | `/routes`              | Set route                       |
| `DELETE` | `/routes/:player_ip`   | Remove route and close session  |
| `DELETE` | `/sessions/:player_ip` | Close session only (keep route) |

## Control-API auth (X-Service-Token)

The mutating control endpoints (`POST /routes`, `DELETE /routes/:ip`,
`DELETE /sessions/:ip`) support an optional `X-Service-Token` shared-secret
gate. **Auth is OFF unless `SERVICE_TOKEN` is set.**

- **`SERVICE_TOKEN` empty (default):** the cell starts and serves the
  control API without auth. The control port is internal-only-bounded — the
  cell publishes only the UDP listener — so it is reachable only from
  sibling cells on the Pulp host. This is the current behavior; no caller
  changes are required.
- **`SERVICE_TOKEN` set (non-empty):** the three mutating endpoints require
  a matching `X-Service-Token` header (constant-time compared); requests
  without it get `401`. The GET observability routes (`/routes`, `/health`)
  stay open.

To **enable** auth, do both in lockstep: set `SERVICE_TOKEN` here AND have
the callers (Bananasplit's `PeelClient`, Potassium's `relay.Client`) send
the same value as the `X-Service-Token` header. Setting only one side will
either break the control plane (token set in Peel but callers don't send
it) or do nothing (callers send a token Peel ignores).

## Sessions

Routing policy remains IP-based for compatibility with Bananasplit, while runtime session identity is endpoint-based. Players sharing a public IP therefore share the selected backend route but never share a live UDP flow. Route updates and `DELETE /sessions/:player_ip` close every active endpoint for that public IP; each player's next packet creates a fresh session against the updated route.

TCP is intentionally a separate transport rather than a UDP mode. Supporting it requires a Pulp stream capability with supervised accept/connect lifecycles, bounded buffering and backpressure, deadlines, and half-close behavior. The durable session key is transport-neutral so a future TCP cell can use keys such as `tcp:<endpoint>` without changing routing ownership.

**Set Route:**

```json
{
  "player_ip": "192.168.1.50",
  "backend": "10.99.0.10:5520"
}
```

**List Routes Response:**

```json
{
  "192.168.1.50": "10.99.0.10:5520",
  "192.168.1.51": "10.99.0.11:5520"
}
```

## Flow

1. Player connects to `relay.hycraft.net:5520`
2. Peel receives packet, extracts source IP
3. Looks up route: `192.168.1.50 → 10.99.0.10:5520`
4. Forwards packet to backend
5. Backend response forwarded back to player

**Server Transfer:**

1. Bananasplit updates route: `192.168.1.50 → 10.99.0.11:5520`
2. Next packet goes to new server
3. Player seamlessly transferred

## License

MIT

