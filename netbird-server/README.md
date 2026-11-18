# NetBird Self-Hosted Server Stack

This project deploys a self-hosted NetBird management + signal + relay stack, designed to sit behind a CDN or reverse proxy for domain fronting.

## Deployment

### CDN-fronted

```bash
NETBIRD_DOMAIN=your-cdn-hostname.example.com \
NETBIRD_SETUP_KEY=your-setup-key \
docker compose up -d
```

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `NETBIRD_DOMAIN` | Public hostname (CDN/reverse proxy) | **required** |
| `NETBIRD_SETUP_KEY` | Pre-configured setup key for implant enrollment | (auto-generated) |
| `NETBIRD_RELAY_SECRET` | Shared secret for relay authentication | `changeme` |

## Architecture

```
CDN / Reverse Proxy (:443)
    │
    ├── /api/*          → Management API (:33073)
    ├── /signalexchange  → Signal (gRPC-web or WS) (:10000)
    ├── /relay           → Relay (WS) (:33080)
    └── STUN             → (:3478/udp, direct to origin)
```

## NetBird Path Routing Rules

NetBird clients communicate with three server components. When fronted by a CDN, the following paths at the reverse proxy should be routed to the NetBird server:

### Management API
- `/api/*` → port 33073
- `/management.ManagementService/*` (gRPC-web) → port 33073

### Signal Server  
- `/signalexchange.SignalExchange/*` (gRPC-web) → port 10000

### Relay Server
- `/relay` (WebSocket) → port 33080

### STUN
- UDP port 3478 must be directly reachable (not through CDN HTTP)

## Reverse Proxy Examples

### Caddy

```
{
    log {
        output file /var/log/caddy/error.log
        level INFO
    }
}

your-origin-hostname.example.com {

    # Management API
    @management path /api/* /management.ManagementService/*
    handle @management {
        reverse_proxy http://localhost:33073
    }

    # Signal server (gRPC-web / WebSocket)
    @signal path /signalexchange.SignalExchange/*
    handle @signal {
        reverse_proxy h2c://localhost:10000
    }

    # Relay (WebSocket)
    @relay path /relay
    handle @relay {
        reverse_proxy http://localhost:33080 {
            transport http {
                read_timeout 0
                write_timeout 0
            }
        }
    }

    # Fallback
    handle {
        respond "OK" 200
    }
}
```

### Apache

```apache
########### BEGIN NETBIRD ###########

    # Management API
    ProxyPass /api/ http://localhost:33073/api/
    ProxyPassReverse /api/ http://localhost:33073/api/

    # gRPC-web management  
    RewriteCond %{REQUEST_URI} ^/management\.ManagementService/
    RewriteRule ^(.*) http://localhost:33073$1 [P,L]

    # Signal (gRPC-web)
    RewriteCond %{REQUEST_URI} ^/signalexchange\.SignalExchange/
    RewriteRule ^(.*) http://localhost:10000$1 [P,L]

    # Relay WebSocket
    RewriteCond %{REQUEST_URI} ^/relay
    RewriteCond %{HTTP:Upgrade} websocket [NC]
    RewriteCond %{HTTP:Connection} upgrade [NC]
    RewriteRule ^(.*) ws://localhost:33080$1 [P,L]

############ END NETBIRD ############
```

## Setup Key

The compose stack pre-creates a setup key via the management API init container. Use this key in the BOFbird daemon:

```
netbirdd --setup-key <KEY> --management-url https://your-cdn-hostname.example.com
```

To generate additional setup keys, use the NetBird management API or CLI:

```bash
docker exec netbird-management /go/bin/netbird-mgmt setup-keys create \
  --name "implant" --type reusable --expires-in 8760h
```
