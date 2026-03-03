# TLS Client Integration Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace standard HTTP clients with bogdanfinn/tls-client library to mimic Chrome 131 browser TLS fingerprints for improved scraping and validation success rates.

**Architecture:** Direct replacement of `net/http` clients in scraper.go and validator.go with TLS client configured with Chrome_131 profile. WebSocket validation remains unchanged as TLS client doesn't support WebSocket protocol.

**Tech Stack:** Go 1.24.1, bogdanfinn/tls-client, bogdanfinn/fhttp, PuerkitoBio/goquery, gorilla/websocket

---

## Task 1: Add TLS Client Dependencies

**Files:**
- Modify: `go.mod`
- Modify: `go.sum` (auto-generated)

**Step 1: Add tls-client dependency**

Run: `go get github.com/bogdanfinn/tls-client`

Expected output: Package downloaded and added to go.mod

**Step 2: Add fhttp dependency**

Run: `go get github.com/bogdanfinn/fhttp`

Expected output: Package downloaded and added to go.mod

**Step 3: Verify dependencies**

Run: `go mod tidy`

Expected output: Dependencies resolved, go.sum updated

**Step 4: Verify build still works**

Run: `go build ./...`

Expected output: Build successful (no compilation errors)

**Step 5: Commit dependency changes**

```bash
git add go.mod go.sum
git commit -m "deps: add tls-client and fhttp for browser fingerprinting"
```

---

## Task 2: Update Scraper to Use TLS Client

**Files:**
- Modify: `scraper.go:1-128`

**Step 1: Update imports**

Replace the import section (lines 3-11) with:

```go
import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)
```

**Step 2: Replace HTTP client creation**

Replace lines 20-29 (request creation and client setup) with:

```go
	// Create TLS client to mimic real browser
	jar := tls_client.NewCookieJar()
	options := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(20),
		tls_client.WithClientProfile(profiles.Chrome_131),
		tls_client.WithNotFollowRedirects(),
		tls_client.WithCookieJar(jar),
	}

	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), options...)
	if err != nil {
		return nil, fmt.Errorf("failed to create TLS client: %w", err)
	}

	req, err := http.NewRequest(http.MethodGet, srcURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
```

**Step 3: Verify scraper compiles**

Run: `go build -o /dev/null ./scraper.go ./proxy.go ./validator.go`

Expected output: Compilation successful

**Step 4: Test scraper functionality**

Create test file `scraper_test_manual.go`:

```go
package freeproxy

import (
	"fmt"
	"testing"
)

func TestScraperWithTLSClient(t *testing.T) {
	proxies, err := scrape("US")
	if err != nil {
		t.Fatalf("scrape failed: %v", err)
	}
	if len(proxies) == 0 {
		t.Fatal("no proxies scraped")
	}
	fmt.Printf("Scraped %d proxies\n", len(proxies))
}
```

Run: `go test -v -run TestScraperWithTLSClient -timeout 30s`

Expected output: Test passes, proxies scraped successfully

**Step 5: Remove test file and commit**

```bash
rm scraper_test_manual.go
git add scraper.go
git commit -m "feat: replace HTTP client with TLS client in scraper"
```

---

## Task 3: Update HTTP Validator to Use TLS Client

**Files:**
- Modify: `validator.go:1-142`

**Step 1: Update imports**

Replace the import section (lines 3-14) with:

```go
import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
	"github.com/gorilla/websocket"
)
```

**Step 2: Create TLS client helper function**

Add new function after `isWebSocketURL` (after line 44):

```go
// createTLSClient creates a TLS client configured to mimic Chrome 131 browser.
// If proxyURL is provided, routes traffic through the proxy.
func createTLSClient(timeout time.Duration, proxyURL string) (tls_client.HttpClient, error) {
	jar := tls_client.NewCookieJar()
	options := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(int(timeout.Seconds())),
		tls_client.WithClientProfile(profiles.Chrome_131),
		tls_client.WithNotFollowRedirects(),
		tls_client.WithCookieJar(jar),
	}

	if proxyURL != "" {
		options = append(options, tls_client.WithProxyUrl(proxyURL))
	}

	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), options...)
	if err != nil {
		return nil, fmt.Errorf("failed to create TLS client: %w", err)
	}

	return client, nil
}
```

**Step 3: Replace validateHTTP implementation**

Replace the entire `validateHTTP` function (lines 46-95) with:

```go
func validateHTTP(ctx context.Context, proxy *FreeProxy, targetURL string) bool {
	slog.Info("testing HTTP proxy", "proxy_url", proxy.ProxyURL(), "target_url", targetURL)

	timeout := getValidationTimeout()

	// Create TLS client with proxy configuration
	client, err := createTLSClient(timeout, proxy.ProxyURL())
	if err != nil {
		slog.Info("proxy test failed", "proxy_url", proxy.ProxyURL(), "error", "failed to create TLS client")
		return false
	}

	// Create request with context for cancellation
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		slog.Info("proxy test failed", "proxy_url", proxy.ProxyURL(), "error", "failed to create request")
		return false
	}
	req.Header.Set("Cache-Control", "no-cache, no-store")
	req.Header.Set("Pragma", "no-cache")

	// Execute request
	resp, err := client.Do(req)
	if err != nil {
		slog.Info("proxy test failed", "proxy_url", proxy.ProxyURL(), "target_url", targetURL, "error", err.Error())
		return false
	}
	defer resp.Body.Close()

	success := resp.StatusCode >= 200 && resp.StatusCode < 500
	if success {
		slog.Info("proxy test success", "proxy_url", proxy.ProxyURL(), "target_url", targetURL, "status_code", resp.StatusCode)
	} else {
		slog.Info("proxy test failed", "proxy_url", proxy.ProxyURL(), "target_url", targetURL, "status_code", resp.StatusCode)
	}
	return success
}
```

**Step 4: Update validateWebSocket to use fhttp types**

Replace lines 97-105 (function signature and initial setup) with:

```go
func validateWebSocket(ctx context.Context, proxy *FreeProxy, targetURL string) bool {
	slog.Info("testing WebSocket proxy", "proxy_url", proxy.ProxyURL(), "target_url", targetURL)

	proxyURL, err := url.Parse(proxy.ProxyURL())
	if err != nil {
		slog.Info("proxy test failed", "proxy_url", proxy.ProxyURL(), "error", "invalid proxy URL")
		return false
	}

	dialer := websocket.Dialer{
		Proxy:            func(req *http.Request) (*url.URL, error) { return proxyURL, nil },
		HandshakeTimeout: getValidationTimeout(),
	}
```

**Step 5: Update WebSocket dial headers**

Replace line 120-123 with:

```go
		conn, resp, err := dialer.Dial(targetURL, http.Header{
			"Cache-Control": []string{"no-cache"},
		})
```

**Step 6: Verify validator compiles**

Run: `go build -o /dev/null ./validator.go ./proxy.go`

Expected output: Compilation successful

**Step 7: Test validator functionality**

Create test file `validator_test_manual.go`:

```go
package freeproxy

import (
	"context"
	"testing"
	"time"
)

func TestValidatorWithTLSClient(t *testing.T) {
	// Test with a known working proxy (or skip if none available)
	proxy := &FreeProxy{
		Scheme: "http",
		IP:     "httpbin.org",
		Port:   80,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// This will likely fail (httpbin.org is not a proxy), but tests the code path
	result := validateHTTP(ctx, proxy, "http://httpbin.org/get")
	t.Logf("Validation result: %v", result)
}
```

Run: `go test -v -run TestValidatorWithTLSClient -timeout 15s`

Expected output: Test completes (may pass or fail depending on proxy, but no compilation errors)

**Step 8: Remove test file and commit**

```bash
rm validator_test_manual.go
git add validator.go
git commit -m "feat: replace HTTP client with TLS client in validator"
```

---

## Task 4: Integration Testing

**Files:**
- Test: All components together

**Step 1: Build the server**

Run: `go build -o freeproxy-server ./server`

Expected output: Binary created successfully

**Step 2: Run integration test**

Create test file `integration_test_manual.go`:

```go
package freeproxy

import (
	"testing"
	"time"
)

func TestFullIntegrationWithTLSClient(t *testing.T) {
	// Set short TTL for testing
	SetTTL(1 * time.Minute)

	// Test GetProxyList (scraping)
	list, err := GetProxyList(FreeProxyParameter{
		CategoryCode: "US",
	})
	if err != nil {
		t.Fatalf("GetProxyList failed: %v", err)
	}
	t.Logf("Scraped %d proxies", len(list))

	if len(list) == 0 {
		t.Skip("No proxies available for validation test")
	}

	// Test GetProxy (validation)
	proxy, err := GetProxy(FreeProxyParameter{
		CategoryCode: "US",
		TargetUrl:    "http://httpbin.org/get",
	})
	if err != nil {
		t.Logf("GetProxy failed (expected if no working proxies): %v", err)
	} else {
		t.Logf("Found working proxy: %s", proxy.ProxyURL())
	}
}
```

Run: `go test -v -run TestFullIntegrationWithTLSClient -timeout 60s`

Expected output: Test passes, demonstrates scraping and validation work

**Step 3: Test server startup**

Run: `./freeproxy-server &`

Wait 2 seconds, then run: `curl http://localhost:15000/swagger.json`

Expected output: Swagger JSON returned

Kill server: `pkill -f freeproxy-server`

**Step 4: Remove test file and commit**

```bash
rm integration_test_manual.go
git add .
git commit -m "test: verify TLS client integration works end-to-end"
```

---

## Task 5: Update Documentation

**Files:**
- Modify: `README.md`

**Step 1: Add TLS client feature to features section**

In the Features section (after line 17), add:

```markdown
- 🔒 **Browser Fingerprinting** — Uses TLS client library to mimic Chrome 131 browser for better scraping success
```

**Step 2: Update dependencies section**

Add new section after Installation (around line 50):

```markdown
### Dependencies

This library uses the following key dependencies:
- `github.com/bogdanfinn/tls-client` - Browser TLS fingerprinting
- `github.com/PuerkitoBio/goquery` - HTML parsing
- `github.com/gofiber/fiber/v2` - HTTP server framework
- `github.com/gorilla/websocket` - WebSocket support
```

**Step 3: Commit documentation**

```bash
git add README.md
git commit -m "docs: document TLS client integration"
```

---

## Task 6: Final Verification and Cleanup

**Files:**
- Verify: All files compile and work

**Step 1: Clean build**

Run: `go clean && go build ./...`

Expected output: Clean build successful

**Step 2: Run go mod tidy**

Run: `go mod tidy`

Expected output: No changes (dependencies already clean)

**Step 3: Verify no unused imports**

Run: `gofmt -s -w .`

Expected output: No changes or only formatting fixes

**Step 4: Final commit if needed**

```bash
git add .
git commit -m "chore: cleanup formatting and imports"
```

**Step 5: Create summary**

The TLS client integration is complete. All HTTP requests now use Chrome 131 browser fingerprinting:
- Scraper uses TLS client for fetching proxy lists
- HTTP validator uses TLS client for testing proxies
- WebSocket validator continues using standard library (TLS client doesn't support WebSocket)

---

## Testing Checklist

- [ ] Dependencies added successfully
- [ ] Scraper compiles and runs
- [ ] HTTP validator compiles and runs
- [ ] WebSocket validator still works
- [ ] Integration test passes
- [ ] Server starts and responds
- [ ] Documentation updated
- [ ] All commits made with clear messages

## Rollback Plan

If issues occur, revert commits in reverse order:
```bash
git log --oneline  # Find commit hashes
git revert <commit-hash>  # Revert specific commit
```

Or reset to before TLS client integration:
```bash
git reset --hard <commit-before-integration>
```
