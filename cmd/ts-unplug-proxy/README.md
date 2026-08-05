# ts-unplug-proxy

Run local SOCKS5 and/or HTTP proxies that reach destinations through a `tsnet.Server`.

!!! WARNING !!!

This is a proof of concept for a socks5 and HTTP proxy using tsnet as a
standalone go binary. This could be useful in environments where installing
tailscaled is not possible.

## Build

```sh
cd ../..
make ts-unplug-proxy
```

## Usage

```sh
ts-unplug-proxy -dir ./state -socks5 localhost:1080
curl --socks5-hostname localhost:1080 http://myserver.tailnet-name.ts.net
```

`socks5h://` and `--socks5-hostname` send the hostname to the proxy. Tailnet hostnames are resolved from Tailscale status/DNS instead of local DNS; public names fall back to normal tsnet egress.

```sh
ts-unplug-proxy -dir ./state -http localhost:8080
curl -x http://localhost:8080 http://myserver.tailnet-name.ts.net
```

Proxy clients provide the remote destination, so this command does not take a positional remote address.

Use `-v` to log proxy connections. Use `-vv` to include `tsnet.Server` debug logs.

Use `-accept-routes` when reaching subnet routes or Tailscale Services advertised by other nodes. On macOS, use `-disable-interface-binding` when another network tunnel controls the default interface.

## Documentation

See **[docs/ts-unplug-proxy.md](../../docs/ts-unplug-proxy.md)** for complete documentation.
