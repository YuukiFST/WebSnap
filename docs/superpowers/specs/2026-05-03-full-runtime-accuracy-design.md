# Full Runtime Accuracy — Design Spec

**Date:** 2026-05-03
**Status:** Draft — pending implementation plan
**Goal:** WebCopy generates a self-contained `index.html` + `assets/` ZIP that replicates any target website with 100% visual fidelity and full interactivity (hover, click, scroll, animations, WebGL, drag) when opened offline.

---

## 1. Problem Statement

Current WebCopy captures static DOM + assets and aggressively strips JavaScript to "fix" offline viewing. This breaks modern sites that rely on JS runtime for rendering, including:

- **WebGL / Canvas** (e.g. LiquidGlass) — JS creates WebGL contexts and renders shaders. Stripping JS leaves empty `<canvas>` elements.
- **SPA frameworks** (Next.js, Gatsby, Nuxt) — framework scripts hydrate DOM. Removing them kills interactivity.
- **Animation libraries** (GSAP, ScrollTrigger, Lottie, AOS) — detected and "neutralized", destroying scroll-driven animations and scrollytelling.
- **Scroll libraries** (Lenis, Locomotive Scroll) — scroll-blocking CSS is force-injected, breaking custom scroll behavior.
- **DOM transform libraries** (CircleType, Splitting, Typed.js) — scripts are removed to prevent "double execution", breaking text effects.

The result: a broken static snapshot instead of a functional replica.

---

## 2. Objective & Success Criteria

| Criterion | Definition |
|---|---|
| **Visual fidelity** | Opening `index.html` offline must match the target site's appearance at capture time. No missing images, fonts, CSS, or canvas content. |
| **Interactivity** | Hover, click, scroll, drag, and animation behaviors must work offline. WebGL shaders must render. |
| **Self-contained** | Zero external network requests. All assets served from local `assets/` folder via relative paths. |
| **File protocol** | Must work when opened directly as `file://` (no server, no Service Worker). |
| **Test case** | `https://liquid-glass.ybouane.com/` must show glass buttons with refraction, blur, and drag interactions offline. |

---

## 3. Architecture

### 3.1 Philosophy Shift

From **static snapshot** to **full runtime archive**.

The offline replica is not a photograph. It is the original website's code, frozen in time, with all asset URLs rewritten to local disk paths. The browser executes the original JS, which recreates WebGL contexts, attaches event listeners, and plays animations — but loads images, fonts, CSS, and module scripts from disk instead of the internet.

### 3.2 High-Level Components

```
┌─────────────────────────────────────────────────────────────┐
│                     WebCopy Server (Go)                      │
│  ┌─────────────┐  ┌─────────────────┐  ┌─────────────────┐  │
│  │  HTTP API   │  │  BrowserManager │  │  RewriterEngine │  │
│  │  (existing) │  │  (modified)     │  │  (new)          │  │
│  └──────┬──────┘  └────────┬────────┘  └─────────────────┘  │
└─────────┼──────────────────┼────────────────────────────────┘
          │                  │
          │    POST /start-download
          │─────────────────>│
          │                  │
          │                  ▼
          │         ┌─────────────────┐
          │         │  ChromeDP Tab   │
          │         │  (Archive Mode) │
          │         └────────┬────────┘
          │                  │
          │    Navigate      │    Network intercept
          │─────────────────>│─────────────────────> Asset Pipeline
          │                  │                        (download + save)
          │    Scroll/Wait   │
          │─────────────────>│
          │                  │
          │    OuterHTML     │    Runtime Script
          │<─────────────────│    (collect dynamic URLs)
          │                  │
          │    Get CSS Text  │
          │<─────────────────│
          │                  │
          ▼                  ▼
    ┌─────────────────────────────────┐
    │      RewriterEngine.Process     │
    │  • Rewrite all paths to local   │
    │  • Preserve 100% of scripts     │
    │  • Inject Import Map            │
    │  • Inline critical CSS imports  │
    └─────────────────────────────────┘
                  │
                  ▼
         ┌──────────────┐
         │  ZIP output  │
         │  index.html  │
         │  assets/     │
         └──────────────┘
```

### 3.3 Component Details

#### 3.3.1 Browser Capturer (ChromeDP — Archive Mode)

**Remove** these flags from `BrowserManager`:
- `disable-webgl`
- `disable-3d-apis`
- `disable-accelerated-2d-canvas`

**Add** these flags:
- `js-flags`: `--max_old_space_size=2048` (up from 512)
- Remove `memory-model=low`

**Network interception strategy:**
- Keep `setupNetworkCapture` — intercepts `EventResponseReceived` + `EventLoadingFinished` for all resource types.
- Keep `setupBlockedResources` — blocks only WebSocket and EventSource (these cannot be archived).
- **Add** Fetch domain interception for `XHR`, `Fetch` API calls — capture JSON/data responses and save them as static files, then rewrite the fetch URL in JS to point to the local file.

**Runtime instrumentation (new):**
Inject a script after page load that collects URLs not caught by network interception:
- `document.querySelectorAll('*[src]')`, `link[href]` — already caught, but double-check.
- `document.fonts` — enumerate loaded fonts, get their `src` URLs.
- CSS rules with `background-image`, `border-image`, `cursor`, `list-style-image`, `@font-face src` — parse stylesheets via `document.styleSheets`.
- Dynamic imports: monkey-patch `import()` to log URLs.
- Workers: monkey-patch `new Worker()` to log URLs.
- `fetch()` and `XMLHttpRequest`: log URLs.
- Canvas WebGL textures: not directly accessible, but their source images are already in the DOM.

#### 3.3.2 Asset Pipeline

**Passive capture** (existing):
- Network listener stores all bodies in `networkResources` map.
- On `Process` end, flush to disk in parallel (sem 4).

**Active capture** (new):
- After scroll/wait, execute runtime script to collect additional URLs.
- Download missing URLs via `httpClient` (with cookies).
- Store in same `networkResources` map, then flush.

**Resource limits:**
- Individual resource: 8MB max.
- Total resources: 5000 max.
- CSS `@import` recursion: depth 5 max.

#### 3.3.3 Rewriter Engine

**HTML Rewriting:**
- `processStylesheets`: rewrite `href` to local path. Inline `@import` recursively.
- `processInlineStyles`: rewrite `url()` in `<style>` tags.
- `processScripts`: rewrite `src`. **Do not remove** any script.
- `processImages`: rewrite `src`, `srcset`, `data-src`, `poster`, etc.
- `processInlineStyleAttrs`: rewrite `url()` in `[style]` attributes.
- `processFavicons`: rewrite `href`.
- `processMetaImages`: rewrite `content`.
- `processBackgroundData`: rewrite `data-background`, `data-bg`.
- `processLottieAssets`: rewrite `data-src`, `data-animation-path`, `src` on lottie elements.
- `fixNavigationLinks`: keep existing behavior (replace internal links with `#`).
- **New**: `injectImportMap()` — inject `<script type="importmap">` mapping CDN URLs to local paths.
- **New**: `processDynamicFetchURLs()` — scan inline JS for `fetch('...')` and `new Request('...')`, rewrite URLs to local paths if the response was captured.

**CSS Rewriting:**
- `rewriteCSSURLs`: rewrite `url()` to local path.
- `processCSSImports`: inline or rewrite `@import` URLs.

**JS Rewriting (new):**
- Parse inline `<script>` text for `import` statements (ES modules).
- Rewrite `from 'https://cdn...'` to `from './assets/...'` using import map or direct rewrite.
- Rewrite `fetch('/api/data')` to `fetch('./assets/api_data_...json')` if the response was captured.
- **Note:** Full JS AST parsing is out of scope for v1. Use regex-based rewriting for common patterns. Accept that some dynamic URLs may not be caught.

#### 3.3.4 Script Preservation (Mudanças no processor.go)

**Remove entirely:**
- `handleSPAFrameworks` — do not remove Next.js, Gatsby, Nuxt scripts.
- `neutralizeDOMTransformScripts` — do not remove CircleType, Splitting, etc.
- `fixScrollBlocking` — do not force `overflow-y: auto` or remove Lenis classes.
- `injectAnimationFallbacks` — do not inject CSS that overrides AOS, ScrollReveal, WOW.
- `handleWebflowOffline` — keep the `w-mod-js` / `w-mod-ix` class injection (this is harmless and helps Webflow), but remove the opacity/visibility CSS override.

**Keep (harmless or helpful):**
- `removeWrapperIframes` — preview iframes are not part of the target site.
- `ensureWebFontLinks` — Google Fonts fallback is fine.
- `processLottieAssets` — asset rewriting is still needed.

**Add (new):**
- `injectImportMap()` — maps captured CDN module URLs to local paths.

---

## 4. Data Flow

1. User POST `/start-download` with URL.
2. `sessionManager.create()` starts goroutine `runDownload()`.
3. `BrowserManager.NewTab()` creates ChromeDP context with Archive flags.
4. `navigateWithRetries()` loads page. JS executes. WebGL contexts initialize.
5. `setupNetworkCapture()` intercepts all network responses, stores bodies in memory.
6. `scrollPage()` scrolls to trigger lazy loading. Network capture continues.
7. `waitForAnimationsToSettle()` waits 3s max for CSS animations to finish.
8. `extractIframeContent()` handles wrapper pages.
9. `captureBaseURL()` gets final URL after redirects.
10. `getCookies()` creates `httpClient` with session cookies.
11. `fetchCSSTextFromBrowser()` fetches stylesheet text from browser cache.
12. **New**: `executeRuntimeCollector()` injects JS to find fonts, dynamic imports, workers, fetch URLs, and downloads missing assets.
13. `chromedp.OuterHTML("html", &finalHTML)` captures DOM.
14. `processHTML(finalHTML)`:
    - Parse with goquery.
    - Detect animation frameworks (for logging only, no neutralization).
    - Rewrite all asset paths.
    - Inject import map.
    - Preserve all scripts.
15. `flushResources()` saves all captured assets to `assets/` in parallel.
16. `zipDirectory()` creates ZIP.
17. Session status -> `complete`. User downloads ZIP.

---

## 5. Error Handling

| Scenario | Strategy |
|---|---|
| WebGL context lost / Chrome crash | Detect via `bm.Healthy()`, restart browser, retry once. If fails again, return error. |
| Individual resource > 8MB | Skip, log warning. Continue processing. |
| Total resources > 5000 | Set `capReached = true`, continue. Log warning: "Replica may be incomplete." |
| JS dynamic import URL not caught | Import map covers common CDN patterns. Uncaught dynamic URLs will 404 offline, but page should still render baseline content. |
| Font CORS blocked during capture | Browser caches font if used. If not, fallback to Google Fonts injection or system fonts. |
| CSS `url()` not resolvable | Keep original URL. Browser shows broken image, does not crash. |
| Timeout 150s | Return error. Clean up workDir. |
| Canvas screenshot not possible | Not needed — WebGL runs offline with preserved JS. |

---

## 6. Testing Strategy

### 6.1 Integration Tests

| Test | Behavior Verified |
|---|---|
| `TestIntegration_LiquidGlass` | Captures liquid-glass.ybouane.com. Asserts `<canvas>` exists in output. Asserts WebGL shader assets (JS files) saved in `assets/`. |
| `TestIntegration_SimpleSite` | example.com baseline. Asserts `index.html` created, assets saved. |
| `TestIntegration_SPA` | Next.js or Gatsby site. Asserts framework scripts NOT removed from HTML. |
| `TestIntegration_HeavyPage` | Page with >30k DOM nodes. Asserts process completes without crash. |

### 6.2 Accuracy Test (Visual Regression)

- Capture target site.
- Open `index.html` in fresh Chrome headless (CDP) at 1366x768.
- Take screenshot.
- Compare with screenshot of original site (taken during capture).
- Use perceptual hash or pixel diff. Threshold: 95% similarity.
- Run for LiquidGlass as primary benchmark.

### 6.3 Unit Tests

| Test | Behavior Verified |
|---|---|
| `TestRewriteEngine` | Absolute URLs rewritten to `assets/...`. Relative paths preserved correctly. |
| `TestImportMapInjection` | Import map contains mappings for captured CDN URLs. |
| `TestCSSImportInlining` | `@import url("...")` recursively inlined up to depth 5. |
| `TestScriptPreservation` | SPA framework scripts remain in HTML after processing. |

---

## 7. Scope & Out of Scope

**In scope:**
- Removing JS-stripping logic.
- Enabling WebGL and all rendering features in ChromeDP.
- Preserving 100% of scripts and interactivity.
- Capturing dynamic assets (fetch, XHR, dynamic imports, workers).
- Rewriting all asset paths to local disk.
- Injecting import maps for ES modules.
- Visual regression test for LiquidGlass.

**Out of scope (v1):**
- Full AST-based JS parsing and rewriting. Regex-based only.
- WebSocket / Server-Sent Events (impossible to archive without a server).
- Real-time data feeds that require live backend.
- User authentication / session persistence across captures.
- Sites that require user interaction during capture (e.g. login forms).

---

## 8. Performance Considerations

- **Memory**: ChromeDP JS heap increased to 2GB. Network resources capped at 5000.
- **Speed**: Parallel asset flush (4 workers). Passive + active capture in one pass.
- **ZIP size**: Larger than current output because JS is not stripped. Acceptable trade-off for accuracy.
- **Scroll**: Keep scroll optimization (abort at 30k nodes), but do not force CSS overrides.

---

## 9. Risks & Mitigations

| Risk | Mitigation |
|---|---|
| Larger ZIPs, slower downloads | Expected trade-off. Document to users. |
| Security: executing arbitrary JS offline | Same risk as opening any HTML file. User is downloading a replica of a public site. |
| Some dynamic URLs still 404 offline | Acceptable degradation. Baseline content renders. |
| WebGL crashes on low-end machines | Browser handles this. Not our concern. |
| Import map does not cover all module patterns | Regex rewrites + import map covers 90%+ of common patterns (jsdelivr, unpkg, skypack, esm.sh). |

---

## 10. File Changes Summary

| File | Change |
|---|---|
| `browsermanager.go` | Remove `disable-webgl`, `disable-3d-apis`, `disable-accelerated-2d-canvas`. Increase JS heap to 2GB. |
| `downloader.go` | Add `executeRuntimeCollector()`. Monkey-patch `import()`, `fetch()`, `Worker()`. |
| `processor.go` | **Remove** `handleSPAFrameworks`, `neutralizeDOMTransformScripts`, `fixScrollBlocking`, `injectAnimationFallbacks`. **Add** `injectImportMap()`, `processDynamicFetchURLs()`. |
| `integration_test.go` | Add `TestIntegration_LiquidGlass`, `TestIntegration_SPA`. Update accuracy test threshold. |
| `templates/index.html` | No change (frontend remains same). |

---

## 11. Checklist Before Implementation

- [ ] Remove all JS-stripping functions from `processor.go`.
- [ ] Enable WebGL in `browsermanager.go`.
- [ ] Implement runtime asset collector in `downloader.go`.
- [ ] Implement import map injection in `processor.go`.
- [ ] Add visual regression test for LiquidGlass.
- [ ] Run all integration tests. All must pass.
- [ ] Benchmark capture time on LiquidGlass. Target: <60s.

---

*End of design spec.*
