# TLS Client Integration Design

**Date:** 2026-03-04
**Status:** Approved

## Overview

Integrate `bogdanfinn/tls-client` library to replace standard HTTP clients throughout the proxy scraper and validator. This provides better browser TLS fingerprinting to avoid bot detection and improve success rates when scraping proxy lists and validating proxies.

## Goals

- Replace all standard `net/http` clients with TLS client library
- Mimic Chrome 131 browser TLS fingerprints
- Maintain existing functionality and error handling
- Improve scraping and validation success rates

## Architecture

### Components Affected

1. **Scraper** (`scraper.go:29`)
   - Fetches proxy lists from free-proxy-list.net
   - Currently uses standard `http.Client` with 20s timeout

2. **HTTP Validator** (`validator.go:46-95`)
   - Tests proxies against HTTP/HTTPS target URLs
   - Currently uses `http.Transport` with proxy configuration

3. **WebSocket Validator** (`validator.go:97-142`)
   - Tests proxies against WebSocket targets
   - Will remain using `gorilla/websocket` (TLS client doesn't support WebSocket)

### Dependencies

Add to `go.mod`:
- `github.com/bogdanfinn/tls-client`
- `github.com/bogdanfinn/fhttp` (required by tls-client)

### Implementation Details

#### Scraper Changes

Replace standard HTTP client with TLS client:
- Profile: `Chrome_131` (mimics Chrome 131 browser)
- Timeout: 20 seconds
- Cookie jar: enabled
- Headers: maintain existing User-Agent and cache control headers
- Use `fhttp` types instead of `net/http` types

#### HTTP Validator Changes

Replace HTTP transport and client:
- Profile: `Chrome_131`
- Timeout: runtime-configurable via `PROXY_TIMEOUT` env variable
- Cookie jar: enabled
- Redirects: disabled (no-follow) to match current behavior
- Proxy configuration: through TLS client options
- Context cancellation: maintain existing behavior for concurrent validation

#### WebSocket Validator

No changes - keep existing `gorilla/websocket` implementation with standard library proxy support.

### Data Flow

```
Scraper Flow:
TLS Client → free-proxy-list.net → Parse HTML → Proxy List

HTTP Validation Flow:
TLS Client + Proxy Config → Target URL → Success/Fail

WebSocket Validation Flow:
Standard Client + Proxy Config → WS Target → Success/Fail
```

### Configuration

- **Browser Profile:** Chrome_131 for all TLS clients
- **Scraper Timeout:** 20 seconds (hardcoded)
- **Validator Timeout:** Runtime-configurable via `PROXY_TIMEOUT` env (default 3s)
- **Cookie Handling:** Enabled with cookie jar
- **Redirect Handling:** Disabled (no-follow)

### Error Handling

- Maintain existing `slog` logging patterns
- TLS client errors map to same failure conditions
- No changes to validation success criteria (HTTP status 200-499)
- Preserve context cancellation behavior

## Trade-offs

**Pros:**
- Better browser fingerprinting reduces bot detection
- Improved success rates for scraping and validation
- Minimal architectural changes
- Maintains existing validation logic

**Cons:**
- Additional dependency (tls-client + fhttp)
- WebSocket validation still uses standard library
- Some code duplication (TLS client setup in 2 locations)

## Non-Goals

- WebSocket TLS fingerprinting (not supported by library)
- Configurable browser profiles (hardcoded to Chrome_131)
- Abstraction layer for swapping HTTP implementations
- Backwards compatibility with standard library

## Success Criteria

- All HTTP requests use TLS client with Chrome_131 profile
- Existing tests pass without modification
- Scraping and validation functionality unchanged
- No regressions in error handling or logging
