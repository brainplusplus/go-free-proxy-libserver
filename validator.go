package freeproxy

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
	"github.com/bogdanfinn/websocket"
)

// getValidationTimeout reads PROXY_TIMEOUT env (in seconds, default 3s)
// Called at runtime so it works with .env files loaded in main()
func getValidationTimeout() time.Duration {
	if v := os.Getenv("PROXY_TIMEOUT"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return 3 * time.Second
}

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

// createTLSClient creates a TLS client configured to mimic Chrome 131 browser.
// If proxyURL is provided, routes traffic through the proxy.
func createTLSClient(timeout time.Duration, proxyURL string) (tls_client.HttpClient, error) {
	jar := tls_client.NewCookieJar()
	options := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(int(timeout.Seconds())),
		tls_client.WithClientProfile(profiles.Chrome_131),
		tls_client.WithNotFollowRedirects(),
		tls_client.WithCookieJar(jar),
	}

	if proxyURL != "" {
		proxyURL = strings.Replace(proxyURL, "https", "http", -1)
		options = append(options, tls_client.WithProxyUrl(proxyURL))
	}

	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), options...)
	if err != nil {
		return nil, fmt.Errorf("failed to create TLS client: %w", err)
	}

	return client, nil
}

func validateHTTP(ctx context.Context, proxy *FreeProxy, targetURL string) bool {
	slog.Info("testing HTTP proxy", "proxy_url", proxy.ProxyURL(), "target_url", targetURL)

	timeout := getValidationTimeout()

	// Create TLS client with proxy configuration
	client, err := createTLSClient(timeout, proxy.ProxyURL())
	if err != nil {
		slog.Info("proxy test failed", "proxy_url", proxy.ProxyURL(), "error", "failed to create TLS client", "Anonym", proxy.Anonym, "Elite", proxy.Elite, "Google", proxy.Google, "HTTPS", proxy.HTTPS)
		return false
	}

	// Create request with context for cancellation
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		slog.Info("proxy test failed", "proxy_url", proxy.ProxyURL(), "error", "failed to create request", "Anonym", proxy.Anonym, "Elite", proxy.Elite, "Google", proxy.Google, "HTTPS", proxy.HTTPS)
		return false
	}
	req.Header.Set("Cache-Control", "no-cache, no-store, must-revalidate")
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("Expires", "0")
	req.Header.Set("accept", "*/*")
	req.Header.Set("user-agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36")

	// Execute request
	resp, err := client.Do(req)
	if err != nil {
		slog.Info("proxy test failed", "proxy_url", proxy.ProxyURL(), "target_url", targetURL, "error", err.Error(), "Anonym", proxy.Anonym, "Elite", proxy.Elite, "Google", proxy.Google, "HTTPS", proxy.HTTPS)
		return false
	}
	defer resp.Body.Close()

	success := resp.StatusCode >= 200 && resp.StatusCode < 500
	if success {
		slog.Info("proxy test success", "proxy_url", proxy.ProxyURL(), "target_url", targetURL, "status_code", resp.StatusCode, "Anonym", proxy.Anonym, "Elite", proxy.Elite, "Google", proxy.Google, "HTTPS", proxy.HTTPS)
	} else {
		slog.Info("proxy test failed", "proxy_url", proxy.ProxyURL(), "target_url", targetURL, "status_code", resp.StatusCode, "Anonym", proxy.Anonym, "Elite", proxy.Elite, "Google", proxy.Google, "HTTPS", proxy.HTTPS)
	}
	return success
}

func validateWebSocket(ctx context.Context, proxy *FreeProxy, targetURL string) bool {
	slog.Info("testing WebSocket proxy", "proxy_url", proxy.ProxyURL(), "target_url", targetURL)
	proxyURLString := proxy.ProxyURL()
	proxyURLString = strings.Replace(proxyURLString, "https", "http", -1)
	proxyURL, err := url.Parse(proxyURLString)
	if err != nil {
		slog.Info("proxy test failed", "proxy_url", proxy.ProxyURL(), "error", "invalid proxy URL", "Anonym", proxy.Anonym, "Elite", proxy.Elite, "Google", proxy.Google, "HTTPS", proxy.HTTPS)
		return false
	}

	dialer := websocket.Dialer{
		Proxy:            http.ProxyURL(proxyURL),
		HandshakeTimeout: getValidationTimeout(),
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
			"Cache-Control": []string{"no-cache", "no-store", "must-revalidate"},
			"Pragma":        []string{"no-cache"},
			"Expires":       []string{"0"},
			"User-Agent":    []string{"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36"},
		})
		ch <- dialResult{conn, resp, err}
	}()

	select {
	case <-ctx.Done():
		slog.Info("proxy test cancelled", "proxy_url", proxy.ProxyURL(), "target_url", targetURL, "Anonym", proxy.Anonym, "Elite", proxy.Elite, "Google", proxy.Google, "HTTPS", proxy.HTTPS)
		return false
	case res := <-ch:
		if res.err != nil {
			slog.Info("proxy test failed", "proxy_url", proxy.ProxyURL(), "target_url", targetURL, "error", res.err.Error(), "Anonym", proxy.Anonym, "Elite", proxy.Elite, "Google", proxy.Google, "HTTPS", proxy.HTTPS)
			return false
		}
		defer res.conn.Close()
		if res.resp != nil && res.resp.Body != nil {
			defer res.resp.Body.Close()
		}
		slog.Info("proxy test success", "proxy_url", proxy.ProxyURL(), "target_url", targetURL, "Anonym", proxy.Anonym, "Elite", proxy.Elite, "Google", proxy.Google, "HTTPS", proxy.HTTPS)
		return true
	}
}
