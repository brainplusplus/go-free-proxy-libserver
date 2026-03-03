package freeproxy

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const validationTimeout = 10 * time.Second

// validateProxy tests a proxy against the targetURL.
func validateProxy(proxy *FreeProxy, targetURL string) bool {
	if isWebSocketURL(targetURL) {
		return validateWebSocket(proxy, targetURL)
	}
	return validateHTTP(proxy, targetURL)
}

func isWebSocketURL(u string) bool {
	lower := strings.ToLower(u)
	return strings.HasPrefix(lower, "ws://") || strings.HasPrefix(lower, "wss://")
}

func validateHTTP(proxy *FreeProxy, targetURL string) bool {
	proxyURL, err := url.Parse(proxy.ProxyURL())
	if err != nil {
		return false
	}

	transport := &http.Transport{
		Proxy:             http.ProxyURL(proxyURL),
		DisableKeepAlives: true,
	}
	defer transport.CloseIdleConnections()

	client := &http.Client{
		Transport: transport,
		Timeout:   validationTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	req, err := http.NewRequest(http.MethodGet, targetURL, nil)
	if err != nil {
		return false
	}
	req.Header.Set("Cache-Control", "no-cache, no-store")
	req.Header.Set("Pragma", "no-cache")

	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode >= 200 && resp.StatusCode < 400
}

func validateWebSocket(proxy *FreeProxy, targetURL string) bool {
	proxyURL, err := url.Parse(proxy.ProxyURL())
	if err != nil {
		return false
	}

	dialer := websocket.Dialer{
		Proxy:            http.ProxyURL(proxyURL),
		HandshakeTimeout: validationTimeout,
	}

	headers := http.Header{}
	headers.Set("Cache-Control", "no-cache")

	conn, resp, err := dialer.Dial(targetURL, headers)
	if err != nil {
		return false
	}
	defer conn.Close()
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}

	return true
}
