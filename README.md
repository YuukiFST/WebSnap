# WebSnap

Grab clean, offline-ready copies of any website. This tool handles the heavy lifting of capturing JavaScript-rendered content, fixing scroll locks, and packaging everything into a single ZIP.

## Features

I built this to solve the common headache of "saving" a modern website only to find it broken or blank offline.

*   **Full Asset Capture:** Grabs HTML, CSS, JS, images, and fonts.
*   **JS Rendering:** Uses Playwright to render pages exactly as a user sees them.
*   **Lazy Load Support:** Automatically scrolls the page to trigger image loading.
*   **ZIP Export:** One-click download for the entire site.
*   **Real-time Logs:** See exactly what the engine is doing in a terminal-style UI.
*   **Auto-Cleanup:** Temporary files don't hang around; the janitor thread wipes them regularly.
*   **Smart Fixes:** Injects CSS overrides to fix scroll-blocking and visibility issues common in modern site builders.

## Local Setup

You'll need Python 3.11+ and `uv`.

```bash
# Sync dependencies
uv sync

# Install the browser engine
uv run playwright install chromium

# Start the server
uv run python app.py
```

Access the UI at `http://localhost:5001`.

## How it works

I tried to keep the architecture straightforward:

1.  **Capture:** Playwright opens the URL, mimics a real user, and intercepts network responses.
2.  **Process:** BeautifulSoup parses the HTML, rewrites URLs to point to local assets, and strips out framework hydration scripts (Next.js, Gatsby, etc.) that would break offline.
3.  **Repair:** It detects scroll-blocking libraries like Lenis or Locomotive and injects a CSS "safety net" to ensure the page is actually scrollable when viewed from your disk.
4.  **Package:** Everything is bundled into a ZIP file.

## Technical Details

The engine handles several edge cases that standard "save page as" tools miss:
*   **Animation Frameworks:** Detects and handles Webflow IX2, GSAP, and Framer Motion artifacts.
*   **SPA Hydration:** Aggressively cleans up Next.js/Nuxt data to prevent the browser from trying to "rehydrate" a local file.
*   **Iframe Extraction:** Pulls content out of `srcdoc` or wrapper iframes (common in site builders like Aura).

## License

Personal and educational use. If you find it useful, let me know.
