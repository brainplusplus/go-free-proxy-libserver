package freeproxy

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"strconv"
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
	ProxyUrl     string    `json:"proxy_url"`
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
	// Add https scheme if missing
	if !strings.Contains(p.TargetUrl, "://") {
		return "https://" + p.TargetUrl
	}
	return p.TargetUrl
}

// numWorkers returns concurrent worker count:
//   - categoryCode set   → 2 workers
//   - categoryCode empty → 2 × number of categories (e.g. 2×4=8)
func numWorkers(categoryCode string) int {
	if categoryCode != "" {
		return 2
	}
	return 2 * len(CategorySources)
}

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

func SetTTL(d time.Duration) {
	defaultPool.globalMu.Lock()
	defer defaultPool.globalMu.Unlock()
	defaultPool.ttl = d
}

// ensureProxiesLoaded ensures global proxies are loaded and not expired
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

// resetAll clears all state for fresh rebuild
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

// GetProxy validates proxies concurrently and returns the first working one.
//
// Workers: 2 per category (if categoryCode empty → 2×4=8 workers, else 2).
// First valid proxy wins → context cancels remaining workers immediately.
// All tested proxies are removed from pool regardless of validity, so the
// next call receives only untested proxies.
func GetProxy(param FreeProxyParameter) (*FreeProxy, error) {
	start := time.Now()
	defer func() {
		globalMetrics.LegacyLatencyTotal.Add(int64(time.Since(start)))
	}()

	targetURL := param.getTargetURL()

	if err := defaultPool.ensureProxiesLoaded(); err != nil {
		globalMetrics.LegacyMisses.Add(1)
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

	// Buffer of 1 — only the first winner is accepted
	winnerCh := make(chan *FreeProxy, 1)

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				// Respect cancellation before picking a new proxy
				select {
				case <-ctx.Done():
					return
				default:
				}

				proxy, ok := defaultPool.pickRandom(key, param.CategoryCode)
				if !ok {
					return // pool empty for this worker
				}

				// Log worker activity
				slog.Info("testing proxy", "worker", id, "ip", proxy.IP, "port", proxy.Port, "target_url", targetURL)

				// Pass ctx so in-flight HTTP/WS requests cancel when winner found
				if validateProxyCtx(ctx, proxy, targetURL) {
					slog.Info("found working proxy", "worker", id, "ip", proxy.IP, "port", proxy.Port)
					select {
					case winnerCh <- proxy:
						cancel() // signal all other workers to stop
					default:
						// Another worker already won; this proxy is already removed
						// from pool — it gets discarded (acceptable for GetProxy)
					}
					return
				}
			}
		}(i)
	}

	// Close done channel when all workers finish
	doneCh := make(chan struct{})
	go func() {
		wg.Wait()
		close(doneCh)
	}()

	select {
	case proxy := <-winnerCh:
		// Drain remaining workers (they're already stopping via ctx)
		<-doneCh
		globalMetrics.LegacyHits.Add(1)
		return proxy, nil
	case <-doneCh:
		// All workers done — check winner channel one last time to avoid
		// a race where winner sent after doneCh closed
		select {
		case proxy := <-winnerCh:
			globalMetrics.LegacyHits.Add(1)
			return proxy, nil
		default:
		}
		globalMetrics.LegacyMisses.Add(1)
		return nil, fmt.Errorf("no working proxy found (all %d workers exhausted)", n)
	}
}

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
