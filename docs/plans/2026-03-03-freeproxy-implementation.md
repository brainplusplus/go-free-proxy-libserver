# FreeProxy Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build a Go library to scrape/validate free proxies and a Fiber REST API server with auto-generated Swagger UI, Docker support.

**Architecture:** Library package (`freeproxy`) manages an in-memory proxy pool keyed by targetURL with mutex + singleflight for safe concurrent access. A Fiber server wraps the library with a route registry that auto-generates OpenAPI 2.0 spec via reflection — no codegen library needed.

**Tech Stack:** Go 1.21, goquery (scraping), gorilla/websocket (WS validation), Fiber v2 (HTTP server), godotenv (.env), golang.org/x/sync (singleflight), Docker multi-stage build.

---

### Task 1: Initialize Go Module

**Files:**
- Create: `go.mod`
- Create: `.gitignore`
- Create: `.env.example`

**Step 1: Initialize module**

Run:
```bash
cd D:/golang/go-free-proxy-server
go mod init github.com/yourusername/freeproxy
```
Expected: `go.mod` created with `module github.com/yourusername/freeproxy`

**Step 2: Add dependencies**

Run:
```bash
go get github.com/PuerkitoBio/goquery@v1.9.1
go get github.com/gofiber/fiber/v2@v2.52.4
go get github.com/gorilla/websocket@v1.5.1
go get github.com/joho/godotenv@v1.5.1
go get golang.org/x/sync@v0.7.0
```

**Step 3: Create `.gitignore`**

```
.env
*.exe
*.out
vendor/
```

**Step 4: Create `.env.example`**

```env
# API Key untuk autentikasi request
# Kosongkan untuk disable autentikasi (semua request diizinkan)
API_KEY=

# Durasi cache proxy dalam detik (default: 1800 = 30 menit)
TIME_EXPIRED=1800

# Port server (default: 8080)
PORT=8080
```

**Step 5: Commit**

```bash
git init
git add go.mod go.sum .gitignore .env.example
git commit -m "chore: initialize go module with dependencies"
```

---

### Task 2: Core Structs and Pool (`proxy.go`)

**Files:**
- Create: `proxy.go`

**Step 1: Write `proxy.go`**

```go
package freeproxy

import (
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

const (
	DefaultTargetURL = "http://httpbin.org/get"
	DefaultTTL       = 30 * time.Minute
)

// CategorySources maps category codes to their scrape URLs.
var CategorySources = map[string]string{
	"EN":  "https://free-proxy-list.net/en/",
	"UK":  "https://free-proxy-list.net/en/uk-proxy.html",
	"US":  "https://free-proxy-list.net/en/us-proxy.html",
	"SSL": "https://free-proxy-list.net/en/ssl-proxy.html",
}

type FreeProxy struct {
	Scheme       string    `json:"scheme"`
	IP           string    `json:"ip"`
	Port         int       `json:"port"`
	Username     string    `json:"username,omitempty"`
	Password     string    `json:"password,omitempty"`
	CategoryCode string    `json:"category_code"`
	CountryCode  string    `json:"country_code"`
	CountryName  string    `json:"country_name"`
	Anonym       bool      `json:"anonym"`
	Elite        bool      `json:"elite"`
	Google       bool      `json:"google"`
	HTTPS        bool      `json:"https"`
	LastChecked  time.Time `json:"last_checked"`
}

// ProxyURL returns the full proxy URL string.
func (fp *FreeProxy) ProxyURL() string {
	if fp.Username != "" {
		return fmt.Sprintf("%s://%s:%s@%s:%d", fp.Scheme, fp.Username, fp.Password, fp.IP, fp.Port)
	}
	return fmt.Sprintf("%s://%s:%d", fp.Scheme, fp.IP, fp.Port)
}

type FreeProxyParameter struct {
	CategoryCode string `json:"category_code,omitempty"`
	TargetUrl    string `json:"target_url,omitempty"`
}

func (p FreeProxyParameter) getTargetURL() string {
	if p.TargetUrl == "" {
		return DefaultTargetURL
	}
	return p.TargetUrl
}

type proxyPool struct {
	mu      sync.Mutex
	sf      singleflight.Group
	proxies map[string][]FreeProxy
	expiry  map[string]time.Time
	ttl     time.Duration
}

var defaultPool = &proxyPool{
	proxies: make(map[string][]FreeProxy),
	expiry:  make(map[string]time.Time),
	ttl:     DefaultTTL,
}

// SetTTL changes the cache TTL globally (call before using the library).
func SetTTL(d time.Duration) {
	defaultPool.mu.Lock()
	defer defaultPool.mu.Unlock()
	defaultPool.ttl = d
}

func (pp *proxyPool) needsRefresh(key string) bool {
	list, ok := pp.proxies[key]
	if !ok || len(list) == 0 {
		return true
	}
	exp, ok := pp.expiry[key]
	return !ok || time.Now().After(exp)
}

func (pp *proxyPool) load(key string) error {
	var all []FreeProxy
	for cat := range CategorySources {
		list, err := scrape(cat)
		if err != nil {
			continue // partial results OK
		}
		all = append(all, list...)
	}
	if len(all) == 0 {
		return fmt.Errorf("failed to scrape proxies from any source")
	}
	pp.mu.Lock()
	pp.proxies[key] = all
	pp.expiry[key] = time.Now().Add(pp.ttl)
	pp.mu.Unlock()
	return nil
}

func (pp *proxyPool) ensureLoaded(key string) error {
	pp.mu.Lock()
	needs := pp.needsRefresh(key)
	pp.mu.Unlock()

	if !needs {
		return nil
	}

	// singleflight prevents duplicate concurrent scrapes for same key
	_, err, _ := pp.sf.Do(key, func() (interface{}, error) {
		// re-check after acquiring singleflight
		pp.mu.Lock()
		needs := pp.needsRefresh(key)
		pp.mu.Unlock()
		if !needs {
			return nil, nil
		}
		return nil, pp.load(key)
	})
	return err
}

func (pp *proxyPool) pickRandom(key, categoryCode string) (*FreeProxy, bool) {
	pp.mu.Lock()
	defer pp.mu.Unlock()

	list := pp.proxies[key]
	var indices []int
	for i, p := range list {
		if categoryCode == "" || strings.EqualFold(p.CategoryCode, categoryCode) {
			indices = append(indices, i)
		}
	}
	if len(indices) == 0 {
		return nil, false
	}

	ridx := rand.Intn(len(indices))
	idx := indices[ridx]
	proxy := list[idx]

	// Swap-remove O(1)
	list[idx] = list[len(list)-1]
	pp.proxies[key] = list[:len(list)-1]

	return &proxy, true
}

func (pp *proxyPool) getAll(key, categoryCode string) []FreeProxy {
	pp.mu.Lock()
	defer pp.mu.Unlock()

	list := pp.proxies[key]
	result := make([]FreeProxy, 0, len(list))
	for _, p := range list {
		if categoryCode == "" || strings.EqualFold(p.CategoryCode, categoryCode) {
			result = append(result, p)
		}
	}
	return result
}

// GetProxy returns a single validated working proxy.
func GetProxy(param FreeProxyParameter) (*FreeProxy, error) {
	targetURL := param.getTargetURL()

	if err := defaultPool.ensureLoaded(targetURL); err != nil {
		return nil, fmt.Errorf("failed to load proxy pool: %w", err)
	}

	// snapshot the current count to cap attempts and avoid infinite loop
	defaultPool.mu.Lock()
	maxAttempts := len(defaultPool.proxies[targetURL])
	defaultPool.mu.Unlock()

	for i := 0; i < maxAttempts; i++ {
		proxy, ok := defaultPool.pickRandom(targetURL, param.CategoryCode)
		if !ok {
			break
		}
		if validateProxy(proxy, targetURL) {
			return proxy, nil
		}
	}

	return nil, fmt.Errorf("no working proxy found (pool exhausted)")
}

// GetProxyList returns all currently cached proxies without validating them.
func GetProxyList(param FreeProxyParameter) ([]FreeProxy, error) {
	targetURL := param.getTargetURL()

	if err := defaultPool.ensureLoaded(targetURL); err != nil {
		return nil, fmt.Errorf("failed to load proxy pool: %w", err)
	}

	return defaultPool.getAll(targetURL, param.CategoryCode), nil
}
```

**Step 2: Verify it compiles**

Run:
```bash
cd D:/golang/go-free-proxy-server
go build ./...
```
Expected: Build error about missing `scrape` and `validateProxy` — that's OK, it means the package is parsed correctly.

**Step 3: Commit**

```bash
git add proxy.go
git commit -m "feat: add core FreeProxy structs and in-memory pool with singleflight"
```

---

### Task 3: Scraper (`scraper.go`)

**Files:**
- Create: `scraper.go`

**Step 1: Write `scraper.go`**

```go
package freeproxy

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// scrape fetches and parses the proxy table for a given category code.
func scrape(categoryCode string) ([]FreeProxy, error) {
	srcURL, ok := CategorySources[categoryCode]
	if !ok {
		return nil, fmt.Errorf("unknown category code: %s", categoryCode)
	}

	req, err := http.NewRequest(http.MethodGet, srcURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Cache-Control", "no-cache, no-store, must-revalidate")
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("Expires", "0")

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse document: %w", err)
	}

	var proxies []FreeProxy

	doc.Find("#list tr").Each(func(i int, s *goquery.Selection) {
		if i == 0 {
			return // skip header row
		}

		row := s.Children()
		if row.Length() < 8 {
			return
		}

		ip := strings.TrimSpace(row.Eq(0).Text())
		portStr := strings.TrimSpace(row.Eq(1).Text())
		countryCode := strings.TrimSpace(row.Eq(2).Text())
		countryName := strings.TrimSpace(row.Eq(3).Text())
		anonymity := strings.ToLower(strings.TrimSpace(row.Eq(4).Text()))
		googleStr := strings.ToLower(strings.TrimSpace(row.Eq(5).Text()))
		httpsStr := strings.ToLower(strings.TrimSpace(row.Eq(6).Text()))
		lastCheckedStr := strings.TrimSpace(row.Eq(7).Text())

		if ip == "" || portStr == "" {
			return
		}

		port, err := strconv.Atoi(portStr)
		if err != nil {
			return
		}

		isHTTPS := httpsStr == "yes"
		scheme := "http"
		if isHTTPS {
			scheme = "https"
		}

		isElite := strings.Contains(anonymity, "elite")
		isAnon := isElite || strings.Contains(anonymity, "anonymous")

		proxies = append(proxies, FreeProxy{
			Scheme:       scheme,
			IP:           ip,
			Port:         port,
			CategoryCode: categoryCode,
			CountryCode:  countryCode,
			CountryName:  countryName,
			Anonym:       isAnon,
			Elite:        isElite,
			Google:       googleStr == "yes",
			HTTPS:        isHTTPS,
			LastChecked:  parseLastChecked(lastCheckedStr),
		})
	})

	return proxies, nil
}

// parseLastChecked converts "N minutes ago" style strings to time.Time.
func parseLastChecked(s string) time.Time {
	parts := strings.Fields(strings.ToLower(s))
	if len(parts) < 2 {
		return time.Time{}
	}

	val, err := strconv.Atoi(parts[0])
	if err != nil {
		return time.Time{}
	}

	unit := parts[1]
	now := time.Now()

	switch {
	case strings.HasPrefix(unit, "second"):
		return now.Add(-time.Duration(val) * time.Second)
	case strings.HasPrefix(unit, "minute"):
		return now.Add(-time.Duration(val) * time.Minute)
	case strings.HasPrefix(unit, "hour"):
		return now.Add(-time.Duration(val) * time.Hour)
	case strings.HasPrefix(unit, "day"):
		return now.Add(-time.Duration(val) * 24 * time.Hour)
	}

	return time.Time{}
}
```

**Step 2: Verify build**

Run:
```bash
go build ./...
```
Expected: Still missing `validateProxy` — OK.

**Step 3: Commit**

```bash
git add scraper.go
git commit -m "feat: add goquery scraper for free-proxy-list.net categories"
```

---

### Task 4: Validator (`validator.go`)

**Files:**
- Create: `validator.go`

**Step 1: Write `validator.go`**

```go
package freeproxy

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const validationTimeout = 10 * time.Second

// validateProxy tests a proxy against the targetURL.
func validateProxy(proxy *FreeProxy, targetURL string) bool {
	if isWebSocketURL(targetURL) {
		return validateWebSocket(proxy, targetURL)
	}
	return validateHTTP(proxy, targetURL)
}

func isWebSocketURL(u string) bool {
	lower := strings.ToLower(u)
	return strings.HasPrefix(lower, "ws://") || strings.HasPrefix(lower, "wss://")
}

func validateHTTP(proxy *FreeProxy, targetURL string) bool {
	proxyURL, err := url.Parse(proxy.ProxyURL())
	if err != nil {
		return false
	}

	transport := &http.Transport{
		Proxy:             http.ProxyURL(proxyURL),
		DisableKeepAlives: true,
	}
	defer transport.CloseIdleConnections()

	client := &http.Client{
		Transport: transport,
		Timeout:   validationTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	req, err := http.NewRequest(http.MethodGet, targetURL, nil)
	if err != nil {
		return false
	}
	req.Header.Set("Cache-Control", "no-cache, no-store")
	req.Header.Set("Pragma", "no-cache")

	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode >= 200 && resp.StatusCode < 400
}

func validateWebSocket(proxy *FreeProxy, targetURL string) bool {
	proxyURL, err := url.Parse(proxy.ProxyURL())
	if err != nil {
		return false
	}

	dialer := websocket.Dialer{
		Proxy:            http.ProxyURL(proxyURL),
		HandshakeTimeout: validationTimeout,
	}

	headers := http.Header{}
	headers.Set("Cache-Control", "no-cache")

	conn, resp, err := dialer.Dial(targetURL, headers)
	if err != nil {
		return false
	}
	defer conn.Close()
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}

	return true
}
```

**Step 2: Verify full library builds**

Run:
```bash
go build ./...
```
Expected: Builds successfully with no errors.

**Step 3: Commit**

```bash
git add validator.go
git commit -m "feat: add HTTP and WebSocket proxy validator"
```

---

### Task 5: Server — Route Registry (`server/registry.go`)

**Files:**
- Create: `server/registry.go`

**Step 1: Create server directory and `registry.go`**

```go
package main

import "github.com/gofiber/fiber/v2"

// RouteMeta holds metadata for a registered route.
// Used by swaggerSpec() to auto-generate OpenAPI documentation.
type RouteMeta struct {
	Method         string
	Path           string
	Summary        string
	QueryStruct    interface{} // reflect → query parameters
	ResponseStruct interface{} // reflect → response schema
	RequireAuth    bool
}

var routeRegistry []RouteMeta

// RegisterGET registers a GET route and records its metadata for Swagger auto-generation.
func RegisterGET(
	group fiber.Router,
	path string,
	summary string,
	handler fiber.Handler,
	queryStruct interface{},
	responseStruct interface{},
	requireAuth bool,
) {
	group.Get(path, handler)
	routeRegistry = append(routeRegistry, RouteMeta{
		Method:         "get",
		Path:           path,
		Summary:        summary,
		QueryStruct:    queryStruct,
		ResponseStruct: responseStruct,
		RequireAuth:    requireAuth,
	})
}
```

**Step 2: Commit**

```bash
git add server/registry.go
git commit -m "feat: add route registry for auto swagger generation"
```

---

### Task 6: Server — Auth Middleware (`server/middleware.go`)

**Files:**
- Create: `server/middleware.go`

**Step 1: Write `middleware.go`**

```go
package main

import "github.com/gofiber/fiber/v2"

// authMiddleware validates X-API-Key header or ?api_key= query param.
// If apiKey is empty string, all requests are allowed.
func authMiddleware(apiKey string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if apiKey == "" {
			return c.Next()
		}

		key := c.Get("X-API-Key")
		if key == "" {
			key = c.Query("api_key")
		}

		if key != apiKey {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "invalid or missing API key",
			})
		}

		return c.Next()
	}
}
```

**Step 2: Commit**

```bash
git add server/middleware.go
git commit -m "feat: add API key auth middleware"
```

---

### Task 7: Server — Handlers (`server/handler.go`)

**Files:**
- Create: `server/handler.go`

**Step 1: Write `handler.go`**

```go
package main

import (
	"github.com/gofiber/fiber/v2"

	freeproxy "github.com/yourusername/freeproxy"
)

// ProxyResponse wraps a single proxy result.
type ProxyResponse struct {
	Data  *freeproxy.FreeProxy `json:"data,omitempty"`
	Error string               `json:"error,omitempty"`
}

// ProxyListResponse wraps a list of proxies.
type ProxyListResponse struct {
	Data  []freeproxy.FreeProxy `json:"data"`
	Total int                   `json:"total"`
	Error string                `json:"error,omitempty"`
}

func getProxyHandler(c *fiber.Ctx) error {
	param := freeproxy.FreeProxyParameter{
		CategoryCode: c.Query("category_code"),
		TargetUrl:    c.Query("target_url"),
	}

	proxy, err := freeproxy.GetProxy(param)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(ProxyResponse{
			Error: err.Error(),
		})
	}

	return c.JSON(ProxyResponse{Data: proxy})
}

func getProxyListHandler(c *fiber.Ctx) error {
	param := freeproxy.FreeProxyParameter{
		CategoryCode: c.Query("category_code"),
		TargetUrl:    c.Query("target_url"),
	}

	list, err := freeproxy.GetProxyList(param)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(ProxyListResponse{
			Data:  []freeproxy.FreeProxy{},
			Total: 0,
			Error: err.Error(),
		})
	}

	if list == nil {
		list = []freeproxy.FreeProxy{}
	}

	return c.JSON(ProxyListResponse{
		Data:  list,
		Total: len(list),
	})
}
```

**Step 2: Commit**

```bash
git add server/handler.go
git commit -m "feat: add proxy get and list handlers"
```

---

### Task 8: Server — Auto Swagger (`server/swagger.go`)

**Files:**
- Create: `server/swagger.go`

**Step 1: Write `swagger.go`**

```go
package main

import (
	"encoding/json"
	"reflect"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// buildSchemaFromStruct generates an OpenAPI 2.0 schema object from a Go struct via reflection.
func buildSchemaFromStruct(v interface{}) map[string]interface{} {
	if v == nil {
		return map[string]interface{}{"type": "object"}
	}

	t := reflect.TypeOf(v)
	for t.Kind() == reflect.Ptr || t.Kind() == reflect.Slice {
		t = t.Elem()
	}

	if t.Kind() != reflect.Struct {
		return map[string]interface{}{"type": "object"}
	}

	properties := map[string]interface{}{}

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)

		jsonTag := field.Tag.Get("json")
		if jsonTag == "" || jsonTag == "-" {
			continue
		}
		name := strings.Split(jsonTag, ",")[0]

		swaggerProp := map[string]interface{}{}

		switch field.Type.Kind() {
		case reflect.String:
			swaggerProp["type"] = "string"
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			swaggerProp["type"] = "integer"
		case reflect.Bool:
			swaggerProp["type"] = "boolean"
		case reflect.Slice:
			swaggerProp["type"] = "array"
			swaggerProp["items"] = map[string]interface{}{"type": "string"}
		case reflect.Struct:
			if field.Type.String() == "time.Time" {
				swaggerProp["type"] = "string"
				swaggerProp["format"] = "date-time"
			} else {
				swaggerProp = buildSchemaFromStruct(reflect.New(field.Type).Interface())
			}
		default:
			swaggerProp["type"] = "string"
		}

		properties[name] = swaggerProp
	}

	return map[string]interface{}{
		"type":       "object",
		"properties": properties,
	}
}

// swaggerSpec builds the OpenAPI 2.0 specification automatically from routeRegistry.
func swaggerSpec() map[string]interface{} {
	paths := map[string]interface{}{}
	hasAuth := false

	for _, r := range routeRegistry {
		// Build query parameters from QueryStruct via reflection
		parameters := []map[string]interface{}{}

		if r.QueryStruct != nil {
			qt := reflect.TypeOf(r.QueryStruct)
			for qt.Kind() == reflect.Ptr {
				qt = qt.Elem()
			}

			if qt.Kind() == reflect.Struct {
				for i := 0; i < qt.NumField(); i++ {
					field := qt.Field(i)
					jsonTag := field.Tag.Get("json")
					if jsonTag == "" || jsonTag == "-" {
						continue
					}
					name := strings.Split(jsonTag, ",")[0]

					param := map[string]interface{}{
						"name":        name,
						"in":          "query",
						"required":    false,
						"type":        "string",
						"description": "",
					}

					// Enum hint for CategoryCode
					if name == "category_code" {
						param["enum"] = []string{"EN", "UK", "US", "SSL"}
						param["description"] = "Filter by category. Omit for all categories."
					}
					if name == "target_url" {
						param["description"] = "Target URL to test/pool key. Supports http, https, ws, wss. Default: http://httpbin.org/get"
					}

					parameters = append(parameters, param)
				}
			}
		}

		// Build response schema from ResponseStruct
		responseSchema := buildSchemaFromStruct(r.ResponseStruct)

		routeSpec := map[string]interface{}{
			"summary":    r.Summary,
			"tags":       []string{"proxy"},
			"parameters": parameters,
			"responses": map[string]interface{}{
				"200": map[string]interface{}{
					"description": "Success",
					"schema":      responseSchema,
				},
				"500": map[string]interface{}{
					"description": "Error",
					"schema":      responseSchema,
				},
			},
		}

		if r.RequireAuth {
			hasAuth = true
			routeSpec["security"] = []map[string]interface{}{
				{"ApiKeyAuth": []string{}},
			}
			routeSpec["responses"].(map[string]interface{})["401"] = map[string]interface{}{
				"description": "Unauthorized - invalid or missing API key",
			}
		}

		paths["/api/v1"+r.Path] = map[string]interface{}{
			r.Method: routeSpec,
		}
	}

	spec := map[string]interface{}{
		"swagger": "2.0",
		"info": map[string]interface{}{
			"title":       "FreeProxy API",
			"description": "REST API for fetching and validating free proxies scraped from free-proxy-list.net",
			"version":     "1.0.0",
		},
		"host":     "localhost:8080",
		"basePath": "/",
		"schemes":  []string{"http", "https"},
		"consumes": []string{"application/json"},
		"produces": []string{"application/json"},
		"paths":    paths,
	}

	if hasAuth {
		spec["securityDefinitions"] = map[string]interface{}{
			"ApiKeyAuth": map[string]interface{}{
				"type":        "apiKey",
				"in":          "header",
				"name":        "X-API-Key",
				"description": "API key. Also accepted as ?api_key= query param.",
			},
		}
	}

	return spec
}

func swaggerJSONHandler(c *fiber.Ctx) error {
	c.Set("Content-Type", "application/json")
	data, _ := json.MarshalIndent(swaggerSpec(), "", "  ")
	return c.Send(data)
}

// swaggerUIHandler serves Swagger UI via CDN — no binary assets needed.
func swaggerUIHandler(c *fiber.Ctx) error {
	html := `<!DOCTYPE html>
<html>
<head>
  <title>FreeProxy API - Swagger UI</title>
  <meta charset="utf-8"/>
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
</head>
<body>
<div id="swagger-ui"></div>
<script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
<script>
  SwaggerUIBundle({
    url: "/swagger.json",
    dom_id: '#swagger-ui',
    presets: [SwaggerUIBundle.presets.apis, SwaggerUIBundle.SwaggerUIStandalonePreset],
    layout: "BaseLayout",
    deepLinking: true
  });
</script>
</body>
</html>`
	c.Set("Content-Type", "text/html")
	return c.SendString(html)
}
```

**Step 2: Commit**

```bash
git add server/swagger.go
git commit -m "feat: add auto-generating OpenAPI 2.0 swagger spec via reflection"
```

---

### Task 9: Server — Entry Point (`server/main.go`)

**Files:**
- Create: `server/main.go`

**Step 1: Write `main.go`**

```go
package main

import (
	"log"
	"os"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/joho/godotenv"

	freeproxy "github.com/yourusername/freeproxy"
)

func main() {
	_ = godotenv.Load() // ignore error if no .env file

	// Configure TTL from env
	if v := os.Getenv("TIME_EXPIRED"); v != "" {
		secs, err := strconv.Atoi(v)
		if err == nil && secs > 0 {
			freeproxy.SetTTL(time.Duration(secs) * time.Second)
		}
	}

	apiKey := os.Getenv("API_KEY")

	app := fiber.New(fiber.Config{
		AppName: "FreeProxy API v1",
	})

	app.Use(recover.New())
	app.Use(logger.New())
	app.Use(cors.New())

	// Swagger endpoints — no auth required
	app.Get("/swagger.json", swaggerJSONHandler)
	app.Get("/swagger", swaggerUIHandler)

	// API routes — auth applied per handler via RequireAuth flag in registry
	api := app.Group("/api/v1", authMiddleware(apiKey))

	RegisterGET(api, "/proxy/get",
		"Get a single validated working proxy",
		getProxyHandler,
		freeproxy.FreeProxyParameter{},
		ProxyResponse{},
		true,
	)

	RegisterGET(api, "/proxy/list",
		"List all cached proxies (not validated)",
		getProxyListHandler,
		freeproxy.FreeProxyParameter{},
		ProxyListResponse{},
		true,
	)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Server running on http://localhost:%s", port)
	log.Printf("Swagger UI: http://localhost:%s/swagger", port)
	log.Fatal(app.Listen(":" + port))
}
```

**Step 2: Verify server builds**

Run:
```bash
cd D:/golang/go-free-proxy-server
go build ./server/...
```
Expected: No errors.

**Step 3: Commit**

```bash
git add server/main.go
git commit -m "feat: add Fiber server entry point with env config and route registration"
```

---

### Task 10: Dockerfile and docker-compose

**Files:**
- Create: `Dockerfile`
- Create: `docker-compose.yml`

**Step 1: Write `Dockerfile`**

```dockerfile
# Stage 1: Build
FROM golang:1.21-alpine AS builder

WORKDIR /app

# Install git (needed for go get with some modules)
RUN apk add --no-cache git

# Copy module files first for layer caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build the server binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/server ./server/...

# Stage 2: Runtime
FROM alpine:3.19

# ca-certificates needed for HTTPS scraping
RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

# Copy compiled binary
COPY --from=builder /app/server .

EXPOSE 8080

ENTRYPOINT ["/app/server"]
```

**Step 2: Write `docker-compose.yml`**

```yaml
version: "3.9"

services:
  freeproxy:
    build:
      context: .
      dockerfile: Dockerfile
    container_name: freeproxy-server
    env_file:
      - .env
    ports:
      - "${PORT:-8080}:8080"
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "wget", "--spider", "-q", "http://localhost:8080/swagger.json"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 10s
```

**Step 3: Verify `.env` file exists (copy from example)**

Run:
```bash
cp .env.example .env
```

**Step 4: Build Docker image to verify**

Run:
```bash
docker build -t freeproxy:latest .
```
Expected: Successfully built image.

**Step 5: Commit**

```bash
git add Dockerfile docker-compose.yml
git commit -m "feat: add multi-stage Dockerfile and docker-compose with .env support"
```

---

### Task 11: Final Verification

**Step 1: Full build check**

Run:
```bash
cd D:/golang/go-free-proxy-server
go build ./...
go vet ./...
```
Expected: No errors, no warnings.

**Step 2: Run server locally**

Run:
```bash
cd D:/golang/go-free-proxy-server/server
go run .
```
Expected:
```
Server running on http://localhost:8080
Swagger UI: http://localhost:8080/swagger
```

**Step 3: Verify Swagger UI**

Open browser: `http://localhost:8080/swagger`
Expected:
- Swagger UI loads
- Two endpoints visible: `/api/v1/proxy/get`, `/api/v1/proxy/list`
- If `API_KEY` set: **Authorize** button appears
- Both endpoints show `category_code` enum (EN/UK/US/SSL) and `target_url` params

**Step 4: Verify swagger.json**

Run:
```bash
curl http://localhost:8080/swagger.json
```
Expected: Valid JSON with `paths`, `securityDefinitions` (if API_KEY set), `info`.

**Step 5: Run with docker-compose**

Run:
```bash
docker compose up --build
```
Expected: Container starts, health check passes after ~10s.

**Step 6: Final commit**

```bash
git add .
git commit -m "chore: final verification — all files complete"
```

---

## Adding New API Endpoints (Reference)

To add a new endpoint in the future with zero Swagger maintenance:

```go
// 1. Add handler in server/handler.go
type StatsResponse struct {
    PoolSize int    `json:"pool_size"`
    TTL      string `json:"ttl"`
}

func getStatsHandler(c *fiber.Ctx) error { ... }

// 2. Register in server/main.go — Swagger auto-updates
RegisterGET(api, "/proxy/stats",
    "Get pool statistics",
    getStatsHandler,
    nil,           // no query params
    StatsResponse{},
    true,
)
```

That's it. No changes to `swagger.go` needed.
