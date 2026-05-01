package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
)

type httpClient struct {
	client *http.Client
}

func newHTTPClient() *httpClient {
	jar, _ := cookiejar.New(nil)
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	return &httpClient{
		client: &http.Client{
			Timeout:   15 * time.Second,
			Jar:       jar,
			Transport: transport,
		},
	}
}

func (c *httpClient) setCookie(cookie *network.Cookie) {
	if cookie.Domain == "" || cookie.Name == "" {
		return
	}
	u, _ := url.Parse("https://" + cookie.Domain)
	if u == nil {
		return
	}
	httpCookie := &http.Cookie{
		Name:   cookie.Name,
		Value:  cookie.Value,
		Domain: cookie.Domain,
		Path:   cookie.Path,
	}
	c.client.Jar.SetCookies(u, []*http.Cookie{httpCookie})
}

func (c *httpClient) get(assetURL, referer string) ([]byte, string, error) {
	return c.getWithHeaders(assetURL, referer, "*/*", "", "", "")
}

func (c *httpClient) getCSS(assetURL, referer string, baseURL string) ([]byte, string, error) {
	secFetchDest := "style"
	secFetchMode := "no-cors"
	secFetchSite := secFetchSiteValue(assetURL, baseURL)
	return c.getWithHeaders(assetURL, referer, "text/css,*/*;q=0.1", secFetchDest, secFetchMode, secFetchSite)
}

func (c *httpClient) getWithHeaders(assetURL, referer, accept, secFetchDest, secFetchMode, secFetchSite string) ([]byte, string, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
		}

		req, err := http.NewRequest("GET", assetURL, nil)
		if err != nil {
			return nil, "", err
		}

		req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
		req.Header.Set("Accept", accept)
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")
		req.Header.Set("Referer", referer)

		if secFetchDest != "" {
			req.Header.Set("Sec-Fetch-Dest", secFetchDest)
		}
		if secFetchMode != "" {
			req.Header.Set("Sec-Fetch-Mode", secFetchMode)
		}
		if secFetchSite != "" && secFetchSite != "unknown" {
			req.Header.Set("Sec-Fetch-Site", secFetchSite)
		}
		req.Header.Set("Sec-Ch-Ua", `"Chromium";v="120", "Google Chrome";v="120"`)
		req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
		req.Header.Set("Sec-Ch-Ua-Platform", `"macOS"`)

		resp, err := c.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode == 429 || resp.StatusCode == 503 {
			resp.Body.Close()
			lastErr = fmt.Errorf("status %d", resp.StatusCode)
			continue
		}

		if resp.StatusCode != 200 {
			resp.Body.Close()
			return nil, "", fmt.Errorf("status %d", resp.StatusCode)
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, maxResourceBytes))
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}

		return body, resp.Header.Get("content-type"), nil
	}

	return nil, "", fmt.Errorf("failed after 3 attempts: %w", lastErr)
}

func secFetchSiteValue(assetURL, baseURL string) string {
	if baseURL == "" {
		return ""
	}
	asset, err1 := url.Parse(assetURL)
	base, err2 := url.Parse(baseURL)
	if err1 != nil || err2 != nil {
		return ""
	}
	assetHost := strings.ToLower(asset.Host)
	baseHost := strings.ToLower(base.Host)
	if assetHost == baseHost {
		return "same-origin"
	}
	if strings.HasSuffix(assetHost, "."+baseHost) || strings.HasSuffix(baseHost, "."+assetHost) {
		return "same-site"
	}
	return "cross-site"
}

type cookiedHTTPClient struct {
	mu         sync.Mutex
	client     *httpClient
	baseURL    string
	lastReq    time.Time
	minDelay   time.Duration
}

func newCookiedHTTPClient(cookies []*network.Cookie, baseURL string) *cookiedHTTPClient {
	c := &cookiedHTTPClient{
		client:   newHTTPClient(),
		baseURL:  baseURL,
		minDelay: 100 * time.Millisecond,
	}
	for _, cookie := range cookies {
		c.client.setCookie(cookie)
	}
	return c
}

func (c *cookiedHTTPClient) get(assetURL string) ([]byte, string, error) {
	c.mu.Lock()
	elapsed := time.Since(c.lastReq)
	if elapsed < c.minDelay {
		time.Sleep(c.minDelay - elapsed)
	}
	c.mu.Unlock()

	body, ct, err := c.client.get(assetURL, c.baseURL)

	c.mu.Lock()
	c.lastReq = time.Now()
	c.mu.Unlock()

	return body, ct, err
}

func (c *cookiedHTTPClient) getCSS(assetURL string) ([]byte, string, error) {
	c.mu.Lock()
	elapsed := time.Since(c.lastReq)
	if elapsed < c.minDelay {
		time.Sleep(c.minDelay - elapsed)
	}
	c.mu.Unlock()

	body, ct, err := c.client.getCSS(assetURL, c.baseURL, c.baseURL)

	c.mu.Lock()
	c.lastReq = time.Now()
	c.mu.Unlock()

	return body, ct, err
}
