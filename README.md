> [!WARNING]
> Lots of Work in Progress stuff here!

# What's in this repo?

One-liner tools to expose things to/from your tailnet!

| Binary | Purpose | Use Case |
|--------|---------|----------|
| **ts-plug** | Expose localhost to your tailnet | Share your dev server to your tailnet, deploy without sidecars |
| **ts-unplug** | Bring tailnet services to localhost | Access tailnet-based databases/APIs as if they were local |
| **ts-unplug-proxy** | SOCKS5/HTTP proxy into your tailnet | Route proxy-aware clients to many tailnet services |

## Quick Start

**Build:**
```sh
make                    # Build all binaries
make install            # Install to $GOPATH/bin
```

**ts-plug** - Share a local service:
```sh
./build/ts-plug -hostname myapp -- python -m http.server 8080
# Access at https://myapp.tailnet-name.ts.net
```

**ts-unplug** - Access a remote service:
```sh
./build/ts-unplug -dir ./state -port 8080 api.tailnet-name.ts.net
# Access at http://localhost:8080
```

**ts-unplug-proxy** - Route proxy-aware clients into your tailnet:
```sh
./build/ts-unplug-proxy -dir ./state -socks5 localhost:1080 -http localhost:8080
curl --socks5-hostname localhost:1080 http://api.tailnet-name.ts.net
curl -x http://localhost:8080 http://api.tailnet-name.ts.net
```

## Key Features

**ts-plug** automatically:
- Starts your upstream server
- Joins your tailnet with TLS and DNS
- Reverse proxies to localhost:8080
- Optional public access with `-public`
- Supports HTTP, HTTPS, and DNS protocols

**ts-unplug** provides:
- Reverse proxy from tailnet to localhost
- Access to services requiring localhost URLs
- Simple port mapping

**ts-unplug-proxy** provides:
- Local SOCKS5 and HTTP proxies into the tailnet
- One proxy for multiple tailnet destinations
- Optional shared listener port for SOCKS5 and HTTP

## Examples

Run servers in any language:
```sh
make examples

# Try different languages with ts-plug
./build/ts-plug -hn hello -- ./build/hello        # Go
./build/ts-plug -hn hello -- cmd/examples/hello/hello.js   # Node
./build/ts-plug -hn hello -- cmd/examples/hello/hello.py   # Python
```

See [cmd/examples/](./cmd/examples/) for more.

## Docker Integration

Use ts-plug as an entrypoint to eliminate sidecar containers:

```dockerfile
COPY ts-plug /usr/local/bin/
ENTRYPOINT ["ts-plug", "-hostname", "myapp", "--"]
CMD ["npm", "start"]
```

See [docker/](./docker/) for Pi-hole, Open WebUI, and Audiobookshelf examples.

## Documentation

- **[Complete Documentation](./docs/)** - Guides, use cases, and detailed examples
- **[ts-plug Guide](./docs/ts-plug.md)** - Full ts-plug documentation
- **[ts-unplug Guide](./docs/ts-unplug.md)** - Full ts-unplug documentation
- **[ts-unplug-proxy Guide](./docs/ts-unplug-proxy.md)** - SOCKS5/HTTP proxy into your tailnet
- **[Use Cases](./docs/use-cases.md)** - Real-world scenarios
- **[Docker Guide](./docs/docker.md)** - Container integration

**Quick help:**
```sh
./build/ts-plug -h
./build/ts-unplug -h
./build/ts-unplug-proxy -h
```

## License

BSD-3-Clause - See [LICENSE](./LICENSE)
