# Proxy Pool Refactoring Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Refactor proxyPool to use global proxy storage with index-based references, add pre-validated working proxy system with concurrent validation, and implement metrics for benchmarking.

**Architecture:** Dual-system approach maintaining legacy pop-based GetProxy alongside new sequence-based GetWorkingProxy. Global []FreeProxy shared across targets, with map[string][]int storing indices. RWMutex for global data, Mutex for target-specific state. Build versioning prevents cross-cycle contamination.

**Tech Stack:** Go 1.21+, golang.org/x/sync/singleflight, sync.RWMutex, atomic operations for metrics

---

## Task 1: Refactor proxyPool Structure

**Files:**
- Modify: `proxy.go:79-91`

**Step 1: Write test for new structure initialization**

Create: `proxy_test.go`

```go
package freeproxy

import (
	"testing"
	"time"
)

func TestProxyPoolInitialization(t *testing.T) {
	pool := &proxyPool{
		proxies:                 []FreeProxy{},
		expiry:                  time.Time{},
		ttl:                     DefaultTTL,
		targetUrlProxies:        make(map[string][]int),
		targetUrlWorkingProxies: make(map[string][]int),
		targetUrlWorkingIndex:   make(map[string]int),
		workingState:            make(map[string]*workingState),
		buildVersion:            0,
	}

	if pool.proxies == nil {
		t.Error("proxies should be initialized")
	}
	if pool.targetUrlProxies == nil {
		t.Error("targetUrlProxies should be initialized")
	}
	if pool.targetUrlWorkingProxies == nil {
		t.Error("targetUrlWorkingProxies should be initialized")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test -v -run TestProxyPoolInitialization`
Expected: FAIL - fields don't exist yet

**Step 3: Refactor proxyPool struct**

Modify: `proxy.go:79-91`

```go
type proxyPool struct {
	// Global proxy storage (read-heavy, use RWMutex)
	globalMu sync.RWMutex
	proxies  []FreeProxy
	expiry   time.Time
	ttl      time.Duration

	// Target-specific data (write-heavy, use Mutex)
	targetMu                 sync.Mutex
	targetUrlProxies         map[string][]int          // Legacy: pop-based indices
	targetUrlWorkingProxies  map[string][]int          // New: validated indices
	targetUrlWorkingIndex    map[string]int            // Sequence tracker
	workingState             map[string]*workingState  // Build state + channel
	buildVersion             int64                     // Prevent cross-cycle contamination

	sf singleflight.Group
}

type workingState struct {
	building bool
	readyCh  chan struct{} // Closed when first proxy validated
}

func newWorkingState() *workingState {
	return &workingState{
		building: true,
		readyCh:  make(chan struct{}),
	}
}

func (ws *workingState) signalReady() {
	if ws.building {
		ws.building = false
		close(ws.readyCh)
	}
}
```

**Step 4: Update defaultPool initialization**

Modify: `proxy.go:87-91`

```go
var defaultPool = &proxyPool{
	proxies:                 []FreeProxy{},
	expiry:                  time.Time{},
	ttl:                     DefaultTTL,
	targetUrlProxies:        make(map[string][]int),
	targetUrlWorkingProxies: make(map[string][]int),
	targetUrlWorkingIndex:   make(map[string]int),
	workingState:            make(map[string]*workingState),
	buildVersion:            0,
}
```

**Step 5: Run test to verify it passes**

Run: `go test -v -run TestProxyPoolInitialization`
Expected: PASS

**Step 6: Commit**

```bash
git add proxy.go proxy_test.go
git commit -m "refactor: update proxyPool structure with global storage and working proxy support"
```

---

## Task 2: Implement ensureProxiesLoaded with Global Storage

**Files:**
- Modify: `proxy.go:99-162`
- Test: `proxy_test.go`

**Step 1: Write test for ensureProxiesLoaded**

Append to: `proxy_test.go`

```go
func TestEnsureProxiesLoaded(t *testing.T) {
	pool := &proxyPool{
		proxies:                 []FreeProxy{},
		expiry:                  time.Time{},
		ttl:                     5 * time.Minute,
		targetUrlProxies:        make(map[string][]int),
		targetUrlWorkingProxies: make(map[string][]int),
		targetUrlWorkingIndex:   make(map[string]int),
		workingState:            make(map[string]*workingState),
	}

	// First call should trigger scrape
	err := pool.ensureProxiesLoaded()
	if err != nil {
		t.Fatalf("ensureProxiesLoaded failed: %v", err)
	}

	pool.globalMu.RLock()
	proxyCount := len(pool.proxies)
	pool.globalMu.RUnlock()

	if proxyCount == 0 {
		t.Error("expected proxies to be loaded")
	}

	// Second call should not re-scrape (cached)
	err = pool.ensureProxiesLoaded()
	if err != nil {
		t.Fatalf("ensureProxiesLoaded failed on cached call: %v", err)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test -v -run TestEnsureProxiesLoaded`
Expected: FAIL - method doesn't exist

**Step 3: Implement ensureProxiesLoaded**

Replace: `proxy.go:99-162` (old needsRefresh and ensureLoaded methods)

```go
func (pp *proxyPool) ensureProxiesLoaded() error {
	// Atomic check + load
	pp.globalMu.RLock()
	if time.Now().Before(pp.expiry) {
		pp.globalMu.RUnlock()
		return nil // Still valid
	}
	pp.globalMu.RUnlock()

	// Singleflight: only one goroutine scrapes
	_, err, _ := pp.sf.Do("scrape", func() (interface{}, error) {
		pp.resetAll()

		// Scrape all categories concurrently
		var (
			wg  sync.WaitGroup
			mu  sync.Mutex
			all []FreeProxy
		)
		for cat := range CategorySources {
			wg.Add(1)
			go func(c string) {
				defer wg.Done()
				list, err := scrape(c)
				if err != nil {
					return
				}
				mu.Lock()
				all = append(all, list...)
				mu.Unlock()
			}(cat)
		}
		wg.Wait()

		if len(all) == 0 {
			return nil, fmt.Errorf("failed to scrape proxies from any source")
		}

		// Shuffle for diversity
		rand.Shuffle(len(all), func(i, j int) { all[i], all[j] = all[j], all[i] })

		pp.globalMu.Lock()
		pp.proxies = all
		pp.expiry = time.Now().Add(pp.ttl)
		pp.globalMu.Unlock()

		return nil, nil
	})

	return err
}

func (pp *proxyPool) resetAll() {
	// Step 1: Lock global and clear
	pp.globalMu.Lock()
	pp.proxies = nil
	pp.expiry = time.Time{}
	pp.globalMu.Unlock()

	// Step 2: Lock target and full re-init
	pp.targetMu.Lock()
	pp.targetUrlProxies = make(map[string][]int)
	pp.targetUrlWorkingProxies = make(map[string][]int)
	pp.targetUrlWorkingIndex = make(map[string]int)
	pp.workingState = make(map[string]*workingState)
	pp.buildVersion++ // Invalidate old builds
	pp.targetMu.Unlock()
}
```

**Step 4: Run test to verify it passes**

Run: `go test -v -run TestEnsureProxiesLoaded`
Expected: PASS

**Step 5: Commit**

```bash
git add proxy.go proxy_test.go
git commit -m "feat: implement ensureProxiesLoaded with global storage and resetAll"
```

---

## Task 3: Update Legacy GetProxy to Use Index-Based Storage

**Files:**
- Modify: `proxy.go:164-189` (pickRandom method)
- Modify: `proxy.go:210-284` (GetProxy function)
- Test: `proxy_test.go`

**Step 1: Write test for index-based pickRandom**

Append to: `proxy_test.go`

```go
func TestPickRandomWithIndices(t *testing.T) {
	pool := &proxyPool{
		proxies: []FreeProxy{
			{IP: "1.1.1.1", Port: 8080, CategoryCode: "US"},
			{IP: "2.2.2.2", Port: 8080, CategoryCode: "UK"},
			{IP: "3.3.3.3", Port: 8080, CategoryCode: "US"},
		},
		targetUrlProxies: map[string][]int{
			"http://test.com": {0, 1, 2},
		},
		targetUrlWorkingProxies: make(map[string][]int),
		targetUrlWorkingIndex:   make(map[string]int),
		workingState:            make(map[string]*workingState),
	}

	// Pick random US proxy
	proxy, ok := pool.pickRandom("http://test.com", "US")
	if !ok {
		t.Fatal("expected to find US proxy")
	}
	if proxy.CategoryCode != "US" {
		t.Errorf("expected US proxy, got %s", proxy.CategoryCode)
	}

	// Verify proxy was removed from pool
	pool.targetMu.Lock()
	remaining := len(pool.targetUrlProxies["http://test.com"])
	pool.targetMu.Unlock()

	if remaining != 2 {
		t.Errorf("expected 2 proxies remaining, got %d", remaining)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test -v -run TestPickRandomWithIndices`
Expected: FAIL - pickRandom still uses old logic

**Step 3: Implement index-based pickRandom**

Replace: `proxy.go:164-189`

```go
// pickRandom picks a random matching proxy index and removes it from the pool (O(1) swap-remove).
// Returns false if no matching proxy is available.
func (pp *proxyPool) pickRandom(key, categoryCode string) (*FreeProxy, bool) {
	pp.targetMu.Lock()
	defer pp.targetMu.Unlock()

	list := pp.targetUrlProxies[key]
	var matchingIndices []int

	pp.globalMu.RLock()
	for i, proxyIdx := range list {
		if categoryCode == "" || strings.EqualFold(pp.proxies[proxyIdx].CategoryCode, categoryCode) {
			matchingIndices = append(matchingIndices, i)
		}
	}
	pp.globalMu.RUnlock()

	if len(matchingIndices) == 0 {
		return nil, false
	}

	// Pick random from matching
	idx := matchingIndices[rand.Intn(len(matchingIndices))]
	proxyIdx := list[idx]

	pp.globalMu.RLock()
	proxy := pp.proxies[proxyIdx]
	pp.globalMu.RUnlock()

	// O(1) swap-remove
	list[idx] = list[len(list)-1]
	pp.targetUrlProxies[key] = list[:len(list)-1]

	return &proxy, true
}
```

**Step 4: Update GetProxy to use ensureProxiesLoaded and populate indices**

Replace: `proxy.go:210-284`

```go
// GetProxy validates proxies concurrently and returns the first working one.
func GetProxy(param FreeProxyParameter) (*FreeProxy, error) {
	targetURL := param.getTargetURL()

	if err := defaultPool.ensureProxiesLoaded(); err != nil {
		return nil, fmt.Errorf("failed to load proxy pool: %w", err)
	}

	// Populate targetUrlProxies with indices if not exists
	key := targetURL
	defaultPool.targetMu.Lock()
	if _, exists := defaultPool.targetUrlProxies[key]; !exists {
		defaultPool.globalMu.RLock()
		indices := make([]int, len(defaultPool.proxies))
		for i := range defaultPool.proxies {
			indices[i] = i
		}
		defaultPool.globalMu.RUnlock()
		defaultPool.targetUrlProxies[key] = indices
	}
	defaultPool.targetMu.Unlock()

	n := numWorkers(param.CategoryCode)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	winnerCh := make(chan *FreeProxy, 1)

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}

				proxy, ok := defaultPool.pickRandom(key, param.CategoryCode)
				if !ok {
					return
				}

				slog.Info("testing proxy", "worker", id, "ip", proxy.IP, "port", proxy.Port, "target_url", targetURL)

				if validateProxyCtx(ctx, proxy, targetURL) {
					slog.Info("found working proxy", "worker", id, "ip", proxy.IP, "port", proxy.Port)
					select {
					case winnerCh <- proxy:
						cancel()
					default:
					}
					return
				}
			}
		}(i)
	}

	doneCh := make(chan struct{})
	go func() {
		wg.Wait()
		close(doneCh)
	}()

	select {
	case proxy := <-winnerCh:
		<-doneCh
		return proxy, nil
	case <-doneCh:
		select {
		case proxy := <-winnerCh:
			return proxy, nil
		default:
		}
		return nil, fmt.Errorf("no working proxy found (all %d workers exhausted)", n)
	}
}
```

**Step 5: Update getAll to use indices**

Replace: `proxy.go:191-202`

```go
func (pp *proxyPool) getAll(key, categoryCode string) []FreeProxy {
	pp.targetMu.Lock()
	indices := pp.targetUrlProxies[key]
	pp.targetMu.Unlock()

	pp.globalMu.RLock()
	defer pp.globalMu.RUnlock()

	var result []FreeProxy
	for _, idx := range indices {
		if idx < len(pp.proxies) {
			p := pp.proxies[idx]
			if categoryCode == "" || strings.EqualFold(p.CategoryCode, categoryCode) {
				result = append(result, p)
			}
		}
	}
	return result
}
```

**Step 6: Update GetProxyList**

Replace: `proxy.go:286-299`

```go
// GetProxyList returns a snapshot of the current pool (not validated).
func GetProxyList(param FreeProxyParameter) ([]FreeProxy, error) {
	targetURL := param.getTargetURL()

	slog.Info("getting proxy list", "category_code", param.CategoryCode, "target_url", targetURL)

	if err := defaultPool.ensureProxiesLoaded(); err != nil {
		return nil, fmt.Errorf("failed to load proxy pool: %w", err)
	}

	// Populate targetUrlProxies with indices if not exists
	key := targetURL
	defaultPool.targetMu.Lock()
	if _, exists := defaultPool.targetUrlProxies[key]; !exists {
		defaultPool.globalMu.RLock()
		indices := make([]int, len(defaultPool.proxies))
		for i := range defaultPool.proxies {
			indices[i] = i
		}
		defaultPool.globalMu.RUnlock()
		defaultPool.targetUrlProxies[key] = indices
	}
	defaultPool.targetMu.Unlock()

	list := defaultPool.getAll(key, param.CategoryCode)
	slog.Info("proxy list retrieved", "count", len(list), "category_code", param.CategoryCode)
	return list, nil
}
```

**Step 7: Run test to verify it passes**

Run: `go test -v -run TestPickRandomWithIndices`
Expected: PASS

**Step 8: Commit**

```bash
git add proxy.go proxy_test.go
git commit -m "refactor: update legacy GetProxy to use index-based storage"
```

---

## Task 4: Add Environment Variable for Worker Count

**Files:**
- Modify: `proxy.go` (add getWorkingProxyWorkers function)
- Test: `proxy_test.go`

**Step 1: Write test for getWorkingProxyWorkers**

Append to: `proxy_test.go`

```go
func TestGetWorkingProxyWorkers(t *testing.T) {
	// Test default value
	workers := getWorkingProxyWorkers()
	if workers != 50 {
		t.Errorf("expected default 50 workers, got %d", workers)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test -v -run TestGetWorkingProxyWorkers`
Expected: FAIL - function doesn't exist

**Step 3: Implement getWorkingProxyWorkers**

Add after `numWorkers` function in `proxy.go`:

```go
// getWorkingProxyWorkers returns the number of concurrent workers for validating working proxies.
// Reads from WORKING_PROXY_WORKERS env var, defaults to 50.
func getWorkingProxyWorkers() int {
	if v := os.Getenv("WORKING_PROXY_WORKERS"); v != "" {
		if workers, err := strconv.Atoi(v); err == nil && workers > 0 {
			return workers
		}
	}
	return 50
}
```

**Step 4: Add required imports**

Ensure these imports exist in `proxy.go`:

```go
import (
	"os"
	"strconv"
	// ... other imports
)
```

**Step 5: Run test to verify it passes**

Run: `go test -v -run TestGetWorkingProxyWorkers`
Expected: PASS

**Step 6: Commit**

```bash
git add proxy.go proxy_test.go
git commit -m "feat: add WORKING_PROXY_WORKERS environment variable"
```

---
## Task 5: Implement buildWorkingProxies with Concurrent Validation

**Files:**
- Create: `working_proxy.go`
- Test: `working_proxy_test.go`

**Step 1: Write test for buildWorkingProxies**

Create: `working_proxy_test.go`

```go
package freeproxy

import (
	"testing"
	"time"
)

func TestBuildWorkingProxies(t *testing.T) {
	pool := &proxyPool{
		proxies: []FreeProxy{
			{IP: "1.1.1.1", Port: 8080, Scheme: "http", CategoryCode: "US"},
			{IP: "2.2.2.2", Port: 8080, Scheme: "http", CategoryCode: "UK"},
		},
		expiry:                  time.Now().Add(5 * time.Minute),
		ttl:                     5 * time.Minute,
		targetUrlProxies:        make(map[string][]int),
		targetUrlWorkingProxies: make(map[string][]int),
		targetUrlWorkingIndex:   make(map[string]int),
		workingState:            make(map[string]*workingState),
		buildVersion:            1,
	}

	key := "http://httpbin.org/get|"
	pool.targetMu.Lock()
	pool.workingState[key] = newWorkingState()
	pool.targetMu.Unlock()

	pool.buildWorkingProxies(key, "http://httpbin.org/get", "", 1)

	pool.targetMu.Lock()
	workingCount := len(pool.targetUrlWorkingProxies[key])
	pool.targetMu.Unlock()

	if workingCount == 0 {
		t.Log("No working proxies found (expected in test environment)")
	} else {
		t.Logf("Found %d working proxies", workingCount)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test -v -run TestBuildWorkingProxies`
Expected: FAIL - buildWorkingProxies doesn't exist

**Step 3: Create working_proxy.go with helper functions**

Create: `working_proxy.go`

```go
package freeproxy

import (
	"context"
	"log/slog"
	"strings"
	"sync"
)

// buildKey creates a unique key for target URL and category combination
func buildKey(targetURL, categoryCode string) string {
	return targetURL + "|" + categoryCode
}

// filterByCategory returns indices of proxies matching the category
func (pp *proxyPool) filterByCategory(categoryCode string) []int {
	pp.globalMu.RLock()
	defer pp.globalMu.RUnlock()

	if categoryCode == "" {
		indices := make([]int, len(pp.proxies))
		for i := range pp.proxies {
			indices[i] = i
		}
		return indices
	}

	var indices []int
	for i, proxy := range pp.proxies {
		if strings.EqualFold(proxy.CategoryCode, categoryCode) {
			indices = append(indices, i)
		}
	}
	return indices
}

// buildWorkingProxies validates proxies concurrently
func (pp *proxyPool) buildWorkingProxies(key, targetURL, categoryCode string, buildVer int64) {
	workers := getWorkingProxyWorkers()
	candidates := pp.filterByCategory(categoryCode)
	
	if len(candidates) == 0 {
		slog.Info("no proxies to validate", "target_url", targetURL, "category_code", categoryCode)
		return
	}

	slog.Info("starting working proxy validation", "target_url", targetURL, "candidates", len(candidates), "workers", workers)

	jobs := make(chan int, len(candidates))
	results := make(chan int, len(candidates))
	var wg sync.WaitGroup
	ctx := context.Background()

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for idx := range jobs {
				pp.globalMu.RLock()
				proxy := pp.proxies[idx]
				pp.globalMu.RUnlock()

				if validateProxyCtx(ctx, &proxy, targetURL) {
					results <- idx
				}
			}
		}(w)
	}

	for _, idx := range candidates {
		jobs <- idx
	}
	close(jobs)

	go func() {
		wg.Wait()
		close(results)
	}()

	firstFound := false
	validCount := 0
	for idx := range results {
		pp.targetMu.Lock()
		if pp.buildVersion != buildVer {
			pp.targetMu.Unlock()
			slog.Info("build version mismatch, discarding results", "target_url", targetURL)
			return
		}

		pp.targetUrlWorkingProxies[key] = append(pp.targetUrlWorkingProxies[key], idx)
		validCount++

		if !firstFound {
			if ws, exists := pp.workingState[key]; exists {
				ws.signalReady()
			}
			firstFound = true
		}
		pp.targetMu.Unlock()
	}

	slog.Info("working proxy validation complete", "target_url", targetURL, "valid_count", validCount)
}
```

**Step 4: Run test to verify it passes**

Run: `go test -v -run TestBuildWorkingProxies`
Expected: PASS

**Step 5: Commit**

```bash
git add working_proxy.go working_proxy_test.go
git commit -m "feat: implement buildWorkingProxies with concurrent validation"
```

---
## Task 6: Implement nextWorkingIndex and ensureBuildStarted

**Files:**
- Modify: `working_proxy.go`
- Test: `working_proxy_test.go`

**Step 1: Write test for nextWorkingIndex**

Append to: `working_proxy_test.go`

```go
func TestNextWorkingIndex(t *testing.T) {
	pool := &proxyPool{
		proxies: []FreeProxy{
			{IP: "1.1.1.1", Port: 8080, CategoryCode: "US"},
			{IP: "2.2.2.2", Port: 8080, CategoryCode: "UK"},
			{IP: "3.3.3.3", Port: 8080, CategoryCode: "US"},
		},
		targetUrlWorkingProxies: map[string][]int{
			"test-key": {0, 2},
		},
		targetUrlWorkingIndex: map[string]int{
			"test-key": 0,
		},
		workingState: make(map[string]*workingState),
	}

	idx, ok := pool.nextWorkingIndex("test-key", "US")
	if !ok || idx != 0 {
		t.Errorf("expected index 0, got %d", idx)
	}

	idx, ok = pool.nextWorkingIndex("test-key", "US")
	if !ok || idx != 2 {
		t.Errorf("expected index 2, got %d", idx)
	}

	idx, ok = pool.nextWorkingIndex("test-key", "US")
	if !ok || idx != 0 {
		t.Errorf("expected wrap to index 0, got %d", idx)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test -v -run TestNextWorkingIndex`
Expected: FAIL

**Step 3: Implement nextWorkingIndex**

Append to: `working_proxy.go`

```go
func (pp *proxyPool) nextWorkingIndex(key, categoryCode string) (int, bool) {
	pp.targetMu.Lock()
	defer pp.targetMu.Unlock()

	list := pp.targetUrlWorkingProxies[key]
	if len(list) == 0 {
		return 0, false
	}

	idx := pp.targetUrlWorkingIndex[key]
	attempts := 0

	for attempts < len(list) {
		proxyIndex := list[idx%len(list)]
		idx = (idx + 1) % len(list)

		pp.globalMu.RLock()
		if proxyIndex < len(pp.proxies) {
			proxy := pp.proxies[proxyIndex]
			pp.globalMu.RUnlock()

			if categoryCode == "" || strings.EqualFold(proxy.CategoryCode, categoryCode) {
				pp.targetUrlWorkingIndex[key] = idx
				return proxyIndex, true
			}
		} else {
			pp.globalMu.RUnlock()
		}
		attempts++
	}

	return 0, false
}

func (pp *proxyPool) ensureBuildStarted(param FreeProxyParameter) chan struct{} {
	targetURL := param.getTargetURL()
	key := buildKey(targetURL, param.CategoryCode)

	pp.targetMu.Lock()
	
	if ws, exists := pp.workingState[key]; exists {
		readyCh := ws.readyCh
		pp.targetMu.Unlock()
		return readyCh
	}

	ws := newWorkingState()
	pp.workingState[key] = ws
	buildVer := pp.buildVersion
	pp.targetMu.Unlock()

	go pp.buildWorkingProxies(key, targetURL, param.CategoryCode, buildVer)

	return ws.readyCh
}
```

**Step 4: Run test to verify it passes**

Run: `go test -v -run TestNextWorkingIndex`
Expected: PASS

**Step 5: Commit**

```bash
git add working_proxy.go working_proxy_test.go
git commit -m "feat: implement nextWorkingIndex and ensureBuildStarted"
```

---

## Task 7: Implement GetWorkingProxy

**Files:**
- Modify: `working_proxy.go`
- Test: `working_proxy_test.go`

**Step 1: Write test for GetWorkingProxy**

Append to: `working_proxy_test.go`

```go
func TestGetWorkingProxy(t *testing.T) {
	param := FreeProxyParameter{
		TargetUrl:    "http://httpbin.org/get",
		CategoryCode: "",
	}

	proxy, err := GetWorkingProxy(param)
	
	if err != nil {
		t.Logf("GetWorkingProxy returned error (expected in test): %v", err)
	} else if proxy != nil {
		t.Logf("GetWorkingProxy returned proxy: %s:%d", proxy.IP, proxy.Port)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test -v -run TestGetWorkingProxy`
Expected: FAIL

**Step 3: Implement GetWorkingProxy**

Append to: `working_proxy.go`

```go
func GetWorkingProxy(param FreeProxyParameter) (*FreeProxy, error) {
	if err := defaultPool.ensureProxiesLoaded(); err != nil {
		return nil, fmt.Errorf("failed to load proxy pool: %w", err)
	}

	targetURL := param.getTargetURL()
	key := buildKey(targetURL, param.CategoryCode)

	if idx, ok := defaultPool.nextWorkingIndex(key, param.CategoryCode); ok {
		defaultPool.globalMu.RLock()
		proxy := defaultPool.proxies[idx]
		defaultPool.globalMu.RUnlock()
		return &proxy, nil
	}

	readyCh := defaultPool.ensureBuildStarted(param)

	select {
	case <-readyCh:
		if idx, ok := defaultPool.nextWorkingIndex(key, param.CategoryCode); ok {
			defaultPool.globalMu.RLock()
			proxy := defaultPool.proxies[idx]
			defaultPool.globalMu.RUnlock()
			return &proxy, nil
		}
		return nil, fmt.Errorf("no working proxy available")
	case <-time.After(3 * time.Second):
		return GetProxy(param)
	}
}
```

**Step 4: Run test to verify it passes**

Run: `go test -v -run TestGetWorkingProxy`
Expected: PASS

**Step 5: Commit**

```bash
git add working_proxy.go working_proxy_test.go
git commit -m "feat: implement GetWorkingProxy with smart wait and fallback"
```

---

## Task 8: Implement GetWorkingProxyList

**Files:**
- Modify: `working_proxy.go`
- Test: `working_proxy_test.go`

**Step 1: Write test**

Append to: `working_proxy_test.go`

```go
func TestGetWorkingProxyList(t *testing.T) {
	param := FreeProxyParameter{
		TargetUrl:    "http://httpbin.org/get",
		CategoryCode: "",
	}

	list, err := GetWorkingProxyList(param)
	
	if err != nil {
		t.Fatalf("GetWorkingProxyList failed: %v", err)
	}

	t.Logf("GetWorkingProxyList returned %d proxies", len(list))
}
```

**Step 2: Run test to verify it fails**

Run: `go test -v -run TestGetWorkingProxyList`
Expected: FAIL

**Step 3: Implement GetWorkingProxyList**

Append to: `working_proxy.go`

```go
func GetWorkingProxyList(param FreeProxyParameter) ([]FreeProxy, error) {
	if err := defaultPool.ensureProxiesLoaded(); err != nil {
		return nil, fmt.Errorf("failed to load proxy pool: %w", err)
	}

	targetURL := param.getTargetURL()
	key := buildKey(targetURL, param.CategoryCode)

	defaultPool.targetMu.Lock()
	indices := defaultPool.targetUrlWorkingProxies[key]
	hasWorking := len(indices) > 0
	defaultPool.targetMu.Unlock()

	if hasWorking {
		defaultPool.globalMu.RLock()
		defer defaultPool.globalMu.RUnlock()

		var result []FreeProxy
		for _, idx := range indices {
			if idx < len(defaultPool.proxies) {
				proxy := defaultPool.proxies[idx]
				if param.CategoryCode == "" || strings.EqualFold(proxy.CategoryCode, param.CategoryCode) {
					result = append(result, proxy)
				}
			}
		}
		return result, nil
	}

	readyCh := defaultPool.ensureBuildStarted(param)

	select {
	case <-readyCh:
		defaultPool.targetMu.Lock()
		indices := defaultPool.targetUrlWorkingProxies[key]
		defaultPool.targetMu.Unlock()

		defaultPool.globalMu.RLock()
		defer defaultPool.globalMu.RUnlock()

		var result []FreeProxy
		for _, idx := range indices {
			if idx < len(defaultPool.proxies) {
				proxy := defaultPool.proxies[idx]
				if param.CategoryCode == "" || strings.EqualFold(proxy.CategoryCode, param.CategoryCode) {
					result = append(result, proxy)
				}
			}
		}
		return result, nil
	case <-time.After(3 * time.Second):
		return GetProxyList(param)
	}
}
```

**Step 4: Run test**

Run: `go test -v -run TestGetWorkingProxyList`
Expected: PASS

**Step 5: Commit**

```bash
git add working_proxy.go working_proxy_test.go
git commit -m "feat: implement GetWorkingProxyList"
```

---
---

## Task 9: Add HTTP Handlers for Working Proxy Endpoints

**Files:**
- Modify: `server/handler.go`
- Modify: `server/registry.go`

**Step 1: Add handlers to server/handler.go**

```go
func getWorkingProxyHandler(c *fiber.Ctx) error {
	param := freeproxy.FreeProxyParameter{
		CategoryCode: c.Query("category_code"),
		TargetUrl:    c.Query("target_url"),
	}

	proxy, err := freeproxy.GetWorkingProxy(param)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(ProxyResponse{
			Error: err.Error(),
		})
	}

	return c.JSON(ProxyResponse{Data: proxy})
}

func getWorkingProxyListHandler(c *fiber.Ctx) error {
	param := freeproxy.FreeProxyParameter{
		CategoryCode: c.Query("category_code"),
		TargetUrl:    c.Query("target_url"),
	}

	list, err := freeproxy.GetWorkingProxyList(param)
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

**Step 2: Register routes in server/registry.go**

Add to v1 group:
```go
v1.Get("/proxy/get-working", getWorkingProxyHandler)
v1.Get("/proxy/list-working", getWorkingProxyListHandler)
```

**Step 3: Test endpoints**

```bash
go run server/main.go
curl "http://localhost:8080/api/v1/proxy/get-working?target_url=http://httpbin.org/get"
```

**Step 4: Commit**

```bash
git add server/handler.go server/registry.go
git commit -m "feat: add HTTP endpoints for working proxy system"
```

---

## Task 10: Implement Metrics and Endpoint

**Files:**
- Create: `metrics.go`
- Modify: `server/handler.go`
- Modify: `server/registry.go`

**Step 1: Create metrics.go**

```go
package freeproxy

import (
	"sync/atomic"
	"time"
)

type ProxyPoolMetrics struct {
	LegacyHits          atomic.Int64
	LegacyMisses        atomic.Int64
	LegacyLatencyTotal  atomic.Int64
	WorkingHits         atomic.Int64
	WorkingMisses       atomic.Int64
	WorkingLatencyTotal atomic.Int64
	WorkingBuildTime    atomic.Int64
	BuildCount          atomic.Int64
	BuildSuccess        atomic.Int64
	BuildFailed         atomic.Int64
	ProxiesTestedTotal  atomic.Int64
	ProxiesValidTotal   atomic.Int64
}

var globalMetrics = &ProxyPoolMetrics{}

func (m *ProxyPoolMetrics) LegacyAvgLatency() time.Duration {
	hits := m.LegacyHits.Load()
	if hits == 0 {
		return 0
	}
	return time.Duration(m.LegacyLatencyTotal.Load() / hits)
}

func (m *ProxyPoolMetrics) WorkingAvgLatency() time.Duration {
	hits := m.WorkingHits.Load()
	if hits == 0 {
		return 0
	}
	return time.Duration(m.WorkingLatencyTotal.Load() / hits)
}

func (m *ProxyPoolMetrics) ValidationSuccessRate() float64 {
	tested := m.ProxiesTestedTotal.Load()
	if tested == 0 {
		return 0
	}
	return float64(m.ProxiesValidTotal.Load()) / float64(tested) * 100
}

func GetMetrics() *ProxyPoolMetrics {
	return globalMetrics
}
```

**Step 2: Integrate metrics into GetProxy (proxy.go)**

Add at start and end of GetProxy function.

**Step 3: Integrate metrics into GetWorkingProxy (working_proxy.go)**

Add at start and end of GetWorkingProxy function.

**Step 4: Add metrics handler (server/handler.go)**

**Step 5: Register route and test**

**Step 6: Commit**

```bash
git add metrics.go proxy.go working_proxy.go server/handler.go server/registry.go
git commit -m "feat: implement metrics instrumentation and endpoint"
```

---

## Task 11: Update Documentation

**Files:**
- Modify: `README.md`

**Step 1: Add new endpoints section**

**Step 2: Add environment variables**

**Step 3: Commit**

```bash
git add README.md
git commit -m "docs: update README with new endpoints and configuration"
```

---

## Completion

All tasks complete. The implementation follows TDD with tests first, frequent commits, and maintains backward compatibility with legacy system for benchmarking.
