package freeproxy

import (
	"os"
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

func TestGetWorkingProxyWorkers(t *testing.T) {
	// Test default value
	workers := getWorkingProxyWorkers()
	if workers != 50 {
		t.Errorf("expected default 50 workers, got %d", workers)
	}

	// Test with env var set
	os.Setenv("WORKING_PROXY_WORKERS", "100")
	defer os.Unsetenv("WORKING_PROXY_WORKERS")

	workers = getWorkingProxyWorkers()
	if workers != 100 {
		t.Errorf("expected 100 workers from env, got %d", workers)
	}
}
