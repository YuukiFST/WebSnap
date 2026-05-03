# Full Runtime Accuracy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Transform WebCopy from a static snapshot tool into a full runtime archive tool that preserves 100% of JavaScript, WebGL, and interactivity, enabling perfect offline replicas of any website.

**Architecture:** Remove all JS-stripping logic. Enable WebGL in ChromeDP. Capture all network assets plus dynamically-discovered URLs via runtime instrumentation. Rewrite all paths to local disk. Inject import maps for ES modules.

**Tech Stack:** Go, ChromeDP, goquery, go test

---

## File Structure

| File | Responsibility |
|---|---|
| `browsermanager.go` | ChromeDP allocator config. Enables WebGL, 3D APIs, increases JS heap. |
| `downloader.go` | Asset capture pipeline. Adds runtime URL collector via injected JS. |
| `processor.go` | HTML/CSS/JS rewriter. Removes JS-stripping functions. Adds import map injection. |
| `integration_test.go` | Integration tests including LiquidGlass visual regression. |
| `processor_test.go` | Unit tests for rewriter engine (import map, script preservation). |
| `browsermanager_test.go` | Unit tests for browser config (WebGL enabled). |

---

## Task 1: Enable WebGL & Rendering in BrowserManager

**Files:**
- Modify: `browsermanager.go:21-46`
- Test: `browsermanager_test.go`

**Context:** WebCopy currently disables WebGL, 3D APIs, and accelerated 2D canvas. This kills WebGL-based sites like LiquidGlass.

### Step 1.1: Remove rendering-killer flags

Remove these lines from the `opts` slice in `NewBrowserManager`:

```go
chromedp.Flag("disable-webgl", true),
chromedp.Flag("disable-3d-apis", true),
chromedp.Flag("disable-accelerated-2d-canvas", true),
```

Also change:
```go
chromedp.Flag("js-flags", "--max_old_space_size=512 --max_semi_space_size=32"),
```
to:
```go
chromedp.Flag("js-flags", "--max_old_space_size=2048 --max_semi_space_size=64"),
```

And remove:
```go
chromedp.Flag("memory-model", "low"),
```

### Step 1.2: Add test verifying WebGL is enabled

Add to `browsermanager_test.go`:

```go
func TestBrowserManagerWebGLEnabled(t *testing.T) {
	bm, err := NewBrowserManager()
	if err != nil {
		t.Skipf("Cannot start browser: %v", err)
	}
	defer bm.Shutdown()

	ctx, cancel := bm.NewTab()
	defer cancel()

	var webglSupported bool
	err = chromedp.Run(ctx, chromedp.Evaluate(`
		(() => {
			try {
				const canvas = document.createElement('canvas');
				return !!(window.WebGLRenderingContext && canvas.getContext('webgl'));
			} catch(e) { return false; }
		})()
	`, &webglSupported))
	if err != nil {
		t.Fatalf("failed to evaluate WebGL support: %v", err)
	}
	if !webglSupported {
		t.Fatal("WebGL is not supported — rendering flags may still be disabled")
	}
}
```

### Step 1.3: Run test

```bash
go test -v -run TestBrowserManagerWebGLEnabled -timeout 60s
```

Expected: PASS

### Step 1.4: Commit

```bash
git add browsermanager.go browsermanager_test.go
git commit -m "feat(browser): enable WebGL and increase JS heap for runtime archive mode"
```

---

## Task 2: Remove JS-Stripping Logic from Processor

**Files:**
- Modify: `processor.go`
- Test: `integration_test.go`, `processor_test.go`

**Context:** `processor.go` contains functions that remove SPA scripts, neutralize animation libraries, force scroll overrides, and inject animation fallbacks. All of these must be removed to preserve runtime behavior.

### Step 2.1: Remove `handleSPAFrameworks`

Delete the entire `handleSPAFrameworks` method from `processor.go` (lines ~639-755).

In `processHTML`, remove the call:
```go
d.handleSPAFrameworks(doc, animCtx)
```

### Step 2.2: Remove `neutralizeDOMTransformScripts`

Delete the entire `neutralizeDOMTransformScripts` method and the `domTransformLibraries` var block (lines ~27-74 and ~293-336).

In `processHTML`, remove the call:
```go
d.neutralizeDOMTransformScripts(doc)
```

### Step 2.3: Remove `fixScrollBlocking`

Delete the entire `fixScrollBlocking` method and `removeScrollBlockingClasses` helper (lines ~216-281).

In `processHTML`, remove the call:
```go
d.fixScrollBlocking(doc, animCtx)
```

### Step 2.4: Remove `injectAnimationFallbacks`

Delete the entire `injectAnimationFallbacks` method (lines ~858-900).

In `processHTML`, remove the call:
```go
d.injectAnimationFallbacks(doc, animCtx)
```

### Step 2.5: Modify `handleWebflowOffline`

Keep the `w-mod-js` / `w-mod-ix` class injection (lines ~778-784), but remove the CSS override block:

```go
webflowFallbackCSS := `
/* WebCopy: Webflow offline fallback */
[data-w-id] {
    opacity: 1 !important;
    visibility: visible !important;
}`

head := doc.Find("head")
if head.Length() > 0 {
	head.AppendHtml(fmt.Sprintf(`<style data-webcopy-webflow-fix="true">%s</style>`, webflowFallbackCSS))
}
```

Also remove the `cleanWebflowInlineStyle` calls (lines ~798-826) and delete the `cleanWebflowInlineStyle` method and its regex vars (lines ~831-856).

In `processHTML`, keep the call `d.handleWebflowOffline(doc, animCtx)` but the method should only inject the CSS classes.

### Step 2.6: Verify `processHTML` calls

After removals, `processHTML` should call only:

```go
d.processStylesheets(doc)
d.processInlineStyles(doc)
d.processScripts(doc)
d.processImages(doc)
d.processInlineStyleAttrs(doc)
d.processFavicons(doc)
d.processMetaImages(doc)
d.processBackgroundData(doc)
d.processLottieAssets(doc)
d.fixNavigationLinks(doc)
d.handleWebflowOffline(doc, animCtx)
```

(plus new functions added in later tasks)

### Step 2.7: Add test verifying scripts are preserved

Add to `processor_test.go`:

```go
func TestScriptsPreserved(t *testing.T) {
	html := `<!DOCTYPE html><html><head><script src="/_next/static/chunks/main.js"></script></head><body></body></html>`
	d := NewWebsiteDownloader("https://example.com", "test_output", func(string) {})
	d.processHTML(html)

	result, err := os.ReadFile("test_output/index.html")
	if err != nil {
		t.Fatal(err)
	}
	content := string(result)
	if !strings.Contains(content, `src="assets/`) && !strings.Contains(content, `src="/_next/`) {
		t.Fatal("script src was removed or not rewritten")
	}
	os.RemoveAll("test_output")
}
```

If `processor_test.go` does not exist, create it.

### Step 2.8: Run test

```bash
go test -v -run TestScriptsPreserved -timeout 30s
```

Expected: PASS

### Step 2.9: Commit

```bash
git add processor.go processor_test.go
git commit -m "feat(processor): remove all JS-stripping logic to preserve runtime interactivity"
```

---

## Task 3: Add Runtime Asset Collector

**Files:**
- Modify: `downloader.go`
- Test: `downloader_test.go`

**Context:** Network interception catches most assets, but dynamically-loaded fonts, dynamic `import()`, `fetch()` responses, and `new Worker()` URLs may be missed. We inject a runtime script into the page to collect these.

### Step 3.1: Add `collectDynamicURLs` method

Add to `downloader.go`:

```go
func (d *WebsiteDownloader) collectDynamicURLs(ctx context.Context) []string {
	script := `(async () => {
		const urls = new Set();

		// Fonts
		try {
			for (const font of document.fonts) {
				if (font.src) urls.add(font.src);
			}
		} catch(e) {}

		// Stylesheet background images and font-face src
		try {
			for (const sheet of document.styleSheets) {
				for (const rule of sheet.cssRules || []) {
					if (rule.style) {
						const bg = rule.style.backgroundImage;
						if (bg && bg.includes('url(')) {
							const m = bg.match(/url\(["']?([^"')]+)["']?\)/);
							if (m) urls.add(m[1]);
						}
					}
					if (rule.cssText && rule.cssText.includes('@font-face')) {
						const m = rule.cssText.match(/src:\s*url\(["']?([^"')]+)["']?\)/g);
						if (m) {
							m.forEach(s => {
								const u = s.match(/url\(["']?([^"')]+)["']?\)/);
								if (u) urls.add(u[1]);
							});
						}
					}
				}
			}
		} catch(e) {}

		// Dynamic imports
		const origImport = window.__webcopy_origImport || (window.__webcopy_origImport = window.import);
		if (origImport && !window.__webcopy_importPatched) {
			window.import = function(specifier) {
				if (typeof specifier === 'string') urls.add(specifier);
				return origImport.apply(this, arguments);
			};
			window.__webcopy_importPatched = true;
		}

		// Workers
		const OrigWorker = window.__webcopy_origWorker || (window.__webcopy_origWorker = window.Worker);
		if (OrigWorker && !window.__webcopy_workerPatched) {
			window.Worker = function(url, options) {
				if (typeof url === 'string') urls.add(url);
				return new OrigWorker(url, options);
			};
			window.__webcopy_workerPatched = true;
		}

		// Fetch
		const origFetch = window.__webcopy_origFetch || (window.__webcopy_origFetch = window.fetch);
		if (origFetch && !window.__webcopy_fetchPatched) {
			window.fetch = function(input, init) {
				const url = typeof input === 'string' ? input : input.url;
				if (url) urls.add(url);
				return origFetch.apply(this, arguments);
			};
			window.__webcopy_fetchPatched = true;
		}

		// XHR
		const origOpen = XMLHttpRequest.prototype.open;
		if (!window.__webcopy_xhrPatched) {
			XMLHttpRequest.prototype.open = function(method, url) {
				urls.add(url);
				return origOpen.apply(this, arguments);
			};
			window.__webcopy_xhrPatched = true;
		}

		// Trigger any lazy-loaded fonts/styles by querying common selectors
		document.querySelectorAll('link[rel="stylesheet"]').forEach(l => urls.add(l.href));
		document.querySelectorAll('img[src], video[src], audio[src], source[src]').forEach(el => urls.add(el.src));

		return Array.from(urls).filter(u => u && !u.startsWith('data:') && !u.startsWith('blob:'));
	})()`

	var result []string
	err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		res, _, err := runtime.Evaluate(script).WithAwaitPromise(true).WithReturnByValue(true).Do(ctx)
		if err != nil {
			return err
		}
		return json.Unmarshal(res.Value, &result)
	}))
	if err != nil {
		d.log(fmt.Sprintf("> Dynamic URL collection error: %v", err))
		return nil
	}
	d.log(fmt.Sprintf("> Collected %d dynamic URLs", len(result)))
	return result
}
```

### Step 3.2: Add `downloadMissingResources` method

Add to `downloader.go`:

```go
func (d *WebsiteDownloader) downloadMissingResources(urls []string) {
	if len(urls) == 0 {
		return
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	downloaded := 0
	var mu sync.Mutex

	for _, u := range urls {
		absURL := d.absolutize(u, "")
		d.networkResourcesMu.RLock()
		_, exists := d.networkResources[absURL]
		d.networkResourcesMu.RUnlock()
		if exists {
			continue
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(url string) {
			defer wg.Done()
			defer func() { <-sem }()

			if d.httpClient == nil {
				return
			}
			body, ct, err := d.httpClient.get(url)
			if err != nil || len(body) == 0 {
				return
			}
			if d.storeNetworkResource(url, body, ct) {
				mu.Lock()
				downloaded++
				mu.Unlock()
			}
		}(absURL)
	}

	wg.Wait()
	if downloaded > 0 {
		d.log(fmt.Sprintf("> Downloaded %d additional resources", downloaded))
	}
}
```

### Step 3.3: Wire into `Process`

In `Process`, after `fetchCSSTextFromBrowser` and before `OuterHTML`, add:

```go
dynamicURLs := d.collectDynamicURLs(ctx)
d.downloadMissingResources(dynamicURLs)
```

### Step 3.4: Run existing tests to verify no regression

```bash
go test -v -timeout 120s
```

Expected: All existing tests PASS.

### Step 3.5: Commit

```bash
git add downloader.go
git commit -m "feat(downloader): add runtime asset collector for dynamic URLs"
```

---

## Task 4: Add Import Map Injection

**Files:**
- Modify: `processor.go`
- Test: `processor_test.go`

**Context:** Modern sites use ES modules from CDN (`import { x } from 'https://cdn.jsdelivr.net/...'`). Offline, these URLs fail. We inject an import map that maps CDN URLs to local paths.

### Step 4.1: Add `injectImportMap` method

Add to `processor.go`:

```go
func (d *WebsiteDownloader) injectImportMap(doc *goquery.Document) {
	mappings := make(map[string]string)

	d.resourceCacheMu.Lock()
	for absURL, localPath := range d.resourceCache {
		// Only map JS assets that look like CDN modules
		if strings.HasSuffix(absURL, ".js") || strings.Contains(absURL, "/npm/") ||
			strings.Contains(absURL, "jsdelivr") || strings.Contains(absURL, "unpkg") ||
			strings.Contains(absURL, "skypack") || strings.Contains(absURL, "esm.sh") {
			mappings[absURL] = localPath
		}
	}
	d.resourceCacheMu.Unlock()

	if len(mappings) == 0 {
		return
	}

	importMap := map[string]interface{}{
		"imports": mappings,
	}
	jsonData, err := json.MarshalIndent(importMap, "", "  ")
	if err != nil {
		return
	}

	head := doc.Find("head")
	if head.Length() > 0 {
		head.PrependHtml(fmt.Sprintf(`<script type="importmap" data-webcopy-importmap="true">\n%s\n</script>`, string(jsonData)))
		d.log(fmt.Sprintf("> Injected import map with %d entries", len(mappings)))
	}
}
```

### Step 4.2: Wire into `processHTML`

Add call in `processHTML` before the final `doc.Html()`:

```go
d.injectImportMap(doc)
```

### Step 4.3: Add test

Add to `processor_test.go`:

```go
func TestImportMapInjection(t *testing.T) {
	html := `<!DOCTYPE html><html><head></head><body></body></html>`
	d := NewWebsiteDownloader("https://example.com", "test_output_importmap", func(string) {})
	// Simulate captured resource
	d.resourceCache["https://cdn.jsdelivr.net/npm/liquidglass/dist/index.js"] = "assets/liquidglass_index.js"
	d.processHTML(html)

	result, err := os.ReadFile("test_output_importmap/index.html")
	if err != nil {
		t.Fatal(err)
	}
	content := string(result)
	if !strings.Contains(content, `<script type="importmap"`) {
		t.Fatal("import map not injected")
	}
	if !strings.Contains(content, `"https://cdn.jsdelivr.net/npm/liquidglass/dist/index.js"`) {
		t.Fatal("CDN URL not in import map")
	}
	if !strings.Contains(content, `"assets/liquidglass_index.js"`) {
		t.Fatal("local path not in import map")
	}
	os.RemoveAll("test_output_importmap")
}
```

### Step 4.4: Run test

```bash
go test -v -run TestImportMapInjection -timeout 30s
```

Expected: PASS

### Step 4.5: Commit

```bash
git add processor.go processor_test.go
git commit -m "feat(processor): inject import map for ES module offline resolution"
```

---

## Task 5: Add LiquidGlass Integration Test

**Files:**
- Modify: `integration_test.go`
- Test: `integration_test.go`

**Context:** LiquidGlass is our primary benchmark. The test verifies that the output contains a `<canvas>` element and that WebGL-related JS assets were captured.

### Step 5.1: Add LiquidGlass test

Add to `integration_test.go`:

```go
func TestIntegrationLiquidGlass(t *testing.T) {
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

	workDir := "downloads/test_liquidglass"
	os.RemoveAll(workDir)

	d := NewWebsiteDownloader("https://liquid-glass.ybouane.com/", workDir, func(msg string) {
		t.Log(msg)
	})

	processCtx, processCancel := context.WithTimeout(tabCtx, 120*time.Second)
	defer processCancel()

	if err := d.Process(processCtx); err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	htmlData, err := os.ReadFile(workDir + "/index.html")
	if err != nil {
		t.Fatal("index.html not found")
	}
	content := string(htmlData)

	// Verify canvas exists (LiquidGlass injects canvas elements)
	if !strings.Contains(content, "<canvas") {
		t.Fatal("No <canvas> found in output — WebGL content may have been stripped")
	}

	// Verify scripts were preserved (not removed by old SPA stripping logic)
	if !strings.Contains(content, "<script") {
		t.Fatal("No scripts found in output")
	}

	// Verify assets directory has JS files (shaders/modules)
	assetsDir := workDir + "/assets"
	entries, err := os.ReadDir(assetsDir)
	if err != nil {
		t.Fatalf("assets dir not found: %v", err)
	}

	jsCount := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".js") {
			jsCount++
		}
	}
	if jsCount == 0 {
		t.Fatal("No JS assets captured — module scripts may be missing")
	}

	t.Logf("LiquidGlass test passed — canvas present, %d JS assets, %d total assets", jsCount, len(entries))
	os.RemoveAll(workDir)
}
```

### Step 5.2: Run test

```bash
go test -v -run TestIntegrationLiquidGlass -timeout 180s
```

**Expected on first run:** May FAIL if WebGL is not fully working or if dynamic imports are not caught. This is acceptable — it validates the gap. Debug and iterate.

**Expected after fixes:** PASS.

### Step 5.3: Commit

```bash
git add integration_test.go
git commit -m "test(integration): add LiquidGlass capture test as accuracy benchmark"
```

---

## Task 6: Add Script Preservation Integration Test

**Files:**
- Modify: `integration_test.go`

### Step 6.1: Add SPA script preservation test

Add to `integration_test.go`:

```go
func TestIntegrationSPAScriptsPreserved(t *testing.T) {
	bm, err := NewBrowserManager()
	if err != nil {
		t.Skipf("Cannot start browser: %v", err)
	}
	defer bm.Shutdown()

	tabCtx, tabCancel := bm.NewTab()
	defer tabCancel()

	workDir := "downloads/test_spa"
	os.RemoveAll(workDir)

	// Use a known Next.js or Gatsby site, or a simple site with script tags
	d := NewWebsiteDownloader("https://example.com", workDir, func(msg string) {
		t.Log(msg)
	})

	if err := d.Process(tabCtx); err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	htmlData, err := os.ReadFile(workDir + "/index.html")
	if err != nil {
		t.Fatal("index.html not found")
	}
	content := string(htmlData)

	// Verify no script-stripping CSS was injected
	if strings.Contains(content, "data-webcopy-anim-fallback") {
		t.Fatal("animation fallback CSS found — JS-stripping logic not fully removed")
	}
	if strings.Contains(content, "data-webcopy-scroll-fix") {
		t.Fatal("scroll fix CSS found — scroll-blocking logic not fully removed")
	}

	t.Log("SPA script preservation test passed")
	os.RemoveAll(workDir)
}
```

### Step 6.2: Run test

```bash
go test -v -run TestIntegrationSPAScriptsPreserved -timeout 120s
```

Expected: PASS

### Step 6.3: Commit

```bash
git add integration_test.go
git commit -m "test(integration): verify no JS-stripping CSS remains in output"
```

---

## Task 7: Visual Regression Test (Screenshot Comparison)

**Files:**
- Create: `accuracy_test.go`

**Context:** Capture a screenshot of the original site, then capture a screenshot of the offline replica, and compare. This is the ultimate accuracy metric.

### Step 7.1: Add screenshot comparison test

Create `accuracy_test.go`:

```go
package main

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

func TestAccuracyLiquidGlass(t *testing.T) {
	bm, err := NewBrowserManager()
	if err != nil {
		t.Skipf("Cannot start browser: %v", err)
	}
	defer bm.Shutdown()

	url := "https://liquid-glass.ybouane.com/"
	workDir := "downloads/test_accuracy_liquidglass"
	os.RemoveAll(workDir)

	d := NewWebsiteDownloader(url, workDir, func(msg string) {
		t.Log(msg)
	})

	tabCtx, tabCancel := bm.NewTab()
	defer tabCancel()

	processCtx, processCancel := context.WithTimeout(tabCtx, 120*time.Second)
	defer processCancel()

	if err := d.Process(processCtx); err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	// Screenshot original
	origCtx, origCancel := bm.NewTab()
	defer origCancel()
	var origScreenshot []byte
	err = chromedp.Run(origCtx,
		chromedp.Navigate(url),
		chromedp.Sleep(3*time.Second),
		chromedp.CaptureScreenshot(&origScreenshot),
	)
	if err != nil {
		t.Fatalf("failed to screenshot original: %v", err)
	}

	// Screenshot replica
	replicaCtx, replicaCancel := bm.NewTab()
	defer replicaCancel()
	var replicaScreenshot []byte
	replicaPath := "file:///" + workDir + "/index.html"
	err = chromedp.Run(replicaCtx,
		chromedp.Navigate(replicaPath),
		chromedp.Sleep(3*time.Second),
		chromedp.CaptureScreenshot(&replicaScreenshot),
	)
	if err != nil {
		t.Fatalf("failed to screenshot replica: %v", err)
	}

	// Compare using simple pixel difference
	similarity := compareScreenshots(origScreenshot, replicaScreenshot)
	threshold := 0.85 // 85% similarity threshold
	if similarity < threshold {
		t.Fatalf("Screenshot similarity %.2f%% below threshold %.2f%%", similarity*100, threshold*100)
	}

	t.Logf("Accuracy test passed — similarity: %.2f%%", similarity*100)
	os.RemoveAll(workDir)
}

func compareScreenshots(a, b []byte) float64 {
	imgA, err := png.Decode(bytes.NewReader(a))
	if err != nil {
		return 0
	}
	imgB, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		return 0
	}

	boundsA := imgA.Bounds()
	boundsB := imgB.Bounds()

	width := boundsA.Dx()
	height := boundsA.Dy()
	if boundsB.Dx() < width {
		width = boundsB.Dx()
	}
	if boundsB.Dy() < height {
		height = boundsB.Dy()
	}

	if width == 0 || height == 0 {
		return 0
	}

	var diff uint64
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			r1, g1, b1, _ := imgA.At(boundsA.Min.X+x, boundsA.Min.Y+y).RGBA()
			r2, g2, b2, _ := imgB.At(boundsB.Min.X+x, boundsB.Min.Y+y).RGBA()
			diff += abs(int(r1)-int(r2)) + abs(int(g1)-int(g2)) + abs(int(b1)-int(b2))
		}
	}

	maxDiff := uint64(width * height * 3 * 65535)
	if maxDiff == 0 {
		return 1.0
	}
	return 1.0 - float64(diff)/float64(maxDiff)
}

func abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}
```

**Note:** Add `"bytes"` to imports in the file if not present.

### Step 7.2: Run test

```bash
go test -v -run TestAccuracyLiquidGlass -timeout 180s
```

**Expected:** May show low similarity initially (e.g. 30-50%) if WebGL/assets missing. Iteratively fix issues until similarity exceeds 85%.

### Step 7.3: Commit

```bash
git add accuracy_test.go
git commit -m "test(accuracy): add screenshot comparison test for LiquidGlass"
```

---

## Task 8: Final Integration & Cleanup

### Step 8.1: Run full test suite

```bash
go test -v -timeout 300s ./...
```

Expected: All tests PASS.

### Step 8.2: Update AGENTS.md (if exists) or README

Document that WebCopy now preserves full runtime interactivity, including WebGL.

### Step 8.3: Commit & Push

```bash
git add -A
git commit -m "feat: full runtime archive mode — 100% JS/WebGL preservation for perfect offline replicas"
git push origin main
```

---

## Spec Coverage Checklist

| Spec Section | Implementing Task |
|---|---|
| Enable WebGL (3.3.1) | Task 1 |
| Remove JS-stripping (3.3.4) | Task 2 |
| Runtime asset collector (3.3.1, 3.3.2) | Task 3 |
| Import map injection (3.3.3) | Task 4 |
| LiquidGlass test case (6.1) | Task 5 |
| Script preservation test (6.1) | Task 6 |
| Visual regression test (6.2) | Task 7 |
| Error handling (5) | Handled in existing code, no changes needed |
| Performance (8) | Handled by increased heap, parallel flush |

---

## Placeholder Scan

- [x] No "TBD" or "TODO" in any step
- [x] Every step has exact file paths
- [x] Every step has code blocks for code changes
- [x] Every step has exact commands with expected output
- [x] No vague instructions like "add appropriate error handling"

---

*Plan complete. Ready for execution.*
