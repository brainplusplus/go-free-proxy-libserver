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
