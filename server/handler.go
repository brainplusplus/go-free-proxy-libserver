package main

import (
	"time"

	"github.com/gofiber/fiber/v2"

	freeproxy "github.com/brainplusplus/go-free-proxy-libserver"
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

// MetricsResponse wraps metrics data
type MetricsResponse struct {
	Legacy struct {
		Hits         int64   `json:"hits"`
		Misses       int64   `json:"misses"`
		AvgLatencyMs float64 `json:"avg_latency_ms"`
		SuccessRate  float64 `json:"success_rate"`
	} `json:"legacy"`
	Working struct {
		Hits           int64   `json:"hits"`
		Misses         int64   `json:"misses"`
		AvgLatencyMs   float64 `json:"avg_latency_ms"`
		SuccessRate    float64 `json:"success_rate"`
		AvgBuildTimeMs float64 `json:"avg_build_time_ms"`
	} `json:"working"`
	Validation struct {
		TotalTested int64   `json:"total_tested"`
		TotalValid  int64   `json:"total_valid"`
		SuccessRate float64 `json:"success_rate"`
	} `json:"validation"`
}

func getMetricsHandler(c *fiber.Ctx) error {
	m := freeproxy.GetMetrics()

	resp := MetricsResponse{}

	resp.Legacy.Hits = m.LegacyHits.Load()
	resp.Legacy.Misses = m.LegacyMisses.Load()
	resp.Legacy.AvgLatencyMs = float64(m.LegacyAvgLatency().Milliseconds())
	if total := resp.Legacy.Hits + resp.Legacy.Misses; total > 0 {
		resp.Legacy.SuccessRate = float64(resp.Legacy.Hits) / float64(total) * 100
	}

	resp.Working.Hits = m.WorkingHits.Load()
	resp.Working.Misses = m.WorkingMisses.Load()
	resp.Working.AvgLatencyMs = float64(m.WorkingAvgLatency().Milliseconds())
	if total := resp.Working.Hits + resp.Working.Misses; total > 0 {
		resp.Working.SuccessRate = float64(resp.Working.Hits) / float64(total) * 100
	}
	buildCount := m.BuildCount.Load()
	if buildCount > 0 {
		resp.Working.AvgBuildTimeMs = float64(m.WorkingBuildTime.Load()/buildCount) / float64(time.Millisecond)
	}

	resp.Validation.TotalTested = m.ProxiesTestedTotal.Load()
	resp.Validation.TotalValid = m.ProxiesValidTotal.Load()
	resp.Validation.SuccessRate = m.ValidationSuccessRate()

	return c.JSON(resp)
}
