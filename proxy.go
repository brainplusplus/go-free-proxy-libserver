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
	DefaultTargetURL = "https://example.com"
	DefaultTTL       = 30 * time.Minute
	maxPoolKeys      = 100
)

// CategorySources maps category codes to their scrape URLs.
var CategorySources = map[string]string{
	"EN":  "https://free-proxy-list.net/en/",
	"UK":  "https://free-proxy-list.net/en/uk-proxy.html",
	"US":  "https://free-proxy-list.net/en/us-proxy.html",
	"SSL": "https://free-proxy-list.net/en/ssl-proxy.html",
}

func init() {
	rand.Seed(time.Now().UnixNano())
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

// evictExpired removes expired keys when pool grows too large.
// Must be called with lock held.
func (pp *proxyPool) evictExpired() {
	if len(pp.proxies) <= maxPoolKeys {
		return
	}
	now := time.Now()
	for k, exp := range pp.expiry {
		if now.After(exp) {
			delete(pp.proxies, k)
			delete(pp.expiry, k)
		}
	}
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
	pp.evictExpired()
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
