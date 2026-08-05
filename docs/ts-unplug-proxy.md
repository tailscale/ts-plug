# ts-unplug-proxy Guide

**ts-unplug-proxy** runs local SOCKS5 and/or HTTP proxies that dial requested destinations through a `tsnet.Server`.

## Overview

ts-unplug-proxy allows you to:
- Use one local proxy process for many tailnet services
- Let proxy-aware tools choose the remote hostname and port
- Resolve tailnet hostnames through Tailscale status/DNS instead of local DNS
- Avoid running one fixed local port mapping per remote service

This differs from `ts-unplug`, which maps one local TCP port to one fixed remote target.

## Installation

Build from source:
```sh
make ts-unplug-proxy
```

Install to `$GOPATH/bin`:
```sh
make install
```

## Basic Usage

```sh
ts-unplug-proxy -dir <state-dir> (-socks5 <listen:port> | -http <listen:port>) [options]
```

Start a SOCKS5 proxy:
```sh
ts-unplug-proxy -dir ./state -socks5 localhost:1080
curl --socks5-hostname localhost:1080 http://myserver.tailnet.ts.net
```

For SOCKS5, use `socks5h://` or `--socks5-hostname` so the client sends the hostname to the proxy. Tailnet names are resolved from Tailscale status/DNS; public names fall back to normal tsnet egress.

Start an HTTP proxy:
```sh
ts-unplug-proxy -dir ./state -http localhost:8080
curl -x http://localhost:8080 http://myserver.tailnet.ts.net
```

Start both on different ports:
```sh
ts-unplug-proxy -dir ./state -socks5 localhost:1080 -http localhost:8080
```

Start both on the same port:
```sh
ts-unplug-proxy -dir ./state -socks5 localhost:1080 -http localhost:1080
```

## Configuration Flags

### Required

- `-dir` - Directory for tsnet server state
  - Stores Tailscale authentication and connection state
  - Example: `-dir ./state`, `-dir /var/lib/tsunplug-proxy`

- At least one listener flag:
  - `-socks5 <listen:port>` - Enable a SOCKS5 proxy listener
  - `-http <listen:port>` - Enable an HTTP proxy listener

### Optional

- `-v` - Log proxy connections and proxy dials
  ```sh
  ts-unplug-proxy -dir ./state -v -socks5 localhost:1080
  ```

- `-vv` - Log proxy connections, proxy dials, and `tsnet.Server` debug info
  ```sh
  ts-unplug-proxy -dir ./state -vv -socks5 localhost:1080
  ```

- `-accept-routes` - Accept subnet and Tailscale Service routes advertised by other nodes

- `-disable-interface-binding` - On macOS, disable Tailscale's network namespace integration, including physical-interface binding. Use this when another network tunnel controls the system's interface and route selection.

## Examples

### SOCKS5 With curl

Use SOCKS5 hostname mode so the tailnet DNS name is sent to the proxy:
```sh
ts-unplug-proxy -dir ./state -socks5 localhost:1080
curl --socks5-hostname localhost:1080 http://api-staging.tailnet.ts.net
```

For proxy URLs, use `socks5h://` rather than `socks5://` when the client supports it:
```sh
ALL_PROXY=socks5h://localhost:1080 curl http://api-staging.tailnet.ts.net
```

### HTTP Proxy With curl

```sh
ts-unplug-proxy -dir ./state -http localhost:8080
curl -x http://localhost:8080 http://api-staging.tailnet.ts.net
```

HTTPS through the HTTP proxy uses CONNECT:
```sh
curl -x http://localhost:8080 https://api-staging.tailnet.ts.net
```

### Browser Proxy

Start one or both proxy listeners:
```sh
ts-unplug-proxy -dir ./state -socks5 localhost:1080 -http localhost:8080
```

Configure your browser to use either SOCKS host `localhost:1080` or HTTP proxy `localhost:8080`.

## How It Works

```
┌─────────────────────────────────────────────────────────┐
│  Your Local Machine                                     │
│                                                         │
│  ┌──────────────┐   SOCKS5/HTTP    ┌────────────────┐   │
│  │ Your App     │ ───────────────> │ ts-unplug-     │   │
│  │              │                  │ proxy          │   │
│  └──────────────┘                  └───────┬────────┘   │
│                                            │            │
└────────────────────────────────────────────┼────────────┘
                                             │ Tailscale
                                             │
┌────────────────────────────────────────────┼────────────┐
│  Tailnet                                   │            │
│                                    ┌───────▼────────┐   │
│                                    │ Remote Service │   │
│                                    └────────────────┘   │
└─────────────────────────────────────────────────────────┘
```

ts-unplug-proxy:
1. Connects a `tsnet.Server` to your tailnet
2. Listens locally for SOCKS5 and/or HTTP proxy connections
3. Receives each requested destination from the proxy client
4. Resolves destinations through Tailscale DNS via `LocalClient.QueryDNS`
5. Falls back to MagicDNS peer names from Tailscale status, including short names like `sippy`
6. Falls back to normal `tsnet.Server.Dial` for non-tailnet public names such as `www.google.ca`
7. Dials resolved IPs with `tsnet.Server.Dial`
8. Relays traffic between the client and the destination

The proxy does not use the local OS resolver for tailnet hostnames received from SOCKS5 or HTTP proxy clients. Public names may use the normal resolver path inside `tsnet.Server.Dial`, which is necessary for general internet egress.

## Security Considerations

### Local Binding

Use a loopback listen address such as `localhost:1080` or `127.0.0.1:1080` unless you intentionally want other machines to use the proxy.

### Authentication

This command does not implement proxy authentication. Do not bind it to a shared interface unless the network path is already trusted and access-controlled.

### State Directory

The `-dir` contains sensitive Tailscale credentials. Protect it appropriately:
```sh
chmod 700 ./state
```

## Troubleshooting

### Missing Listener Error

At least one proxy listener is required:
```sh
ts-unplug-proxy -dir ./state -socks5 localhost:1080
ts-unplug-proxy -dir ./state -http localhost:8080
```

### Tailnet DNS Does Not Resolve With SOCKS5

Use SOCKS5 hostname mode:
```sh
curl --socks5-hostname localhost:1080 http://myserver.tailnet.ts.net
```

For proxy URLs, use `socks5h://` rather than `socks5://` when the client supports it:
```sh
ALL_PROXY=socks5h://127.0.0.1:1080 curl http://myserver.tailnet.ts.net
```

### Port Already in Use

```sh
lsof -i :1080
ts-unplug-proxy -dir ./state -socks5 localhost:1081
```

### Can't Reach Remote Service

1. Verify the remote hostname and port are correct
2. Check the remote service is running
3. Ensure your Tailscale identity has access to the destination
4. Try `tailscale ping myserver.tailnet.ts.net`

## See Also

- [ts-unplug Guide](./ts-unplug.md) - Fixed local port mapping to a tailnet service
- [ts-plug Guide](./ts-plug.md) - Expose local services to your tailnet
- [Main README](../README.md) - Quick start guide
