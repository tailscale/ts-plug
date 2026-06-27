# Documentation Index

Complete documentation for ts-plug, ts-unplug, and ts-unplug-proxy.

## Core Guides

### [ts-plug Guide](./ts-plug.md)
Complete guide for exposing local services to your tailnet.

**Topics covered:**
- Basic usage and configuration
- HTTP, HTTPS, and DNS proxying
- Public access with Tailscale Funnel
- Security features and user identity headers
- Advanced patterns and troubleshooting

**Quick start:**
```sh
ts-plug -hostname myapp -- python -m http.server 8080
```

---

### [ts-unplug Guide](./ts-unplug.md)
Complete guide for accessing remote Tailscale services locally.

**Topics covered:**
- Basic usage and configuration
- Development workflows with remote services
- Port mapping and proxy setup
- Security considerations
- Advanced patterns and troubleshooting

**Quick start:**
```sh
ts-unplug -dir ./state -port 8080 myserver.tailnet.ts.net
```

---

### [ts-unplug-proxy Guide](./ts-unplug-proxy.md)
Complete guide for running local SOCKS5 and HTTP proxies into your tailnet.

**Topics covered:**
- SOCKS5 and HTTP client setup
- Tailnet DNS through proxy-aware clients
- Shared and separate listener ports
- Security considerations and troubleshooting

**Quick start:**
```sh
ts-unplug-proxy -dir ./state -socks5 localhost:1080 -http localhost:8080
curl --socks5-hostname localhost:1080 http://myserver.tailnet.ts.net
curl -x http://localhost:8080 http://myserver.tailnet.ts.net
```

---

### [Use Cases](./use-cases.md)
Real-world scenarios and patterns for using ts-plug and ts-unplug.

**Scenarios covered:**
- Development workflows (full-stack, microservices, mobile)
- Testing scenarios (webhooks, E2E, load testing)
- Deployment patterns (containers, homelab, demos)
- Team collaboration (code review, pair programming)
- Hybrid cloud architectures

**Examples:**
- Developing locally with remote databases
- Microservices development
- Webhook testing
- Sidecar-free container deployment

---

### [Docker Integration](./docker.md)
Using ts-plug to eliminate Tailscale sidecar containers.

**Topics covered:**
- Building Docker images with ts-plug
- Real-world examples (Pi-hole, web apps, APIs)
- Docker Compose patterns
- Kubernetes integration
- Best practices and troubleshooting

**Quick example:**
```dockerfile
COPY ts-plug /usr/local/bin/
ENTRYPOINT ["ts-plug", "-hostname", "myapp", "--"]
CMD ["npm", "start"]
```

---

## Quick Reference

### When to Use Which Tool

| Scenario | Tool | Command |
|----------|------|---------|
| Share local dev server | ts-plug | `ts-plug -hostname dev -- npm start` |
| Access remote database | ts-unplug | `ts-unplug -dir ./state -port 5432 db.ts.net:5432` |
| Route many tailnet services through one local proxy | ts-unplug-proxy | `ts-unplug-proxy -dir ./state -socks5 localhost:1080` |
| Test webhooks | ts-plug | `ts-plug -public -hostname webhook -- ./server` |
| Test against staging | ts-unplug | `ts-unplug -dir ./state -port 8080 api-staging.ts.net` |
| Deploy in container | ts-plug | Use as Docker ENTRYPOINT |
| Multi-cloud access | ts-unplug | Multiple instances for each service |
| Route through remote proxy | ts-unplug | `ts-unplug -dir ./state -port 8888 proxy.ts.net:3128` |

### Common Flags

| Flag | ts-plug | ts-unplug | ts-unplug-proxy |
|------|---------|-----------|--------------------|
| Hostname | `-hostname myapp` | `-hostname myproxy` | N/A |
| State dir | `-dir .data` (default) | `-dir ./state` (required) | `-dir ./state` (required) |
| Port | `-https-port 443:8080` | `-port 8080` | `-socks5 localhost:1080`, `-http localhost:8080` |
| Protocol | `-http`, `-https`, `-dns` | HTTP only | SOCKS5 and HTTP proxy |
| Public | `-public` | N/A | N/A |
| Debug | `-log debug` | `-debug-tsnet` | `-v`, `-vv` |

## Getting Started

```sh
make                    # Build all binaries
make install            # Install to $GOPATH/bin
```

Try ts-plug:
```sh
./build/ts-plug -hostname test -- python -m http.server 8080
```

Try ts-unplug:
```sh
./build/ts-unplug -dir ./state -port 8080 someservice.yournet.ts.net
```

Try ts-unplug-proxy:
```sh
./build/ts-unplug-proxy -dir ./state -socks5 localhost:1080 -http localhost:8080
curl --socks5-hostname localhost:1080 http://someservice.yournet.ts.net
curl -x http://localhost:8080 http://someservice.yournet.ts.net
```

## Navigation

### By Task

- **Share dev work** → [ts-plug Guide](./ts-plug.md) → [Team Collaboration](./use-cases.md#team-collaboration)
- **Access remote DB** → [ts-unplug Guide](./ts-unplug.md) → [Development Workflows](./use-cases.md#development-workflows)
- **Test webhooks** → [ts-plug Guide](./ts-plug.md#public-access) → [Testing Scenarios](./use-cases.md#testing-scenarios)
- **Deploy containers** → [Docker Guide](./docker.md) → [Deployment Patterns](./use-cases.md#deployment-patterns)
- **Multiple services** → [Use Cases](./use-cases.md#microservices-development)

### By Role

**Developers:**
- Start with [ts-plug Guide](./ts-plug.md)
- Read [Development Workflows](./use-cases.md#development-workflows)
- See [Team Collaboration](./use-cases.md#team-collaboration)

**DevOps/SRE:**
- Start with [Docker Guide](./docker.md)
- Read [Deployment Patterns](./use-cases.md#deployment-patterns)
- See [Hybrid Cloud](./use-cases.md#hybrid-cloud-architectures)

**QA/Testing:**
- Start with [ts-unplug Guide](./ts-unplug.md)
- Read [Testing Scenarios](./use-cases.md#testing-scenarios)
- See [Webhook Testing](./use-cases.md#webhook-testing)

## Additional Resources

- [Main README](../README.md) - Project overview and quick start
- [Examples](../cmd/examples/) - Sample servers in multiple languages
- [Docker Examples](../docker/) - Real container deployments
- [ts-plug Source](../cmd/ts-multi-plug/) - Implementation details
- [ts-unplug Source](../cmd/ts-unplug/) - Implementation details
- [ts-unplug-proxy Source](../cmd/ts-unplug-proxy/) - Implementation details

## Contributing

Found an issue or have a suggestion? Please check the [main README](../README.md) for contribution guidelines.

## License

BSD-3-Clause - See [LICENSE](../LICENSE)
