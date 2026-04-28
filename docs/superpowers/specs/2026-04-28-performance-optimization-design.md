# Performance Optimization — Design Spec

**Date:** 2026-04-28
**Scope:** Reduce total download time (URL → ZIP) by 30-50% via architectural optimizations.
**Stack:** Python, Flask, Playwright, BeautifulSoup. No framework/language change.
**Deploy target:** Free-tier platforms (Render, Railway), ~512MB RAM.

## Current State

| Step | Time | Bottleneck |
|---|---|---|
| Launch Chromium (cold start) | 2-4s | New browser per request |
| Page navigation + resource capture | 5-30s | Sequential waits, network |
| Fallback resource downloads | 1-10s | Sync `requests.Session`, one at a time |
| HTML processing (BS4) | 0.5-3s | CPU-bound, single-threaded |
| ZIP creation (disk I/O) | 0.5-5s | `shutil.make_archive` writes temp file |
| **Total** | **~10-50s** | — |

## Architecture

```
┌──────────────────────────────────────────────────┐
│  app.py                                           │
│  ┌──────────┐  ┌──────────────┐  ┌─────────────┐ │
│  │Flask HTTP│  │SSE Stream    │  │Session Mgmt │ │
│  └────┬─────┘  └──────────────┘  └──────┬──────┘ │
│       │                                 │         │
│  ┌────▼─────────────────────────────────▼──────┐  │
│  │         DownloadWorker (per-request)         │  │
│  │   ┌────────────┐  ┌────────────┐  ┌───────┐ │  │
│  │   │Navigate    │→│Process HTML│→│Zip    │ │  │
│  │   │& Capture   │  │& Assets   │  │& Serve│ │  │
│  │   └──────┬─────┘  └────────────┘  └───────┘ │  │
│  └─────────┬┼───────────────────────────────────┘  │
│            ││                                      │
└────────────│┼──────────────────────────────────────┘
             ││
    ┌────────▼▼────────┐    ┌──────────────────────┐
    │ BrowserManager   │    │ AsyncResourceClient  │
    │ (singleton)      │    │ (httpx)              │
    │                  │    │                      │
    │ - warm browser   │    │ - parallel downloads │
    │ - isolated ctxs  │    │ - connection pool    │
    │ - health check   │    │ - timeout control    │
    └──────────────────┘    └──────────────────────┘
```

## Components

### 1. BrowserManager (`app.py`, ~60 lines)

Singleton that keeps one Chromium instance alive across requests.

**Public API:**
- `BrowserManager.start()` — launches browser with existing args (headless, no-sandbox, disable-gpu, etc.)
- `BrowserManager.get_context()` → `BrowserContext` — creates isolated incognito context per download
- `BrowserManager.release_context(ctx)` — closes context, frees ~50MB
- `BrowserManager.healthy` → `bool` — `browser.is_connected()` check
- `BrowserManager.restart()` — if unhealthy, relaunch
- `BrowserManager.shutdown()` — close everything (SIGTERM handler)

**Concurrency model:** `threading.Lock` serializes access. Only one download at a time (free-tier memory constraint: ~250MB idle browser + ~100MB per active download). Workers that arrive while busy wait on the lock.

**Isolation:** Each download gets a fresh `browser.new_context()` (incognito). Cookies, localStorage, and network cache are independent. No cross-session leakage.

**Lifecycle:** Started at module import. Health-checked before each `get_context()` call. Restarted transparently if dead. Shut down on `atexit` and SIGTERM.

### 2. AsyncResourceClient (`downloader.py`, ~40 lines)

Replaces sync `requests.Session` fallback with `httpx.AsyncClient` for parallel downloads.

**New dependency:** `httpx` (add to `pyproject.toml` and `requirements.txt`).

**Public API:**
- `AsyncResourceClient(cookies=...)` — constructor, configures async client with browser cookies and same UA/headers
- `async download_many(urls: list[str])` → `dict[str, bytes]` — downloads up to 8 URLs concurrently via `asyncio.gather()`. Returns successful results keyed by URL. Silently skips failures.
- `async download_one(url: str)` → `bytes | None` — single URL fallback.

**Integration:** In `_get_resource()`, collect all uncached URLs into a list. After the collection pass, call `download_many(urls)` in one batch. Then resolve each URL from the result dict.

**Connection limits:** `httpx.Limits(max_connections=10, max_keepalive_connections=5)`.

### 3. StreamingZip (`downloader.py`, ~30 lines)

Creates ZIP in memory via `BytesIO`, eliminating disk I/O.

**Function:** `zip_directory_to_memory(folder_path: str) -> BytesIO`
- Walks `folder_path`, adds each file to `zipfile.ZipFile(BytesIO(), mode='w', compression=ZIP_DEFLATED)`
- Returns the `BytesIO` buffer (seeked to 0)

**Change in `app.py`:** `download_file()` route uses `send_file(buf, mimetype='application/zip', download_name=filename)` instead of `send_file(path)`. No `.zip` file on disk; no post-download cleanup needed for the zip itself.

### 4. Download Timer (`app.py` + `templates/index.html`)

Measures wall-clock time from URL submission to ZIP delivery.

**Backend:**
- `download_results[session_id]['started_at']` — set at `start_download()` (already exists)
- `download_results[session_id]['completed_at']` — set when status transitions to `complete` or `error`
- Duration is sent as an SSE event: `event: done\ndata: {"status":"complete","duration":12.34}\n\n`

**Frontend:**
- On SSE `done` event with `complete` status, parse `duration` from data
- Display: `⏱️ Tempo total: 12.3 segundos` in the success message or as a final log entry

## Files Changed

| File | Changes |
|---|---|
| `app.py` | Add `BrowserManager` class, modify `process_download()` to use it, modify `download_file()` for streaming ZIP, add timer fields, add `httpx` import |
| `downloader.py` | Add `AsyncResourceClient`, replace `requests.Session` usage, add `zip_directory_to_memory()`, make `_download_fallback()` async-aware |
| `templates/index.html` | Add timer display logic in SSE `done` handler |
| `pyproject.toml` | Add `httpx` dependency |
| `requirements.txt` | Add `httpx` |

## What Does NOT Change

- All Playwright capture logic (navigation, retries, anti-bot detection, stealth)
- All BeautifulSoup HTML processing (CSS rewrite, scroll fixes, framework detection, iframe extraction, lazy loading)
- Session management (queues, results dict, lock, janitor)
- SSE streaming endpoint
- Dockerfile, Render/Railway config, entrypoint

## Expected Gains

| Optimization | Reduction |
|---|---|
| BrowserManager (warm browser) | -2 to -4s |
| AsyncResourceClient (parallel downloads) | -1 to -5s |
| StreamingZip (in-memory ZIP) | -0.5 to -2s |
| **Total** | **30-50% faster** (~5-25s typical) |

## Risks

- **BrowserManager health:** Chromium may crash under memory pressure. Mitigated by health check + auto-restart before each download.
- **Serialized downloads:** Only one concurrent download. Acceptable for free tier. Queue is implicit (lock wait).
- **httpx memory:** Parallel downloads hold multiple response bodies in memory. Mitigated by 8MB-per-asset cap and 8-concurrent limit.
- **ZIP in memory:** Very large sites (>100MB assets) could OOM. Mitigated by existing 8MB-per-asset cap. If site has >50 large assets, ZIP may spill — add a fallback to disk-based ZIP if total exceeds 200MB threshold (future enhancement, not in this spec).
