# Proxy Pool Refactoring Design

**Date**: 2026-03-04
**Status**: Approved
**Author**: Design Session with User

## Executive Summary

Refactor `proxyPool` structure to optimize memory usage and introduce a new "working proxy" system that pre-validates proxies for faster response times. The new system maintains backward compatibility with legacy APIs while enabling performance benchmarking between old and new approaches.

## Goals

1. **Memory Optimization**: Share global proxy list across all target URLs instead of duplicating per target
2. **Performance**: Pre-validate proxies concurrently and serve from validated pool (sequence-based)
3. **Benchmarking**: Maintain dual systems (legacy + new) to compare performance metrics
4. **Backward Compatibility**: Keep existing `GetProxy`/`GetProxyList` APIs unchanged

## Non-Goals

- Removing legacy system (needed for benchmarking)
- Real-time health checks per request (expiry-based refresh is sufficient)
- Complex retry logic (client-side responsibility)

---

## Architecture

### 1. Data Structure

```go
type proxyPool struct {
    // Global proxy storage (read-heavy, use RWMutex)
    globalMu sync.RWMutex
    proxies  []FreeProxy
    expiry   time.Time
    ttl      time.Duration

    // Target-specific data (write-heavy, use Mutex)
    targetMu sync.Mutex
    targetUrlProxies         map[string][]int           // Legacy: pop-based indices
    targetUrlWorkingProxies  map[string][]int           // New: validated indices
    targetUrlWorkingIndex    map[string]int             // Sequence tracker
    workingState             map[string]*workingState   // Build state + channel
    buildVersion             int64                      // Prevent cross-cycle contamination

    sf singleflight.Group
}

type workingState struct {
    building bool
    readyCh  chan struct{}  // Closed when first proxy validated
}
```

### Memory Optimization

**Before**: `map[string][]FreeProxy` - duplicated proxy objects per target URL

**After**:
- Global `[]FreeProxy` - single source of truth
- `map[string][]int` - only store indices (4-8 bytes each)
- **Savings**: ~95% memory reduction for proxy references when multiple targets share same pool

**Example**:
- 10,000 proxies × 20 target URLs
- Old: 20 × 10,000 × struct_size
- New: 10,000 × struct_size + 20 × 10,000 × 8 bytes

---

## 2. Core Operations

### GetWorkingProxy Flow (New System)

```go
func GetWorkingProxy(param FreeProxyParameter) (*FreeProxy, error) {
    // Ensure proxies loaded
    if err := ensureProxiesLoaded(); err != nil {
        return nil, err
    }

    key := buildKey(param.TargetUrl, param.CategoryCode)

    // Fast path: working proxies already available
    if idx, ok := nextWorkingIndex(key, param.CategoryCode); ok {
        return &proxies[idx], nil
    }

    // Slow path: trigger build if not started
    readyCh := ensureBuildStarted(key, param)

    // Smart wait with timeout
    select {
    case <-readyCh:
        // At least 1 working proxy found
        if idx, ok := nextWorkingIndex(key, param.CategoryCode); ok {
            return &proxies[idx], nil
        }
        return nil, errors.New("no working proxy available")
    case <-time.After(3 * time.Second):
        // Timeout: fallback to legacy GetProxy
        return GetProxy(param)
    }
}
```

### Concurrent Validation (50 Workers Default)

```go
func buildWorkingProxies(targetUrl, categoryCode string, buildVer int64) {
    workers := getWorkingProxyWorkers() // ENV: WORKING_PROXY_WORKERS=50

    // Get snapshot of proxies
    globalMu.RLock()
    localProxies := make([]FreeProxy, len(proxies))
    copy(localProxies, proxies)
    globalMu.RUnlock()

    // Filter by category if needed
    candidates := filterByCategory(localProxies, categoryCode)

    jobs := make(chan int, len(candidates))
    results := make(chan int, len(candidates))

    var wg sync.WaitGroup

    // Start workers
    for w := 0; w < workers; w++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for idx := range jobs {
                if validateProxyCtx(ctx, &localProxies[idx], targetUrl) {
                    results <- idx
                }
            }
        }()
    }

    // Send jobs
    for _, idx := range candidates {
        jobs <- idx
    }
    close(jobs)

    // Close results when all workers done
    go func() {
        wg.Wait()
        close(results)
    }()

    // Collect results
    firstFound := false
    for idx := range results {
        // Version guard: discard if expired during build
        targetMu.Lock()
        if buildVersion != buildVer {
            targetMu.Unlock()
            return // Build cycle expired, discard results
        }

        targetUrlWorkingProxies[key] = append(targetUrlWorkingProxies[key], idx)

        if !firstFound {
            workingState[key].signalReady()
            firstFound = true
        }
        targetMu.Unlock()
    }
}
```

### Sequence Logic (Round-Robin)

```go
func nextWorkingIndex(key string, categoryCode string) (int, bool) {
    targetMu.Lock()
    defer targetMu.Unlock()

    list := targetUrlWorkingProxies[key]
    if len(list) == 0 {
        return 0, false
    }

    // Filter by category on-the-fly
    idx := targetUrlWorkingIndex[key]
    attempts := 0

    for attempts < len(list) {
        proxyIndex := list[idx % len(list)]
        idx = (idx + 1) % len(list)

        // Check category match
        globalMu.RLock()
        proxy := proxies[proxyIndex]
        globalMu.RUnlock()

        if categoryCode == "" || proxy.CategoryCode == categoryCode {
            targetUrlWorkingIndex[key] = idx
            return proxyIndex, true
        }

        attempts++
    }

    return 0, false
}
```

---

## 3. Concurrency Safety

### Lock Ordering Rule (CRITICAL)

**ALWAYS follow this order to prevent deadlock:**

```
globalMu → targetMu (NEVER reverse)
```

**Safe patterns:**
- ✅ Lock `globalMu` only
- ✅ Lock `targetMu` only
- ✅ Lock `globalMu`, then `targetMu`
- ❌ Lock `targetMu`, then `globalMu` (DEADLOCK RISK)

### Build Versioning (Prevent Cross-Cycle Contamination)

**Problem**: Expiry during build can cause old workers to write to new state

**Solution**: Version guard

```go
// On reset
p.targetMu.Lock()
p.buildVersion++
p.targetMu.Unlock()

// In worker
localVersion := p.buildVersion

// Before writing results
p.targetMu.Lock()
if p.buildVersion != localVersion {
    p.targetMu.Unlock()
    return // Discard results from old build cycle
}
// ... append results
p.targetMu.Unlock()
```

### Channel Lifecycle Management

```go
type workingState struct {
    building bool
    readyCh  chan struct{}
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
        close(ws.readyCh) // Close only once
    }
}
```

**Critical rules:**
- ✅ Create fresh channel per build cycle
- ✅ Close channel only once via `signalReady()`
- ✅ Full re-init on expiry (no channel reuse)
- ❌ Never close same channel twice (panic)

---

## 4. Expiry & Rebuild Strategy

### Expiry Detection

```go
func (p *proxyPool) ensureProxiesLoaded() error {
    // Atomic check + load
    p.globalMu.RLock()
    if time.Now().Before(p.expiry) {
        p.globalMu.RUnlock()
        return nil // Still valid
    }
    p.globalMu.RUnlock()

    // Singleflight: only one goroutine scrapes
    _, err, _ := p.sf.Do("scrape", func() (interface{}, error) {
        p.resetAll()

        proxies, err := scrapeProxies()
        if err != nil {
            return nil, err
        }

        p.globalMu.Lock()
        p.proxies = proxies
        p.expiry = time.Now().Add(p.ttl)
        p.globalMu.Unlock()

        return nil, nil
    })

    return err
}
```

### Reset All State

```go
func (p *proxyPool) resetAll() {
    // Step 1: Lock global and clear
    p.globalMu.Lock()
    p.proxies = nil
    p.expiry = time.Time{}
    p.globalMu.Unlock()

    // Step 2: Lock target and full re-init
    p.targetMu.Lock()
    p.targetUrlProxies = make(map[string][]int)
    p.targetUrlWorkingProxies = make(map[string][]int)
    p.targetUrlWorkingIndex = make(map[string]int)
    p.workingState = make(map[string]*workingState)
    p.buildVersion++ // Invalidate old builds
    p.targetMu.Unlock()
}
```

### Expiry During Build Scenario

**Scenario**: Working proxies being built, expiry occurs

**Behavior:**
1. Build in progress continues (not cancelled)
2. New request triggers reset + new build
3. Old build results discarded via version guard
4. No memory leak, no channel leak

**Why safe:**
- Fresh maps created on reset
- Old goroutines check `buildVersion` before writing
- Version mismatch → discard results
- No shared state between old/new build cycles

---

## 5. API Design

### New Public Functions

```go
// GetWorkingProxy returns a pre-validated proxy using sequence (round-robin)
func GetWorkingProxy(param FreeProxyParameter) (*FreeProxy, error)

// GetWorkingProxyList returns all pre-validated proxies for target
func GetWorkingProxyList(param FreeProxyParameter) ([]FreeProxy, error)
```

### New HTTP Endpoints

```
GET /api/v1/proxy/get-working?target_url=<url>&category_code=<code>
GET /api/v1/proxy/list-working?target_url=<url>&category_code=<code>
```

### Legacy APIs (Unchanged)

```go
func GetProxy(param FreeProxyParameter) (*FreeProxy, error)
func GetProxyList(param FreeProxyParameter) ([]FreeProxy, error)
```

```
GET /api/v1/proxy/get?target_url=<url>&category_code=<code>
GET /api/v1/proxy/list?target_url=<url>&category_code=<code>
```

---

## 6. Edge Cases

### Edge Case 1: Working List Empty During Build

**Scenario**: `GetWorkingProxy` called, `targetUrlWorkingProxies` still empty

**Solution**: Smart wait with timeout
- Wait up to 3 seconds for first proxy
- If timeout → fallback to legacy `GetProxy`
- Channel-based signaling (no polling)

### Edge Case 2: Proxy Dies After Validation

**Scenario**: Proxy validated as working, but fails when client uses it

**Solution**: Lazy removal (hybrid approach)
- Accept as-is (expiry-based refresh handles this)
- Optional: Client reports failure → remove from working list
- Client-side retry: skip to next proxy in sequence

---

## 7. Testing & Benchmarking

### Metrics Instrumentation

```go
type ProxyPoolMetrics struct {
    // Legacy system
    LegacyHits          atomic.Int64
    LegacyMisses        atomic.Int64
    LegacyLatencyTotal  atomic.Int64  // nanoseconds

    // Working proxy system
    WorkingHits         atomic.Int64
    WorkingMisses       atomic.Int64
    WorkingLatencyTotal atomic.Int64
    WorkingBuildTime    atomic.Int64

    // Build metrics
    BuildCount          atomic.Int64
    BuildSuccess        atomic.Int64
    BuildFailed         atomic.Int64

    // Validation metrics
    ProxiesTestedTotal  atomic.Int64
    ProxiesValidTotal   atomic.Int64
}
```

### Benchmark Comparison Points

| Metric | Legacy | Working |
|--------|--------|---------|
| **Success Rate** | Pop-based, test on-demand | Pre-validated, sequence-based |
| **Latency** | Includes validation time | Near-zero after first build |
| **Memory** | `map[string][]FreeProxy` | `[]FreeProxy + map[string][]int` |
| **Stability** | Race to first valid | Round-robin over validated pool |

### Test Scenarios

1. **Cold start** - First request, no cache
2. **Warm cache** - Subsequent requests, working list ready
3. **High concurrency** - 100+ concurrent requests
4. **Expiry during load** - Reset while serving requests
5. **Low valid proxy rate** - Only 5% proxies working
6. **Category filtering** - Different categories per target

### Metrics API

```
GET /api/v1/metrics

Response:
{
    "legacy": {
        "hits": 1000,
        "misses": 50,
        "avg_latency_ms": 450,
        "success_rate": 95.2
    },
    "working": {
        "hits": 2000,
        "misses": 10,
        "avg_latency_ms": 5,
        "success_rate": 99.5,
        "avg_build_time_ms": 1200
    },
    "validation": {
        "total_tested": 5000,
        "total_valid": 450,
        "success_rate": 9.0
    }
}
```

---

## 8. Configuration

### Environment Variables

```bash
# Existing
PROXY_TIMEOUT=3  # Validation timeout in seconds

# New
WORKING_PROXY_WORKERS=50  # Concurrent validation workers (default: 50)
```

---

## 9. Migration Strategy

### Phase 1: Implementation
- Implement new structure with dual systems
- Add metrics instrumentation
- Deploy with both APIs available

### Phase 2: Benchmarking
- Run production traffic through both systems
- Collect metrics for 1-2 weeks
- Analyze success rate, latency, memory usage

### Phase 3: Decision
- If working system significantly better → soft replace legacy
- If comparable → keep both for different use cases
- If worse → investigate and iterate

---

## 10. Performance Expectations

### Typical Scenario
- 300 proxies in pool
- 10% working rate (30 valid proxies)
- 50 concurrent workers

**Expected timings:**
- First proxy found: < 500ms
- Full validation: 1-2 seconds
- Subsequent requests: < 5ms (sequence lookup)

### Worst Case
- 1000 proxies
- 1% working rate (10 valid proxies)
- Timeout: 3 seconds

**Behavior:**
- Smart wait ensures at least 1 proxy attempt
- Fallback to legacy prevents complete failure
- Background build continues for future requests

---

## 11. Security Considerations

- No new security risks introduced
- Existing validation logic unchanged
- Metrics do not expose sensitive proxy data
- Rate limiting handled at HTTP layer (unchanged)

---

## 12. Rollback Plan

If issues arise:
1. Disable new endpoints via feature flag
2. Metrics remain for analysis
3. Legacy system continues unchanged
4. No data migration needed (stateless)

---

## Conclusion

This design provides a production-grade concurrent proxy pool with:
- ✅ Significant memory optimization
- ✅ Near-zero latency for validated proxies
- ✅ Robust concurrency safety
- ✅ Comprehensive benchmarking capability
- ✅ Backward compatibility
- ✅ Clear migration path

The dual-system approach enables data-driven decision making while maintaining system stability.
