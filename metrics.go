package freeproxy

import (
	"sync/atomic"
	"time"
)

// ProxyPoolMetrics holds performance metrics for the proxy pool
type ProxyPoolMetrics struct {
	// Legacy system metrics
	LegacyHits         atomic.Int64
	LegacyMisses       atomic.Int64
	LegacyLatencyTotal atomic.Int64 // nanoseconds

	// Working proxy system metrics
	WorkingHits         atomic.Int64
	WorkingMisses       atomic.Int64
	WorkingLatencyTotal atomic.Int64 // nanoseconds
	WorkingBuildTime    atomic.Int64 // nanoseconds

	// Build metrics
	BuildCount   atomic.Int64
	BuildSuccess atomic.Int64
	BuildFailed  atomic.Int64

	// Validation metrics
	ProxiesTestedTotal atomic.Int64
	ProxiesValidTotal  atomic.Int64
}

// globalMetrics is the singleton metrics instance
var globalMetrics = &ProxyPoolMetrics{}

// LegacyAvgLatency returns the average latency for legacy GetProxy calls
func (m *ProxyPoolMetrics) LegacyAvgLatency() time.Duration {
	hits := m.LegacyHits.Load()
	if hits == 0 {
		return 0
	}
	return time.Duration(m.LegacyLatencyTotal.Load() / hits)
}

// WorkingAvgLatency returns the average latency for GetWorkingProxy calls
func (m *ProxyPoolMetrics) WorkingAvgLatency() time.Duration {
	hits := m.WorkingHits.Load()
	if hits == 0 {
		return 0
	}
	return time.Duration(m.WorkingLatencyTotal.Load() / hits)
}

// ValidationSuccessRate returns the percentage of proxies that passed validation
func (m *ProxyPoolMetrics) ValidationSuccessRate() float64 {
	tested := m.ProxiesTestedTotal.Load()
	if tested == 0 {
		return 0
	}
	return float64(m.ProxiesValidTotal.Load()) / float64(tested) * 100
}

// GetMetrics returns the global metrics instance
func GetMetrics() *ProxyPoolMetrics {
	return globalMetrics
}
