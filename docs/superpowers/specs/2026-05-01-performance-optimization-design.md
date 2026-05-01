# Performance Optimization — Design Spec

**Date:** 2026-05-01
**Scope:** Reduce CPU usage during page capture, maintain/exceed current speed, zero accuracy degradation.
**Stack:** Go, chromedp, goquery. No language/framework change.
**Deploy target:** Local machine (Windows/headless Chromium), 1 concurrent download.

## Current State

| Step | Time | CPU | Bottleneck |
|---|---|---|---|
| Launch Chromium (cold) | 2-4s | Medium | New browser process per request |
| Page navigation | 5-30s | High | Full render of animations/3D/canvas |
| Preloader wait | 0-10s | High | Polling + active animations |
| Scroll (15 steps × 400ms) | 6s | High | Layout/paint per scroll step |
| Animation settle | 5s | Medium | `getAnimations()` polling |
| HTML processing | 0.5-3s | Low | goquery CPU-bound |
| Asset saving (sequential) | 1-3s | Low | Disk I/O |
| ZIP creation | 0.5-3s | Low | Disk I/O |
| **Total** | **~8-50s** | **Up to 90%** | — |

## Architecture

```
Before:
  New Chrome → Navigate → Sleep → Preloader → Scroll → Anims → Capture → Save Assets(seq) → ZIP

After:
  BrowserManager(warm Chrome) → Navigate(optimized flags) → Smart Scroll → Smart Anims
    → Capture → CSS(parallel) → Save Assets(parallel) → ZIP
```

## Components

### 1. Chromium Flags (downloader.go)

Add resource-saving flags. No accuracy impact — captured HTML comes from DOM, not rendered pixels.

**Flags to add:**
- `--disable-webgl` — blocks WebGL rendering (Shopify uses this)
- `--disable-3d-apis` — blocks Three.js, GPU 3D
- `--disable-accelerated-2d-canvas` — CPU canvas rendering instead of GPU
- `--headless=new` — newer headless mode, less overhead

**Viewport change:** 1920×1080 → 1366×768. Still desktop layout. 47% fewer pixels to render.

### 2. Speed Timer Reductions (downloader.go)

| Location | Before | After | Rationale |
|---|---|---|---|
| Post-navigation settle | 2000ms | 1000ms | Page already painted |
| Nav success sleep | 2500ms | 1500ms | Resources already streaming |
| Scroll inter-step delay | 400ms | 200ms | Lazy triggers fire nearly instantly |
| Post-scroll settle | 500ms | 300ms | Layout already resolved |
| Post-scroll top restore | 500ms | 300ms | Simple scroll reset |

### 3. Smart Scroll Exit (downloader.go:390-465)

Max iterations: 15 → 10. Inter-step delay: 400ms → 200ms.
Early exit: stop after 2 consecutive `newHeight == totalHeight` iterations (no new lazy content triggering).

**Accuracy preservation:** Content that hasn't triggered by then wouldn't trigger with more scrolls either. Lazy load fires synchronously on scroll events.

### 4. Smart Animation Wait (downloader.go:613-630)

Deadline: 5s → 3s.
Loop exit: if `document.getAnimations().filter(a => a.playState === 'running').length` stays same for 3 consecutive polls, break immediately (infinite-looping animations).

Final settle sleep: 1s → 500ms.

**Accuracy preservation:** Existing CSS fallback injection forces `opacity: 1 !important; visibility: visible !important; transform: none !important` on all animation elements. Mid-animation capture = same visible result.

### 5. Parallel CSS Fetch (downloader.go:476-490)

Change sequential `for (const link of links) { await fetch(href); }` to `Promise.all(links.map(link => fetch(link.href))).`

Uses same `cache: 'force-cache'`. Zero accuracy difference.

### 6. BrowserManager (browsermanager.go + main.go)

Singleton that keeps one Chromium instance alive across requests.

**Public API:**
- `NewBrowserManager() *BrowserManager` — launches browser with optimized flags
- `(bm *BrowserManager) NewContext() (context.Context, context.CancelFunc)` — creates isolated incognito tab per download
- `(bm *BrowserManager) Healthy() bool` — checks browser process still alive
- `(bm *BrowserManager) Restart()` — relaunches if unhealthy
- `(bm *BrowserManager) Shutdown()` — SIGTERM handler

**Concurrency:** One tab at a time (matches `maxConcurrentDownloads = 1`). No lock needed beyond existing session mutex.

**Lifecycle:** Started on first download. Reused across requests. Shut down on process exit (signal handler).

### 7. Parallel Asset Save (downloader.go)

Change from sequential `saveResource()` calls to goroutine pool (N=4) for disk writes. `resourceCache` mutex-protected.

## Files Changed

| File | Changes |
|---|---|
| `downloader.go` | Add flags, reduce sleeps, smart scroll exit, smart anim exit, parallel CSS, parallel asset save |
| `browsermanager.go` | **New file.** BrowserManager singleton with warm start |
| `main.go` | Integrate BrowserManager, shutdown handler, pass browser context to downloader |
| `downloader.go:Process()` | Accept optional browser context from BrowserManager |

## What Does NOT Change

- All HTML processing logic (processor.go untouched)
- All animation framework detection
- All CSS/URL rewriting
- All iframe/SPA/Webflow handling
- ZIP creation
- Session management
- SSE streaming
- Frontend

## Expected Gains

| Optimization | CPU Reduction | Speed Gain |
|---|---|---|
| Browser flags (WebGL/3D/canvas) | 10-15% | — |
| Viewport reduction | 10-15% | — |
| Smart scroll early exit | 5-10% | 2-4s |
| Smart anim wait | 5-10% | 1-3s |
| Speed timers | — | 2-4s |
| CSS parallel fetch | — | 2-5s |
| BrowserManager warm start | — | 2-4s |
| Parallel asset save | — | 1-3s |
| **Total (heavy site)** | **35-45% lower CPU** | **10-20s faster** |
| **Total (simple site)** | **20-30% lower CPU** | **3-7s faster** |

## Integration Test

After all changes, run against `https://www.shopify.com/ca/editions/winter2026` and a simple site (e.g., `https://example.com`).

Verify:
1. index.html renders correctly in browser (same layout, images, fonts)
2. Assets folder has all expected files (no missing images/CSS/JS)
3. CPU stays below 70% on heavy site
4. Total time is measurably faster than before
