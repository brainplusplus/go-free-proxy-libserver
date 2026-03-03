package freeproxy

import (
	"context"
	"fmt"
	"log/slog"
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

type proxyPool struct {
	mu      sync.Mutex
	proxies map[string][]FreeProxy
	expiry  map[string]time.Time
	ttl     time.Duration
	sf      singleflight.Group
}

var defaultPool = &proxyPool{
	proxies: make(map[string][]FreeProxy),
	expiry:  make(map[string]time.Time),
	ttl:     DefaultTTL,
}

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

func (pp *proxyPool) ensureLoaded(key string) error {
	pp.mu.Lock()
	needed := pp.needsRefresh(key)
	pp.mu.Unlock()

	if !needed {
		return nil
	}

	_, err, _ := pp.sf.Do(key, func() (interface{}, error) {
		pp.mu.Lock()
		if !pp.needsRefresh(key) {
			pp.mu.Unlock()
			return nil, nil
		}
		pp.mu.Unlock()

		// Scrape all 4 categories concurrently
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

		// Shuffle so concurrent workers pick diverse proxies
		rand.Shuffle(len(all), func(i, j int) { all[i], all[j] = all[j], all[i] })

		pp.mu.Lock()
		pp.proxies[key] = all
		pp.expiry[key] = time.Now().Add(pp.ttl)
		pp.mu.Unlock()

		return nil, nil
	})

	return err
}

// pickRandom picks a random matching proxy and removes it from the pool (O(1) swap-remove).
// Returns false if no matching proxy is available.
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

	idx := indices[rand.Intn(len(indices))]
	proxy := list[idx]

	// O(1) swap-remove
	list[idx] = list[len(list)-1]
	pp.proxies[key] = list[:len(list)-1]

	return &proxy, true
}

func (pp *proxyPool) getAll(key, categoryCode string) []FreeProxy {
	pp.mu.Lock()
	defer pp.mu.Unlock()

	var result []FreeProxy
	for _, p := range pp.proxies[key] {
		if categoryCode == "" || strings.EqualFold(p.CategoryCode, categoryCode) {
			result = append(result, p)
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
	targetURL := param.getTargetURL()

	if err := defaultPool.ensureLoaded(targetURL); err != nil {
		return nil, fmt.Errorf("failed to load proxy pool: %w", err)
	}

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

				proxy, ok := defaultPool.pickRandom(targetURL, param.CategoryCode)
				if !ok {
					return // pool empty for this worker
				}

				// DEBUG: Log worker activity
				slog.Debug("testing proxy", "worker", id, "ip", proxy.IP, "port", proxy.Port)

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
		return proxy, nil
	case <-doneCh:
		// All workers done — check winner channel one last time to avoid
		// a race where winner sent after doneCh closed
		select {
		case proxy := <-winnerCh:
			return proxy, nil
		default:
		}
		return nil, fmt.Errorf("no working proxy found (all %d workers exhausted)", n)
	}
}

// GetProxyList returns a snapshot of the current pool (not validated).
func GetProxyList(param FreeProxyParameter) ([]FreeProxy, error) {
	targetURL := param.getTargetURL()

	if err := defaultPool.ensureLoaded(targetURL); err != nil {
		return nil, fmt.Errorf("failed to load proxy pool: %w", err)
	}

	return defaultPool.getAll(targetURL, param.CategoryCode), nil
}
