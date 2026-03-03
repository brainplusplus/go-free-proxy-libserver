# FreeProxy Go Library + REST API Server — Design Doc

**Date:** 2026-03-03
**Status:** Approved

---

## Overview

Build a Go library and REST API server for fetching and validating free proxies scraped from free-proxy-list.net. The library is independently importable; the server is a thin Fiber wrapper around it.

---

## Project Structure

```
github.com/yourusername/freeproxy/
├── go.mod
├── .env.example
├── .gitignore
├── Dockerfile
├── docker-compose.yml
├── proxy.go           ← package freeproxy: structs, pool, GetProxy, GetProxyList
├── scraper.go         ← scrape() per category via goquery
├── validator.go       ← validateHTTP + validateWebSocket
└── server/
    ├── main.go        ← Fiber setup, RegisterGET calls, env config
    ├── handler.go     ← handler funcs + query/response structs
    ├── middleware.go  ← authMiddleware (X-API-Key)
    ├── registry.go    ← RouteMeta, routeRegistry, RegisterGET wrapper
    └── swagger.go     ← auto-build OpenAPI spec + Swagger UI handler
```

---

## Dependencies

```
github.com/PuerkitoBio/goquery  v1.9.1
github.com/gofiber/fiber/v2     v2.52.4
github.com/gorilla/websocket    v1.5.1
github.com/joho/godotenv        v1.5.1
golang.org/x/sync               v0.7.0  ← singleflight
```

---

## Core Data Structures

```go
// Library package
type FreeProxy struct {
    Scheme, IP, Username, Password, CategoryCode, CountryCode, CountryName string
    Port                                                                    int
    Anonym, Elite, Google, HTTPS                                            bool
    LastChecked                                                             time.Time
}

type FreeProxyParameter struct {
    CategoryCode string  // EN | UK | US | SSL — optional, empty = all
    TargetUrl    string  // default: http://httpbin.org/get
}
```

---

## Public API (Library)

| Function | Behavior |
|---|---|
| `GetProxy(FreeProxyParameter)` | Returns single validated working proxy; removes each tested proxy from pool |
| `GetProxyList(FreeProxyParameter)` | Returns snapshot of current pool (unvalidated) |
| `SetTTL(time.Duration)` | Override default 30-minute pool TTL |

---

## Proxy Pool Architecture

- **Key**: `targetURL` (default `http://httpbin.org/get`)
- **Storage**: `map[string][]FreeProxy` with `map[string]time.Time` expiry
- **Mutex**: `sync.Mutex` on all read/write operations
- **Singleflight**: `golang.org/x/sync/singleflight` — prevents duplicate concurrent crawls
- **Expiry**: Pool refreshed when empty OR TTL expired (default 30 min)
- **Removal**: Swap-remove O(1) — proxy removed from pool regardless of validation result
- **Max attempts**: Capped at snapshot size at start of `GetProxy` call (prevents infinite loop)

### Data Flow

```
GetProxy(param)
  → ensureLoaded(targetURL)
      → singleflight.Do → scrape(EN) + scrape(UK) + scrape(US) + scrape(SSL)
  → loop (max = pool size):
      → pickRandom(key, categoryCode) — removes proxy
      → validateProxy(proxy, targetURL)
      → return on first success
  → error if pool exhausted
```

---

## Scraper

- Scrapes: `https://free-proxy-list.net/en/`, uk-proxy, us-proxy, ssl-proxy
- Parser: `goquery` on `#list tr` (skip row 0 = header)
- Headers: `User-Agent`, `Cache-Control: no-cache`, `Pragma: no-cache`
- Fields parsed: IP, Port, CountryCode, CountryName, Anonymity, Google, HTTPS, LastChecked
- `parseLastChecked`: converts "N minutes ago" → `time.Time`

---

## Validator

| Target scheme | Method |
|---|---|
| `http://`, `https://` | `validateHTTP` |
| `ws://`, `wss://` | `validateWebSocket` |

**HTTP validation:**
- `DisableKeepAlives: true`
- No redirect follow
- `Cache-Control: no-cache` header
- Accept status `200–399` only

**WebSocket validation:**
- `gorilla/websocket` dialer with proxy URL
- `HandshakeTimeout: 10s`

---

## REST API (Fiber Server)

| Endpoint | Auth | Description |
|---|---|---|
| `GET /api/v1/proxy/get` | Yes | Returns single validated proxy |
| `GET /api/v1/proxy/list` | Yes | Returns pool snapshot |
| `GET /swagger.json` | No | OpenAPI 2.0 spec (auto-generated) |
| `GET /swagger` | No | Swagger UI (CDN) |

Query params for both proxy endpoints:
- `category_code` — optional, enum: EN/UK/US/SSL
- `target_url` — optional, default: `http://httpbin.org/get`

---

## Auto Swagger Architecture

**No codegen library.** Spec built at runtime from route registry via reflection.

```go
type RouteMeta struct {
    Method         string
    Path           string
    Summary        string
    QueryStruct    interface{}  // reflect → query params
    ResponseStruct interface{}  // reflect → response schema
    RequireAuth    bool
}
```

`RegisterGET(group, path, summary, handler, queryStruct, responseStruct, requireAuth)`:
- Registers route in Fiber
- Appends to `routeRegistry`

`swaggerSpec()` builds OpenAPI 2.0 map dynamically:
- Loops `routeRegistry`
- Reflects `QueryStruct` fields (json tags) → query parameters
- Reflects `ResponseStruct` fields (json tags) → response schema
- If `RequireAuth=true`: injects `security: [{ApiKeyAuth: []}]` per route
- If any route has auth: adds `securityDefinitions.ApiKeyAuth` (apiKey in header `X-API-Key`)

Adding a new API = call `RegisterGET`. Swagger updates automatically.

---

## Auth Middleware

- Header: `X-API-Key`
- Fallback: query param `?api_key=`
- If `API_KEY` env is empty → all requests allowed (no auth)
- Returns `401 Unauthorized` on invalid key

---

## Environment Config

```env
API_KEY=          # empty = no auth
TIME_EXPIRED=1800 # seconds, default 30 min
PORT=8080
```

---

## Docker

**Dockerfile** — multi-stage:
1. `golang:1.21-alpine` → build binary
2. `alpine:latest` → copy binary, expose 8080

**docker-compose.yml:**
- `env_file: .env`
- Port `8080:8080`
- Restart `unless-stopped`
- Healthcheck: `GET /swagger.json`

---

## Design Decisions Summary

| Decision | Rationale |
|---|---|
| Pool key = targetURL | Different targets may need different proxy validation |
| singleflight on load | Prevent N concurrent requests each triggering a full scrape |
| Remove proxy on failure AND success | Prevents reuse of stale/used proxies |
| Max attempt = pool snapshot size | Prevents infinite loop when all proxies are dead |
| HTTP status 200-399 only | 4xx means proxy is working but target rejected — still consider valid for connectivity test |
| Registry-based swagger | Zero-maintenance, no codegen, auth-aware per route |
| Swagger UI via CDN | No binary assets, minimal image size |
