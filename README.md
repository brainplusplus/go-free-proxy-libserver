<div align="center">

# 🔐 FreeProxy

**A high-performance Go library and REST API server for fetching and validating free proxies**

[![Go Reference](https://pkg.go.dev/badge/github.com/brainplusplus/go-free-proxy-libserver.svg)](https://pkg.go.dev/github.com/brainplusplus/go-free-proxy-libserver)
[![Go Report Card](https://goreportcard.com/badge/github.com/brainplusplus/go-free-proxy-libserver)](https://goreportcard.com/report/github.com/brainplusplus/go-free-proxy-libserver)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

[Features](#-features) • [Installation](#-installation) • [Usage](#-usage) • [API Reference](#-api-reference) • [Docker](#-docker) • [Configuration](#-configuration)

</div>

---

## ✨ Features

- 🚀 **High Performance** — In-memory proxy pool with O(1) swap-remove
- 🔒 **Thread-Safe** — Mutex-protected pool with `singleflight` to prevent duplicate concurrent scrapes
- ✅ **Validation** — HTTP and WebSocket proxy validation with configurable timeout
- 🔄 **Auto-Refresh** — Pool automatically refreshes when empty or expired (configurable TTL)
- 📊 **Auto Swagger** — OpenAPI 2.0 spec generated via reflection — add routes, Swagger updates automatically
- 🐳 **Docker Ready** — Multi-stage Dockerfile with health checks
- 🔑 **API Key Auth** — Optional authentication via header or query param

---

## 📦 Installation

### As a Library

```bash
go get github.com/brainplusplus/go-free-proxy-libserver
```

### As a Server

```bash
git clone https://github.com/brainplusplus/go-free-proxy-libserver.git
cd go-free-proxy-libserver

# Run directly (development)
go run ./server

# Or build binary (production)
go build -o freeproxy-server ./server
```

---

## 🚀 Usage

### Library Usage

```go
package main

import (
    "fmt"
    "time"

    freeproxy "github.com/brainplusplus/go-free-proxy-libserver"
)

func main() {
    // Optional: Configure TTL (default: 30 minutes)
    freeproxy.SetTTL(15 * time.Minute)

    // Get a single validated proxy
    proxy, err := freeproxy.GetProxy(freeproxy.FreeProxyParameter{
        CategoryCode: "US",  // Optional: EN, UK, US, SSL
        TargetUrl:    "hhttp://httpbin.org/get",  // Optional: default is http://httpbin.org/get
    })
    if err != nil {
        panic(err)
    }
    fmt.Printf("Working proxy: %s\n", proxy.ProxyUrl)

    // Get list of all cached proxies (not validated)
    list, err := freeproxy.GetProxyList(freeproxy.FreeProxyParameter{
        CategoryCode: "SSL",
    })
    if err != nil {
        panic(err)
    }
    fmt.Printf("Found %d proxies\n", len(list))
}
```

### Server Usage

```bash
# Run directly with go run (development)
go run ./server

# Or run built binary (production)
./freeproxy-server

# Or with Docker
docker compose up -d
```

#### With Custom Port/API Key

```bash
# Set custom port
PORT=3000 go run ./server

# With API key
API_KEY=mysecretkey go run ./server

# With custom TTL (seconds)
TIME_EXPIRED=3600 go run ./server

# Or use .env file
cp .env.example .env
# Edit .env, then:
go run ./server
```

---

## 📖 API Reference

| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/api/v1/proxy/get` | GET | ✅ | Returns a single validated working proxy |
| `/api/v1/proxy/list` | GET | ✅ | Returns all cached proxies (unvalidated) |
| `/swagger.json` | GET | ❌ | OpenAPI 2.0 specification |
| `/swagger` | GET | ❌ | Swagger UI (CDN-based) |

### Query Parameters

| Parameter | Type | Description |
|-----------|------|-------------|
| `category_code` | string | Filter by category: `EN`, `UK`, `US`, `SSL` (omit for all) |
| `target_url` | string | Target URL to validate against (supports `http`, `https`, `ws`, `wss`) |

### Example Requests

```bash
# Get a validated US proxy
curl -H "X-API-Key: your-key" "http://localhost:8080/api/v1/proxy/get?category_code=US"

# List all SSL proxies
curl -H "X-API-Key: your-key" "http://localhost:8080/api/v1/proxy/list?category_code=SSL"

# Get proxy validated against specific target
curl -H "X-API-Key: your-key" "http://localhost:8080/api/v1/proxy/get?target_url=https://api.myapp.com"
```

### Response Format

**GET /api/v1/proxy/get**
```json
{
  "data": {
    "scheme": "http",
    "ip": "192.168.1.1",
    "port": 8080,
    "proxy_url": "http://192.168.1.1:8080",
    "category_code": "US",
    "country_code": "US",
    "country_name": "United States",
    "anonym": true,
    "elite": false,
    "google": false,
    "https": true,
    "last_checked": "2026-03-03T22:00:00Z"
  }
}
```

**GET /api/v1/proxy/list**
```json
{
  "data": [...],
  "total": 42
}
```

---

## 🐳 Docker

### Quick Start

```bash
# Copy and configure environment
cp .env.example .env

# Build and run
docker compose up -d

# Check logs
docker compose logs -f

# Stop
docker compose down
```

### Manual Build

```bash
docker build -t freeproxy:latest .
docker run -d -p 8080:8080 --env-file .env freeproxy:latest
```

### Health Check

```bash
curl http://localhost:8080/swagger.json
```

---

## ⚙️ Configuration

Environment variables (`.env` file):

| Variable | Default | Description |
|----------|---------|-------------|
| `API_KEY` | *(empty)* | API key for authentication. Empty = no auth |
| `TIME_EXPIRED` | `1800` | Pool TTL in seconds (default: 30 min) |
| `PORT` | `15000` | Server port |
| `PROXY_TIMEOUT` | `3` | Proxy validation timeout in seconds (per request) |

---

## 📊 Proxy Categories

| Code | Source | Description |
|------|--------|-------------|
| `EN` | free-proxy-list.net | All countries |
| `UK` | uk-proxy.html | United Kingdom |
| `US` | us-proxy.html | United States |
| `SSL` | ssl-proxy.html | HTTPS/SSL proxies |

---

## 🔧 Adding New API Endpoints

The Swagger spec is auto-generated. Just register your route:

```go
// 1. Add handler in server/handler.go
type StatsResponse struct {
    PoolSize int    `json:"pool_size"`
    TTL      string `json:"ttl"`
}

func getStatsHandler(c *fiber.Ctx) error { ... }

// 2. Register in server/main.go — Swagger updates automatically
RegisterGET(api, "/proxy/stats",
    "Get pool statistics",
    getStatsHandler,
    nil,           // no query params
    StatsResponse{},
    true,          // require auth
)
```

No changes to `swagger.go` needed!

---

## 🏗️ Architecture

```
go-free-proxy-libserver/
├── proxy.go           ← Core structs, pool, GetProxy, GetProxyList
├── scraper.go         ← goquery-based scraper for free-proxy-list.net
├── validator.go       ← HTTP + WebSocket proxy validation
└── server/
    ├── main.go        ← Fiber server with graceful shutdown
    ├── handler.go     ← Route handlers
    ├── middleware.go  ← API key authentication
    ├── registry.go    ← Route metadata for auto-swagger
    └── swagger.go     ← OpenAPI 2.0 generator via reflection
```

### Key Design Decisions

| Decision | Rationale |
|----------|-----------|
| Pool key = `targetURL` | Different targets need different proxy validation |
| `singleflight` on load | Prevent N concurrent requests from triggering duplicate scrapes |
| Remove proxy after test | Prevents reuse of stale proxies |
| Max attempts = pool size | Prevents infinite loop when all proxies are dead |
| Registry-based Swagger | Zero maintenance, no codegen library |

---

## 🤝 Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

---

## 📄 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

---

<div align="center">

**⭐ If this project helps you, give it a star! ⭐**

Made with ❤️ by [brainplusplus](https://github.com/brainplusplus)

</div>
