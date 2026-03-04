package freeproxy

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
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
	start := time.Now()
	globalMetrics.BuildCount.Add(1)

	workers := getWorkingProxyWorkers()
	candidates := pp.filterByCategory(categoryCode)

	if len(candidates) == 0 {
		slog.Info("no proxies to validate", "target_url", targetURL, "category_code", categoryCode)
		globalMetrics.BuildFailed.Add(1)
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

	// Update metrics
	globalMetrics.ProxiesTestedTotal.Add(int64(len(candidates)))
	globalMetrics.ProxiesValidTotal.Add(int64(validCount))
	globalMetrics.WorkingBuildTime.Add(int64(time.Since(start)))

	if validCount > 0 {
		globalMetrics.BuildSuccess.Add(1)
	} else {
		globalMetrics.BuildFailed.Add(1)
	}

	slog.Info("working proxy validation complete", "target_url", targetURL, "valid_count", validCount)
}

// nextWorkingIndex returns the next working proxy index using round-robin sequence
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

// ensureBuildStarted ensures working proxy build is started for the given target
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

// GetWorkingProxy returns a pre-validated proxy using sequence (round-robin)
func GetWorkingProxy(param FreeProxyParameter) (*FreeProxy, error) {
	start := time.Now()
	defer func() {
		globalMetrics.WorkingLatencyTotal.Add(int64(time.Since(start)))
	}()

	if err := defaultPool.ensureProxiesLoaded(); err != nil {
		globalMetrics.WorkingMisses.Add(1)
		return nil, fmt.Errorf("failed to load proxy pool: %w", err)
	}

	targetURL := param.getTargetURL()
	key := buildKey(targetURL, param.CategoryCode)

	if idx, ok := defaultPool.nextWorkingIndex(key, param.CategoryCode); ok {
		defaultPool.globalMu.RLock()
		proxy := defaultPool.proxies[idx]
		defaultPool.globalMu.RUnlock()
		globalMetrics.WorkingHits.Add(1)
		return &proxy, nil
	}

	readyCh := defaultPool.ensureBuildStarted(param)

	select {
	case <-readyCh:
		if idx, ok := defaultPool.nextWorkingIndex(key, param.CategoryCode); ok {
			defaultPool.globalMu.RLock()
			proxy := defaultPool.proxies[idx]
			defaultPool.globalMu.RUnlock()
			globalMetrics.WorkingHits.Add(1)
			return &proxy, nil
		}
		globalMetrics.WorkingMisses.Add(1)
		return nil, fmt.Errorf("no working proxy available")
	case <-time.After(3 * time.Second):
		globalMetrics.WorkingMisses.Add(1)
		return GetProxy(param)
	}
}

// GetWorkingProxyList returns all pre-validated proxies for target
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
