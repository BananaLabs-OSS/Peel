# Peel

UDP relay proxy with dynamic routing.

From [BananaLabs OSS](https://github.com/bananalabs-oss).

## Overview

Peel is a transparent UDP relay that routes player traffic to game servers based on their IP address. Routes are updated dynamically via HTTP API, enabling seamless server transfers without client reconnection.

## Quick Start
```bash
go run ./cmd/server
```

- UDP relay: `:5520`
- HTTP API: `:8080`

## How It Works
```
Player (192.168.1.50) → Peel (:5520) → Backend (game server)
                            ↑
                      Route table
                  192.168.1.50 → 10.99.0.10:5520
```

Players connect to Peel's public address. Peel looks up their IP in the route table and forwards traffic to the appropriate backend.

## API Reference

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/routes` | List all routes |
| `POST` | `/routes` | Set route |
| `DELETE` | `/routes/:player_ip` | Remove route |
| `DELETE` | `/sessions/:player_ip` | Close session only (keep route) |

## Sessions

When a route is updated for an existing player, the session's backend is hot-swapped in-place without closing the UDP socket. Use `DELETE /sessions/:player_ip` to explicitly close a session after sending a refer packet.

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