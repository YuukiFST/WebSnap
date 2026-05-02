package main

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"html"
	"mime"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

const maxResourceBytes = 8 * 1024 * 1024

type networkResource struct {
	body        []byte
	contentType string
}

type WebsiteDownloader struct {
	url                string
	outputDir          string
	assetsDir          string
	resourceCache      map[string]string
	resourceCacheMu    sync.Mutex
	networkResources   map[string]*networkResource
	networkResourcesMu sync.RWMutex
	baseURL            string
	logCallback        func(string)
	httpClient         *cookiedHTTPClient
	pendingReqs        map[network.RequestID]*pendingRequest
}

type pendingRequest struct {
	url         string
	mimeType    string
	body        []byte // stored early for main document
}

func NewWebsiteDownloader(targetURL, outputDir string, logFn func(string)) *WebsiteDownloader {
	return &WebsiteDownloader{
		url:              targetURL,
		outputDir:        outputDir,
		assetsDir:        outputDir + "/assets",
		resourceCache:    make(map[string]string),
		networkResources: make(map[string]*networkResource),
		logCallback:      logFn,
		pendingReqs:      make(map[network.RequestID]*pendingRequest),
	}
}

func (d *WebsiteDownloader) log(msg string) {
	d.logCallback(msg)
}

func (d *WebsiteDownloader) getExtension(assetURL, contentType string) string {
	parsed, err := url.Parse(assetURL)
	if err != nil {
		return ""
	}
	ext := path.Ext(parsed.Path)
	if ext != "" && len(ext) <= 6 {
		return ext
	}
	if contentType != "" {
		parts := strings.SplitN(contentType, ";", 2)
		if guessed, err := mime.ExtensionsByType(parts[0]); err == nil && len(guessed) > 0 {
			return guessed[0]
		}
	}
	return ""
}

func (d *WebsiteDownloader) generateFilename(assetURL, contentType string) string {
	ext := d.getExtension(assetURL, contentType)
	h := md5.Sum([]byte(assetURL))
	hash := fmt.Sprintf("%x", h)[:12]

	parsed, _ := url.Parse(assetURL)
	name := path.Base(parsed.Path)
	if name != "" {
		name = regexp.MustCompile(`[^a-zA-Z0-9_-]`).ReplaceAllString(
			strings.SplitN(name, ".", 2)[0], "_")
		if len(name) > 30 {
			name = name[:30]
		}
	} else {
		name = "resource"
	}
	return fmt.Sprintf("%s_%s%s", name, hash, ext)
}

func (d *WebsiteDownloader) saveResource(assetURL string, content []byte, contentType string) string {
	d.resourceCacheMu.Lock()
	if cached, ok := d.resourceCache[assetURL]; ok {
		d.resourceCacheMu.Unlock()
		return cached
	}
	d.resourceCacheMu.Unlock()

	if len(content) == 0 {
		return ""
	}

	filename := d.generateFilename(assetURL, contentType)
	filepath := d.assetsDir + "/" + filename
	if err := os.WriteFile(filepath, content, 0644); err != nil {
		return ""
	}

	relPath := "assets/" + filename
	d.resourceCacheMu.Lock()
	if _, exists := d.resourceCache[assetURL]; !exists {
		d.resourceCache[assetURL] = relPath
	}
	d.resourceCacheMu.Unlock()
	return relPath
}

func (d *WebsiteDownloader) flushResources() {
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)

	type resource struct {
		url         string
		body        []byte
		contentType string
	}
	d.networkResourcesMu.RLock()
	resources := make([]resource, 0, len(d.networkResources))
	for u, r := range d.networkResources {
		resources = append(resources, resource{u, r.body, r.contentType})
	}
	d.networkResourcesMu.RUnlock()

	for _, r := range resources {
		wg.Add(1)
		sem <- struct{}{}
		go func(url string, body []byte, ct string) {
			defer wg.Done()
			defer func() { <-sem }()
			d.saveResource(url, body, ct)
		}(r.url, r.body, r.contentType)
	}

	wg.Wait()
	d.log(fmt.Sprintf("> Saved %d assets in parallel", len(resources)))
}

func (d *WebsiteDownloader) getResource(assetURL, base string) string {
	if assetURL == "" || strings.HasPrefix(assetURL, "data:") ||
		strings.HasPrefix(assetURL, "blob:") || strings.HasPrefix(assetURL, "#") {
		return assetURL
	}

	absURL := d.absolutize(assetURL, base)

	if cached, ok := d.resourceCache[absURL]; ok {
		return cached
	}

	d.networkResourcesMu.RLock()
	res, ok := d.networkResources[absURL]
	d.networkResourcesMu.RUnlock()
	if ok {
		return d.saveResource(absURL, res.body, res.contentType)
	}

	if d.httpClient != nil {
		if body, ct, err := d.httpClient.get(absURL); err == nil && len(body) > 0 {
			return d.saveResource(absURL, body, ct)
		}
	}

	return assetURL
}

func (d *WebsiteDownloader) absolutize(assetURL, base string) string {
	if base == "" {
		base = d.baseURL
	}
	parsedBase, err := url.Parse(base)
	if err != nil {
		return assetURL
	}
	parsed, err := url.Parse(assetURL)
	if err != nil {
		return assetURL
	}
	return parsedBase.ResolveReference(parsed).String()
}

func (d *WebsiteDownloader) Process(allocCtx context.Context) error {
	os.RemoveAll(d.outputDir)
	os.MkdirAll(d.assetsDir, 0755)

	ctx := allocCtx

	d.setupNetworkCapture(ctx)
	d.setupStealth(ctx)
	d.setupBlockedResources(ctx)

	d.log(fmt.Sprintf("> Loading %s...", d.url))

	if err := d.navigateWithRetries(ctx); err != nil {
		d.log(fmt.Sprintf("> Loading warning: %s", err))
		d.log("> Attempting to continue anyway...")
	}

	time.Sleep(1 * time.Second)
	d.captureBaseURL(ctx)

	d.waitAndRemovePreloader(ctx)

	htmlContent, isIframe := d.extractIframeContent(ctx)

	if !isIframe {
		d.log("> Scrolling page for lazy content...")
		d.scrollPage(ctx)
		time.Sleep(300 * time.Millisecond)
	}

	d.waitForAnimationsToSettle(ctx)

	sessionCookies, err := d.getCookies(ctx)
	if err == nil {
		d.httpClient = newCookiedHTTPClient(sessionCookies, d.baseURL)
	}

	var finalHTML string
	if isIframe && htmlContent != "" {
		finalHTML = htmlContent
		d.log("> Using iframe extracted content")
	} else {
		if err := chromedp.Run(ctx, chromedp.OuterHTML("html", &finalHTML)); err != nil {
			return fmt.Errorf("get html: %w", err)
		}
	}

	d.networkResourcesMu.RLock()
	resourceCount := len(d.networkResources)
	d.networkResourcesMu.RUnlock()
	d.log(fmt.Sprintf("> Captured %d network resources", resourceCount))

	d.fetchCSSTextFromBrowser(ctx, finalHTML)

	d.log("> Processing HTML and assets...")
	if err := d.processHTML(finalHTML); err != nil {
		return fmt.Errorf("process html: %w", err)
	}

	d.flushResources()

	assetsCount := len(d.resourceCache)
	d.log(fmt.Sprintf("> Done! %d assets saved", assetsCount))

	d.networkResourcesMu.Lock()
	d.networkResources = nil
	d.networkResourcesMu.Unlock()
	d.resourceCache = nil
	if d.httpClient != nil {
		d.httpClient = nil
	}

	return nil
}

func (d *WebsiteDownloader) setupNetworkCapture(ctx context.Context) {
	chromedp.ListenTarget(ctx, func(ev interface{}) {
		switch ev := ev.(type) {
		case *network.EventResponseReceived:
			if ev.Response.Status != 200 {
				return
			}
			u := ev.Response.URL
			if strings.HasPrefix(u, "data:") || strings.HasPrefix(u, "blob:") {
				return
			}
			d.pendingReqs[ev.RequestID] = &pendingRequest{
				url:      u,
				mimeType: ev.Response.MimeType,
			}
		case *network.EventLoadingFinished:
			req, ok := d.pendingReqs[ev.RequestID]
			if !ok {
				return
			}
			body, err := network.GetResponseBody(ev.RequestID).Do(ctx)
			if err == nil && len(body) > 0 && len(body) <= maxResourceBytes {
				d.networkResourcesMu.Lock()
				d.networkResources[req.url] = &networkResource{
					body:        body,
					contentType: req.mimeType,
				}
				d.networkResourcesMu.Unlock()
			}
			delete(d.pendingReqs, ev.RequestID)
		case *network.EventLoadingFailed:
			delete(d.pendingReqs, ev.RequestID)
		}
	})
}

func (d *WebsiteDownloader) setupStealth(ctx context.Context) {
	script := `
		Object.defineProperty(navigator, 'webdriver', { get: () => undefined });
		Object.defineProperty(navigator, 'languages', { get: () => ['en-US', 'en'] });
		Object.defineProperty(navigator, 'plugins', { get: () => [1, 2, 3] });
		window.chrome = window.chrome || { runtime: {} };
	`
	_, _ = page.AddScriptToEvaluateOnNewDocument(script).Do(ctx)
}

func (d *WebsiteDownloader) setupBlockedResources(ctx context.Context) {
	chromedp.ListenTarget(ctx, func(ev interface{}) {
		if ev, ok := ev.(*fetch.EventRequestPaused); ok {
			resType := ev.ResourceType
			if resType == network.ResourceTypeWebSocket ||
				resType == network.ResourceTypeEventSource {
				fetch.FailRequest(ev.RequestID, network.ErrorReasonBlockedByClient).Do(ctx)
			} else {
				fetch.ContinueRequest(ev.RequestID).Do(ctx)
			}
		}
	})
	fetch.Enable().Do(ctx)
}

func (d *WebsiteDownloader) navigateWithRetries(ctx context.Context) error {
	strategies := []struct {
		waitFor string
		timeout time.Duration
	}{
		{"domcontentloaded", 60 * time.Second},
		{"load", 80 * time.Second},
		{"networkidle", 90 * time.Second},
	}

	var lastErr error
	for _, s := range strategies {
		navCtx, cancel := context.WithTimeout(ctx, s.timeout)
		var err error
		switch s.waitFor {
		case "domcontentloaded":
			err = chromedp.Run(navCtx,
				chromedp.Navigate(d.url),
				chromedp.WaitReady("body"),
			)
		case "load":
			err = chromedp.Run(navCtx,
				chromedp.Navigate(d.url),
				chromedp.Sleep(500*time.Millisecond),
			)
		case "networkidle":
			err = chromedp.Run(navCtx,
				chromedp.ActionFunc(func(ctx context.Context) error {
					_, _, _, err := page.Navigate(d.url).Do(ctx)
					return err
				}),
			)
		}
		cancel()

		if err == nil {
			d.log(fmt.Sprintf("> Page loaded (%s)", s.waitFor))
			time.Sleep(1500 * time.Millisecond)
			return nil
		}
		lastErr = err
		d.log(fmt.Sprintf("> Attempt (%s) failed: %s", s.waitFor, err))
	}

	var currentURL string
	chromedp.Run(ctx, chromedp.Location(&currentURL))
	if currentURL != "" && currentURL != "about:blank" {
		d.log("> Continuing with partial content...")
		return nil
	}

	if lastErr != nil {
		return lastErr
	}
	return nil
}

func (d *WebsiteDownloader) captureBaseURL(ctx context.Context) {
	var location string
	if err := chromedp.Run(ctx, chromedp.Location(&location)); err == nil && location != "" {
		d.baseURL = location
	}
}

func (d *WebsiteDownloader) getCookies(ctx context.Context) ([]*network.Cookie, error) {
	var cookies []*network.Cookie
	err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		var e error
		cookies, e = network.GetCookies().Do(ctx)
		return e
	}))
	return cookies, err
}

func (d *WebsiteDownloader) scrollPage(ctx context.Context) {
	d.log("> Preparing scroll for lazy content...")

	chromedp.Run(ctx, chromedp.Evaluate(`
		(() => {
			window.__webcopy_origStyles = {
				htmlScrollBehavior: document.documentElement.style.scrollBehavior,
				bodyScrollBehavior: document.body.style.scrollBehavior,
				htmlOverflow: document.documentElement.style.overflow,
				bodyOverflow: document.body.style.overflow,
			};

			document.documentElement.style.scrollBehavior = 'auto';
			document.body.style.scrollBehavior = 'auto';

			if (window.lenis) {
				try { window.lenis.stop(); } catch(e) {}
			}

			if (getComputedStyle(document.body).overflow === 'hidden') {
				document.body.style.overflow = 'auto';
			}
			if (getComputedStyle(document.documentElement).overflow === 'hidden') {
				document.documentElement.style.overflow = 'auto';
			}
		})()
	`, nil))

	var totalHeight, viewportHeight int
	chromedp.Run(ctx,
		chromedp.Evaluate(`Math.max(document.body.scrollHeight, document.documentElement.scrollHeight)`, &totalHeight),
		chromedp.Evaluate(`window.innerHeight`, &viewportHeight),
	)

	current := 0
	noChangeCount := 0
	for iteration := 0; current < totalHeight && iteration < 10; iteration++ {
		chromedp.Run(ctx, chromedp.Evaluate(fmt.Sprintf(`
			((pos) => {
				window.scrollTo(0, pos);
				document.documentElement.scrollTop = pos;
				document.body.scrollTop = pos;
				const containers = document.querySelectorAll('[data-scroll-container], .scroll-container, main');
				containers.forEach(c => { c.scrollTop = pos; });
			})(%d)
		`, current), nil))
		time.Sleep(200 * time.Millisecond)
		current += viewportHeight

		var newHeight int
		chromedp.Run(ctx, chromedp.Evaluate(`Math.max(document.body.scrollHeight, document.documentElement.scrollHeight)`, &newHeight))
		if newHeight > totalHeight {
			totalHeight = newHeight
			noChangeCount = 0
		} else {
			noChangeCount++
		}

		if noChangeCount >= 2 {
			break
		}
	}

	chromedp.Run(ctx, chromedp.Evaluate(`window.scrollTo(0, 0)`, nil))
	time.Sleep(300 * time.Millisecond)

	chromedp.Run(ctx, chromedp.Evaluate(`
		(() => {
			const orig = window.__webcopy_origStyles;
			if (!orig) return;
			document.documentElement.style.scrollBehavior = orig.htmlScrollBehavior;
			document.body.style.scrollBehavior = orig.bodyScrollBehavior;
			document.documentElement.style.overflow = orig.htmlOverflow;
			document.body.style.overflow = orig.bodyOverflow;

			if (window.lenis) {
				try { window.lenis.start(); } catch(e) {}
			}

			delete window.__webcopy_origStyles;
		})()
	`, nil))
	time.Sleep(300 * time.Millisecond)
}

func (d *WebsiteDownloader) fetchCSSTextFromBrowser(ctx context.Context, htmlContent string) {
	d.log("> Fetching CSS from browser cache...")

	if strings.Count(strings.ToLower(htmlContent), `rel="stylesheet"`) == 0 &&
		strings.Count(strings.ToLower(htmlContent), "rel=stylesheet") == 0 {
		d.log("> No external stylesheets found in HTML")
		return
	}

	script := `(async () => {
		const links = document.querySelectorAll('link[rel="stylesheet"]');
		const fetchPromises = Array.from(links).map(async (link) => {
			const href = link.href;
			if (!href || href.startsWith('data:') || href.startsWith('blob:')) return null;
			try {
				const resp = await fetch(href, {cache: 'force-cache'});
				if (!resp.ok) return null;
				const text = await resp.text();
				return {url: href, content: text};
			} catch(e) { return null; }
		});
		const results = (await Promise.all(fetchPromises)).filter(r => r !== null);
		return results;
	})()`

	var cssContents []struct {
		URL     string `json:"url"`
		Content string `json:"content"`
	}

	err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		res, _, err := runtime.Evaluate(script).WithAwaitPromise(true).WithReturnByValue(true).Do(ctx)
		if err != nil {
			return err
		}
		return json.Unmarshal(res.Value, &cssContents)
	}))

	if err != nil {
		d.log(fmt.Sprintf("> CSS fetch error: %v", err))
		return
	}

	if len(cssContents) == 0 {
		d.log("> No stylesheets fetched from browser")
		return
	}

	d.networkResourcesMu.Lock()
	for _, item := range cssContents {
		if _, exists := d.networkResources[item.URL]; !exists {
			d.networkResources[item.URL] = &networkResource{
				body:        []byte(item.Content),
				contentType: "text/css",
			}
		}
	}
	d.networkResourcesMu.Unlock()

	d.log(fmt.Sprintf("> Fetched %d stylesheets from browser", len(cssContents)))
}

func (d *WebsiteDownloader) waitAndRemovePreloader(ctx context.Context) {
	var hasPreloader bool
	chromedp.Run(ctx, chromedp.Evaluate(`
		(() => {
			const selectors = [
				'.preloader', '.loader', '.loading-screen', '.page-loader',
				'.site-loader', '#preloader', '#loader', '#loading',
				'[class*="preload"]', '[class*="page-load"]',
				'[data-preloader]', '.loading-overlay'
			];
			for (const sel of selectors) {
				const el = document.querySelector(sel);
				if (el) {
					const style = getComputedStyle(el);
					if (style.display !== 'none' && style.opacity !== '0' && style.visibility !== 'hidden') {
						return true;
					}
				}
			}
			return false;
		})()
	`, &hasPreloader))

	if !hasPreloader {
		return
	}

	d.log("> Preloader detected, waiting for animation...")

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var isGone bool
		chromedp.Run(ctx, chromedp.Evaluate(`
			(() => {
				const selectors = [
					'.preloader', '.loader', '.loading-screen', '.page-loader',
					'.site-loader', '#preloader', '#loader', '#loading',
					'[data-preloader]', '.loading-overlay'
				];
				for (const sel of selectors) {
					const el = document.querySelector(sel);
					if (el) {
						const style = getComputedStyle(el);
						if (style.display !== 'none' && style.opacity !== '0' &&
							style.visibility !== 'hidden' &&
							parseFloat(style.opacity) > 0.01) {
							return false;
						}
					}
				}
				return true;
			})()
		`, &isGone))

		if isGone {
			d.log("> Preloader disappeared")
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	chromedp.Run(ctx, chromedp.Evaluate(`
		(() => {
			const selectors = [
				'.preloader', '.loader', '.loading-screen', '.page-loader',
				'.site-loader', '#preloader', '#loader', '#loading',
				'[data-preloader]', '.loading-overlay'
			];
			let removed = 0;
			for (const sel of selectors) {
				document.querySelectorAll(sel).forEach(el => {
					const style = getComputedStyle(el);
					if (style.display === 'none' || style.opacity === '0' ||
						style.visibility === 'hidden' || parseFloat(style.opacity) < 0.02) {
						el.remove();
						removed++;
					}
				});
			}
			return removed;
		})()
	`, nil))

	d.log("> Preloader removed from DOM")
}

func (d *WebsiteDownloader) waitForAnimationsToSettle(ctx context.Context) {
	d.log("> Waiting for initial animations...")

	deadline := time.Now().Add(3 * time.Second)
	var prevCount int = -1
	var sameCount int = 0

	for time.Now().Before(deadline) {
		var count int
		chromedp.Run(ctx, chromedp.Evaluate(`
			document.getAnimations().filter(a => a.playState === 'running').length
		`, &count))

		if count == 0 {
			break
		}

		if count == prevCount {
			sameCount++
		} else {
			sameCount = 0
		}
		prevCount = count

		if sameCount >= 3 {
			break
		}

		time.Sleep(300 * time.Millisecond)
	}

	time.Sleep(500 * time.Millisecond)
}

func (d *WebsiteDownloader) extractIframeContent(ctx context.Context) (string, bool) {
	var srcdocHTML string
	chromedp.Run(ctx, chromedp.Evaluate(`document.querySelector('iframe[srcdoc]')?.getAttribute('srcdoc')`, &srcdocHTML))
	if srcdocHTML != "" {
		d.log("> Detected srcdoc iframe - extracting content...")
		decoded := html.UnescapeString(srcdocHTML)
		return decoded, true
	}

	var wrapperHTML string
	chromedp.Run(ctx, chromedp.Evaluate(`document.documentElement.outerHTML`, &wrapperHTML))

	if d.isWrapperPage(wrapperHTML) {
		d.log("> Wrapper page detected. Waiting for iframes...")
		content, ok := d.waitForRealContentFrame(ctx)
		if ok {
			d.log(fmt.Sprintf("> Real content found in iframe"))
			return content, true
		}
	}

	return "", false
}

func (d *WebsiteDownloader) isWrapperPage(html string) bool {
	if len(html) >= 8000 {
		return false
	}

	doc, err := parseHTML(strings.NewReader(html))
	if err != nil {
		return false
	}

	directChildren := doc.Find("body > *").Length()
	iframes := doc.Find("iframe").Length()
	return directChildren <= 5 && iframes > 0
}

func (d *WebsiteDownloader) waitForRealContentFrame(ctx context.Context) (string, bool) {
	deadline := time.Now().Add(15 * time.Second)
	var bestScore int
	var bestContent string

	for time.Now().Before(deadline) {
		var frames []struct {
			URL     string `json:"url"`
			Content string `json:"-"`
		}

		chromedp.Run(ctx, chromedp.Evaluate(`
			(() => {
				const results = [];
				try {
					for (const frame of window.frames) {
						try {
							results.push({
								url: frame.location.href,
							});
						} catch(e) {}
					}
				} catch(e) {}
				return results;
			})()
		`, &frames))

		for _, f := range frames {
			if f.URL == "about:blank" {
				continue
			}
			score := d.scoreFrameContent(f.Content)
			if score > bestScore {
				bestScore = score
				bestContent = f.Content
			}
		}

		if bestScore >= 50 {
			return bestContent, true
		}

		time.Sleep(1 * time.Second)
	}

	if bestScore > 0 {
		return bestContent, true
	}
	return "", false
}

func (d *WebsiteDownloader) scoreFrameContent(content string) int {
	if len(content) < 500 {
		return -1
	}

	lowered := strings.ToLower(content)

	loaderMarkers := []string{
		"loading-preview", "preview environment is not responding",
		"starting up", "cdn-cgi/challenge", "criteo", "doubleclick",
		"googletagmanager", "googlesyndication", "analytics", "pixel",
	}
	for _, m := range loaderMarkers {
		if strings.Contains(lowered, m) {
			return -1
		}
	}

	if !strings.Contains(lowered, "<head") || !strings.Contains(lowered, "<body") {
		return -1
	}

	score := 0
	score += strings.Count(lowered, "<link") * 5
	score += strings.Count(lowered, "<script") * 2
	score += strings.Count(lowered, "<div")
	score += strings.Count(lowered, "<img") * 3
	score += strings.Count(lowered, "<section") * 2

	spaMarkers := []string{`id="__next"`, `id="root"`, `id="app"`, `id="__nuxt"`,
		`__next_data__`, `data-reactroot`}
	for _, m := range spaMarkers {
		if strings.Contains(lowered, m) {
			score += 25
			break
		}
	}

	score += min(len(content)/1000, 100)
	return score
}
