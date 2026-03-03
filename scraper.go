package freeproxy

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// scrape fetches and parses the proxy table for a given category code.
func scrape(categoryCode string) ([]FreeProxy, error) {
	srcURL, ok := CategorySources[categoryCode]
	if !ok {
		return nil, fmt.Errorf("unknown category code: %s", categoryCode)
	}

	req, err := http.NewRequest(http.MethodGet, srcURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Cache-Control", "no-cache, no-store, must-revalidate")
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("Expires", "0")

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse document: %w", err)
	}

	var proxies []FreeProxy

	doc.Find("#list tr").Each(func(i int, s *goquery.Selection) {
		if i == 0 {
			return // skip header row
		}

		row := s.Children()
		if row.Length() < 8 {
			return
		}

		ip := strings.TrimSpace(row.Eq(0).Text())
		portStr := strings.TrimSpace(row.Eq(1).Text())
		countryCode := strings.TrimSpace(row.Eq(2).Text())
		countryName := strings.TrimSpace(row.Eq(3).Text())
		anonymity := strings.ToLower(strings.TrimSpace(row.Eq(4).Text()))
		googleStr := strings.ToLower(strings.TrimSpace(row.Eq(5).Text()))
		httpsStr := strings.ToLower(strings.TrimSpace(row.Eq(6).Text()))
		lastCheckedStr := strings.TrimSpace(row.Eq(7).Text())

		if ip == "" || portStr == "" {
			return
		}

		port, err := strconv.Atoi(portStr)
		if err != nil {
			return
		}

		isHTTPS := httpsStr == "yes"
		scheme := "http"
		if isHTTPS {
			scheme = "https"
		}

		isElite := strings.Contains(anonymity, "elite")
		isAnon := isElite || strings.Contains(anonymity, "anonymous")

		proxy := FreeProxy{
			Scheme:       scheme,
			IP:           ip,
			Port:         port,
			CategoryCode: categoryCode,
			CountryCode:  countryCode,
			CountryName:  countryName,
			Anonym:       isAnon,
			Elite:        isElite,
			Google:       googleStr == "yes",
			HTTPS:        isHTTPS,
			LastChecked:  parseLastChecked(lastCheckedStr),
		}
		proxy.ProxyUrl = proxy.ProxyURL()
		proxies = append(proxies, proxy)
	})

	return proxies, nil
}

// parseLastChecked converts "N minutes ago" style strings to time.Time.
func parseLastChecked(s string) time.Time {
	parts := strings.Fields(strings.ToLower(s))
	if len(parts) < 2 {
		return time.Time{}
	}

	val, err := strconv.Atoi(parts[0])
	if err != nil {
		return time.Time{}
	}

	unit := parts[1]
	now := time.Now()

	switch {
	case strings.HasPrefix(unit, "second"):
		return now.Add(-time.Duration(val) * time.Second)
	case strings.HasPrefix(unit, "minute"):
		return now.Add(-time.Duration(val) * time.Minute)
	case strings.HasPrefix(unit, "hour"):
		return now.Add(-time.Duration(val) * time.Hour)
	case strings.HasPrefix(unit, "day"):
		return now.Add(-time.Duration(val) * 24 * time.Hour)
	}

	return time.Time{}
}
