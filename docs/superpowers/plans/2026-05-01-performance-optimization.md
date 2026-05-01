# Performance Optimization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduce CPU usage 35-45%, improve speed 10-20s, zero accuracy degradation.

**Architecture:** Keep warm Chromium instance across requests (BrowserManager). Add rendering-avoidance flags. Reduce wait timers. Add early-exit logic for scroll/animation loops. Parallelize CSS fetch and asset disk writes.

**Tech Stack:** Go 1.25, chromedp, goquery, standard library.

**Spec:** `docs/superpowers/specs/2026-05-01-performance-optimization-design.md`

---

## File Map

| File | Responsibility |
|---|---|
| `browsermanager.go` | **New.** BrowserManager singleton — launches Chromium once with optimized flags, provides tab contexts per download, health checks, restart |
| `main.go:17-26` | Add `browsermanager` import, BrowserManager init, shutdown handler, pass allocCtx to downloader |
| `downloader.go:1-30` | Add `context` + `sync` to imports (context already imported, need sync for mutex). Move flag creation out of `Process()` |
| `downloader.go:31-41` | Add `mu sync.Mutex` to `WebsiteDownloader` struct for parallel asset save |
| `downloader.go:161-197` | Modify `Process()` signature: accept `context.Context` parameter (allocCtx). Remove flag creation. Use provided allocCtx. |
| `downloader.go:206,353` | Reduce post-navigation sleep (2s→1s), nav-success sleep (2.5s→1.5s) |
| `downloader.go:390-465` | Smart scroll: max iterations 15→10, delay 400ms→200ms, early exit after 2 consecutive same-height |
| `downloader.go:613-630` | Smart anim: deadline 5s→3s, break after 3 same-count polls, final settle 1s→500ms |
| `downloader.go:476-490` | Parallel CSS: `for...await fetch()` → `Promise.all(links.map(link => fetch(link.href, { cache: 'force-cache' })))` |
| `downloader.go:102-119` | Parallel asset save: goroutine pool N=4 with mutex on `resourceCache` |
| `downloader.go:435,446,464,629` | Settle sleep reductions |

---

### Task 1: BrowserManager + Flag Migration + Downloader Integration

**Files:**
- Create: `browsermanager.go`
- Modify: `main.go:17-26` (imports), `main.go:95-118` (runDownload), `main.go:386-422` (main + shutdown)
- Modify: `downloader.go:161-197` (Process signature, remove flag creation)

- [ ] **Step 1: Write BrowserManager unit test**

Create `browsermanager_test.go`:

```go
package main

import (
	"context"
	"testing"
	"time"
)

func TestBrowserManagerHealthCheck(t *testing.T) {
	bm, err := NewBrowserManager()
	if err != nil {
		t.Skipf("Cannot start browser: %v", err)
	}
	defer bm.Shutdown()

	if !bm.Healthy() {
		t.Fatal("Expected browser to be healthy after creation")
	}

	ctx1, cancel1 := bm.NewTab()
	defer cancel1()

	if ctx1 == nil {
		t.Fatal("Expected non-nil context from NewTab")
	}

	var title string
	err = chromedp.Run(ctx1, chromedp.Navigate("about:blank"), chromedp.Title(&title))
	if err != nil {
		t.Fatalf("Expected navigation to work: %v", err)
	}

	cancel1()

	if !bm.Healthy() {
		t.Fatal("Browser should still be healthy after closing a tab")
	}
}

func TestBrowserManagerMultipleTabs(t *testing.T) {
	bm, err := NewBrowserManager()
	if err != nil {
		t.Skipf("Cannot start browser: %v", err)
	}
	defer bm.Shutdown()

	ctx1, cancel1 := bm.NewTab()
	defer cancel1()

	if !bm.Healthy() {
		t.Fatal("Expected healthy with one tab")
	}

	cancel1()
	time.Sleep(100 * time.Millisecond)

	ctx2, cancel2 := bm.NewTab()
	defer cancel2()

	if !bm.Healthy() {
		t.Fatal("Expected healthy after closing tab and opening new one")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -run TestBrowserManager -v -count=1
```

Expected: FAIL — `undefined: NewBrowserManager`

- [ ] **Step 3: Create BrowserManager**

Create `browsermanager.go`:

```go
package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
)

type BrowserManager struct {
	mu          sync.Mutex
	allocCtx    context.Context
	allocCancel context.CancelFunc
	healthy     bool
}

func NewBrowserManager() (*BrowserManager, error) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-setuid-sandbox", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("disable-default-apps", true),
		chromedp.Flag("disable-sync", true),
		chromedp.Flag("disable-translate", true),
		chromedp.Flag("metrics-recording-only", true),
		chromedp.Flag("mute-audio", true),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("safebrowsing-disable-auto-update", true),
		chromedp.Flag("disable-webgl", true),
		chromedp.Flag("disable-3d-apis", true),
		chromedp.Flag("disable-accelerated-2d-canvas", true),
		chromedp.UserAgent("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"),
		chromedp.WindowSize(1366, 768),
	)

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	bm := &BrowserManager{
		allocCtx:    allocCtx,
		allocCancel: allocCancel,
		healthy:     true,
	}

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	if err := chromedp.Run(ctx, chromedp.EmulateViewport(1366, 768)); err != nil {
		allocCancel()
		return nil, fmt.Errorf("viewport: %w", err)
	}

	return bm, nil
}

func (bm *BrowserManager) NewTab() (context.Context, context.CancelFunc) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	ctx, cancel := chromedp.NewContext(bm.allocCtx)
	return ctx, cancel
}

func (bm *BrowserManager) Healthy() bool {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if !bm.healthy {
		return false
	}

	if bm.allocCtx.Err() != nil {
		bm.healthy = false
		return false
	}

	checkCtx, checkCancel := context.WithTimeout(bm.allocCtx, 3*time.Second)
	defer checkCancel()

	ctx, cancel := chromedp.NewContext(checkCtx)
	defer cancel()

	var result string
	err := chromedp.Run(ctx, chromedp.Evaluate(`"ok"`, &result))

	if err != nil || result != "ok" {
		bm.healthy = false
		return false
	}

	return true
}

func (bm *BrowserManager) Restart() error {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if bm.allocCancel != nil {
		bm.allocCancel()
	}

	var err error
	bm, err = NewBrowserManager()
	return err
}

func (bm *BrowserManager) Shutdown() {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if bm.allocCancel != nil {
		bm.allocCancel()
	}
	bm.healthy = false
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test -run TestBrowserManager -v -count=1 -timeout 60s
```

Expected: PASS for all BrowserManager tests.

- [ ] **Step 5: Modify downloader.go Process() to accept allocCtx**

In `downloader.go:161-197`, change `Process()` signature and remove flag creation:

```go
func (d *WebsiteDownloader) Process(allocCtx context.Context) error {
	os.RemoveAll(d.outputDir)
	os.MkdirAll(d.assetsDir, 0755)

	ctx, ctxCancel := chromedp.NewContext(allocCtx)
	defer ctxCancel()

	if err := chromedp.Run(ctx, chromedp.EmulateViewport(1366, 768)); err != nil {
		return fmt.Errorf("viewport: %w", err)
	}

	d.log("> Starting browser...")

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

	d.log(fmt.Sprintf("> Captured %d network resources", len(d.networkResources)))

	d.fetchCSSTextFromBrowser(ctx, finalHTML)

	d.log("> Processing HTML and assets...")
	if err := d.processHTML(finalHTML); err != nil {
		return fmt.Errorf("process html: %w", err)
	}

	assetsCount := len(d.resourceCache)
	d.log(fmt.Sprintf("> Done! %d assets saved", assetsCount))

	d.networkResources = nil
	d.resourceCache = nil
	if d.httpClient != nil {
		d.httpClient = nil
	}

	return nil
}
```

Note: `chromedp` import is already in downloader.go since the file uses `chromedp.NewContext`, `chromedp.EmulateViewport`, etc. The `context` import is also already present.

- [ ] **Step 6: Modify main.go to use BrowserManager**

In `main.go:17-26`, add `context` to imports if not present:

```go
import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
)
```

In `main.go:386-422` (the `main()` function), initialize BrowserManager:

```go
var bm *BrowserManager

func main() {
	var err error
	tmpl, err = template.ParseFiles("templates/index.html")
	if err != nil {
		log.Fatalf("Failed to parse template: %v", err)
	}

	os.MkdirAll(downloadDir, 0755)
	cleanDownloadDir(downloadDir)

	sm = newSessionManager()
	go sm.cleanup()

	bm, err = NewBrowserManager()
	if err != nil {
		log.Fatalf("Failed to start browser: %v", err)
	}
	log.Println("Browser started (warm)")

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Shutting down browser...")
		bm.Shutdown()
		os.Exit(0)
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", handleIndex)
	mux.HandleFunc("GET /health", handleHealth)
	mux.HandleFunc("POST /start-download", handleStartDownload)
	mux.HandleFunc("GET /stream/{session_id}", handleStream)
	mux.HandleFunc("GET /download-file/{session_id}", handleDownloadFile)

	port := os.Getenv("PORT")
	if port == "" {
		port = "5001"
	}

	log.Printf("Server started on port %s", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
```

In `main.go:95-118` (the `runDownload` function), pass allocCtx to downloader:

```go
func (sm *sessionManager) runDownload(id, url string) {
	defer func() {
		sm.mu.Lock()
		sm.activeDownloads--
		sm.mu.Unlock()
	}()

	s, _ := sm.get(id)
	if s == nil {
		return
	}

	workDir := fmt.Sprintf("%s/%s", downloadDir, id)
	zipPath := fmt.Sprintf("%s/%s.zip", downloadDir, id)

	logFn := func(msg string) {
		select {
		case s.MsgChan <- msg:
		default:
		}
	}

	d := NewWebsiteDownloader(url, workDir, logFn)

	if ok := bm.Healthy(); !ok {
		logFn("> Browser died — restarting...")
		if restartErr := bm.Restart(); restartErr != nil {
			logFn(fmt.Sprintf("> Fatal: browser restart failed: %s", restartErr))
			sm.mu.Lock()
			s.Result = sessionResult{
				Status:    "error",
				CreatedAt: time.Now().Unix(),
				Error:     restartErr.Error(),
			}
			sm.mu.Unlock()
			os.RemoveAll(workDir)
			return
		}
		logFn("> Browser restarted")
	}

	tabCtx, tabCancel := bm.NewTab()
	defer tabCancel()

	if err := d.Process(tabCtx); err != nil {
		logFn(fmt.Sprintf("> Error: %s", err))
		sm.mu.Lock()
		s.Result = sessionResult{
			Status:    "error",
			CreatedAt: time.Now().Unix(),
			Error:     err.Error(),
		}
		sm.mu.Unlock()
		os.RemoveAll(workDir)
		os.Remove(zipPath)
		return
	}

	logFn("Creating ZIP file...")
	siteName := getSiteName(url)
	zipFilename := siteName + ".zip"
	if err := zipDirectory(workDir, zipPath); err != nil {
		logFn(fmt.Sprintf("> Error creating ZIP: %s", err))
		sm.mu.Lock()
		s.Result = sessionResult{
			Status:    "error",
			CreatedAt: time.Now().Unix(),
			Error:     err.Error(),
		}
		sm.mu.Unlock()
		return
	}

	os.RemoveAll(workDir)

	logFn("Download ready!")

	sm.mu.Lock()
	s.Result = sessionResult{
		Status:    "complete",
		ZipPath:   zipPath,
		Filename:  zipFilename,
		CreatedAt: time.Now().Unix(),
	}
	sm.mu.Unlock()
}
```

- [ ] **Step 7: Verify compilation**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 8: Run all BrowserManager tests**

```bash
go test -run TestBrowserManager -v -count=1 -timeout 60s
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add browsermanager.go browsermanager_test.go downloader.go main.go
git commit -m "feat: add BrowserManager for warm Chrome reuse across requests"
```

---

### Task 2: Downloader Optimizations (Timers, Scroll, Animations, CSS, Assets)

**Files:**
- Modify: `downloader.go:206` (post-nav sleep), `downloader.go:353` (nav-success sleep)
- Modify: `downloader.go:390-465` (scrollPage), `downloader.go:435,446,464` (scroll delays)
- Modify: `downloader.go:613-630` (waitForAnimationsToSettle)
- Modify: `downloader.go:476-490` (fetchCSSTextFromBrowser)
- Modify: `downloader.go:102-119` (saveResource, add parallel version)
- Modify: `downloader.go:31-41` (struct, add mutex)
- Run: `processor_test.go` to verify no regression

- [ ] **Step 1: Reduce post-navigation and nav-success sleeps**

In `downloader.go:206`, change:
```go
// BEFORE
time.Sleep(2 * time.Second)
// AFTER
time.Sleep(1 * time.Second)
```

In `downloader.go:353`, change:
```go
// BEFORE (inside navigateWithRetries, after successful navigation)
time.Sleep(2500 * time.Millisecond)
// AFTER
time.Sleep(1500 * time.Millisecond)
```

- [ ] **Step 2: Verify navigateWithRetries timers**

Check `downloader.go:353` exact location:

```go
if err == nil {
    d.log(fmt.Sprintf("> Page loaded (%s)", s.waitFor))
    time.Sleep(1500 * time.Millisecond) // was 2500
    return nil
}
```

- [ ] **Step 3: Optimize scrollPage**

Replace `downloader.go:390-465` (the entire `scrollPage` function):

```go
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
```

- [ ] **Step 4: Optimize waitForAnimationsToSettle**

Replace `downloader.go:613-630`:

```go
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
```

- [ ] **Step 5: Parallel CSS fetch**

Replace `downloader.go:476-490` (the JS script inside `fetchCSSTextFromBrowser`):

```go
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
```

- [ ] **Step 6: Parallel asset save**

Add mutex to `WebsiteDownloader` struct in `downloader.go:31-41`:

```go
type WebsiteDownloader struct {
	url              string
	outputDir        string
	assetsDir        string
	resourceCache    map[string]string
	resourceCacheMu  sync.Mutex
	networkResources map[string]*networkResource
	baseURL          string
	logCallback      func(string)
	httpClient       *cookiedHTTPClient
	pendingReqs      map[network.RequestID]*pendingRequest
}
```

Add `"sync"` to imports if not present.

Replace `downloader.go:102-119` (`saveResource`):

```go
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
	d.resourceCache[assetURL] = relPath
	d.resourceCacheMu.Unlock()
	return relPath
}
```

Add parallel save method after `saveResource`:

```go
func (d *WebsiteDownloader) flushResources() {
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)

	type resource struct {
		url         string
		body        []byte
		contentType string
	}
	resources := make([]resource, 0, len(d.networkResources))
	for u, r := range d.networkResources {
		resources = append(resources, resource{u, r.body, r.contentType})
	}

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
```

- [ ] **Step 7: Call flushResources in Process() after HTML processing**

In `downloader.go:Process()`, replace the end block:

```go
assetsCount := len(d.resourceCache)
d.log(fmt.Sprintf("> Done! %d assets saved", assetsCount))
```

with:

```go
d.flushResources()

assetsCount := len(d.resourceCache)
d.log(fmt.Sprintf("> Done! %d assets saved", assetsCount))
```

- [ ] **Step 8: Verify compilation**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 9: Run processor tests (no regression)**

```bash
go test -run TestProcess -v -count=1
```

Expected: PASS for all existing processor tests.

- [ ] **Step 10: Commit**

```bash
git add downloader.go
git commit -m "perf: reduce timers, smart scroll/anim exit, parallel CSS+assets"
```

---

### Task 3: Integration Test + Final Verification

**Files:**
- Create: `integration_test.go`
- Verify: manual run against Shopify and simple site

- [ ] **Step 1: Write integration test**

Create `integration_test.go`:

```go
package main

import (
	"os"
	"strings"
	"testing"
)

func TestIntegrationSimpleSite(t *testing.T) {
	bm, err := NewBrowserManager()
	if err != nil {
		t.Skipf("Cannot start browser: %v", err)
	}
	defer bm.Shutdown()

	if !bm.Healthy() {
		t.Fatal("Browser not healthy")
	}

	tabCtx, tabCancel := bm.NewTab()
	defer tabCancel()

	workDir := "downloads/test_simple"
	os.RemoveAll(workDir)

	d := NewWebsiteDownloader("https://example.com", workDir, func(msg string) {
		t.Log(msg)
	})

	if err := d.Process(tabCtx); err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	htmlPath := workDir + "/index.html"
	data, err := os.ReadFile(htmlPath)
	if err != nil {
		t.Fatalf("index.html not created: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "<html") {
		t.Fatal("index.html missing <html> tag")
	}
	if !strings.Contains(content, "<body") {
		t.Fatal("index.html missing <body> tag")
	}

	os.RemoveAll(workDir)
	t.Log("Simple site integration test passed")
}

func TestIntegrationAccuracy(t *testing.T) {
	bm, err := NewBrowserManager()
	if err != nil {
		t.Skipf("Cannot start browser: %v", err)
	}
	defer bm.Shutdown()

	tabCtx, tabCancel := bm.NewTab()
	defer tabCancel()

	workDir := "downloads/test_accuracy"
	os.RemoveAll(workDir)

	d := NewWebsiteDownloader("https://example.com", workDir, func(msg string) {
		t.Log(msg)
	})

	if err := d.Process(tabCtx); err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	assetsDir := workDir + "/assets"
	entries, err := os.ReadDir(assetsDir)
	if err != nil {
		t.Fatalf("assets dir not found: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("No assets saved")
	}

	htmlData, err := os.ReadFile(workDir + "/index.html")
	if err != nil {
		t.Fatal("index.html not found")
	}

	if len(htmlData) < 1000 {
		t.Fatal("index.html too small — likely incomplete capture")
	}

	os.RemoveAll(workDir)
	t.Logf("Accuracy test passed — %d assets, %d bytes HTML", len(entries), len(htmlData))
}
```

- [ ] **Step 2: Run integration tests**

```bash
go test -run TestIntegration -v -count=1 -timeout 120s
```

Expected: PASS for both tests.

- [ ] **Step 3: Run ALL tests to verify no regression**

```bash
go test ./... -v -count=1 -timeout 120s
```

Expected: ALL PASS. No failures, no errors.

- [ ] **Step 4: Manual verification against Shopify**

```bash
go run . &
sleep 3
curl -s -X POST http://localhost:5001/start-download -H "Content-Type: application/json" -d '{"url":"https://www.shopify.com/ca/editions/winter2026"}'
```

Verify:
- CPU stays below 70% during capture (check Task Manager)
- Response returns session_id
- Download ZIP and open index.html in browser — page renders correctly
- Assets loaded (images, fonts, CSS visible)

- [ ] **Step 5: Commit**

```bash
git add integration_test.go
git commit -m "test: add integration tests for BrowserManager + full capture"
```

---

## Verification Checklist

Before marking plan complete:

- [ ] `go build ./...` — no errors
- [ ] `go test ./...` — all tests pass
- [ ] BrowserManager tests pass (new tabs work, health check works)
- [ ] Processor tests pass (no regression on HTML transforms)
- [ ] Integration test passes (full capture works)
- [ ] Manual Shopify test: CPU < 70%, output renders correctly
- [ ] Simple site (example.com): output identical to before optimization
