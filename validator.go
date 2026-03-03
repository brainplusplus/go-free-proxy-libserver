package freeproxy

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// validationTimeout is configurable via PROXY_TIMEOUT env (in seconds, default 3s)
var validationTimeout = func() time.Duration {
	if v := os.Getenv("PROXY_TIMEOUT"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return 3 * time.Second
}()

// validateProxyCtx is the context-aware entry point used by GetProxy workers.
// When ctx is cancelled (another worker won), in-flight requests are aborted.
func validateProxyCtx(ctx context.Context, proxy *FreeProxy, targetURL string) bool {
	if isWebSocketURL(targetURL) {
		return validateWebSocket(ctx, proxy, targetURL)
	}
	return validateHTTP(ctx, proxy, targetURL)
}

// validateProxy is the context-free convenience wrapper (used in tests / library direct calls)
func validateProxy(proxy *FreeProxy, targetURL string) bool {
	return validateProxyCtx(context.Background(), proxy, targetURL)
}

func isWebSocketURL(u string) bool {
	lower := strings.ToLower(u)
	return strings.HasPrefix(lower, "ws://") || strings.HasPrefix(lower, "wss://")
}

func validateHTTP(ctx context.Context, proxy *FreeProxy, targetURL string) bool {
	proxyURL, err := url.Parse(proxy.ProxyURL())
	if err != nil {
		return false
	}

	// Combine validationTimeout and ctx so whichever fires first wins
	reqCtx, cancel := context.WithTimeout(ctx, validationTimeout)
	defer cancel()

	transport := &http.Transport{
		Proxy:             http.ProxyURL(proxyURL),
		DisableKeepAlives: true,
	}
	defer transport.CloseIdleConnections()

	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		// Do NOT set Timeout here — context handles it
	}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, targetURL, nil)
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

	return resp.StatusCode >= 200 && resp.StatusCode < 500
}

func validateWebSocket(ctx context.Context, proxy *FreeProxy, targetURL string) bool {
	proxyURL, err := url.Parse(proxy.ProxyURL())
	if err != nil {
		return false
	}

	dialer := websocket.Dialer{
		Proxy:            http.ProxyURL(proxyURL),
		HandshakeTimeout: validationTimeout,
	}

	// Wrap dial in a goroutine so we can honour ctx cancellation
	type dialResult struct {
		conn *websocket.Conn
		resp *http.Response
		err  error
	}

	ch := make(chan dialResult, 1)
	go func() {
		conn, resp, err := dialer.Dial(targetURL, http.Header{
			"Cache-Control": []string{"no-cache"},
		})
		ch <- dialResult{conn, resp, err}
	}()

	select {
	case <-ctx.Done():
		return false
	case res := <-ch:
		if res.err != nil {
			return false
		}
		defer res.conn.Close()
		if res.resp != nil && res.resp.Body != nil {
			defer res.resp.Body.Close()
		}
		return true
	}
}
