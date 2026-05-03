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
	rootCtx     context.Context
	rootCancel  context.CancelFunc
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
		chromedp.Flag("js-flags", "--max_old_space_size=512 --max_semi_space_size=32"),
		chromedp.Flag("memory-model", "low"),
		chromedp.Flag("force-device-scale-factor", "1"),
		chromedp.Flag("max-texture-size", "4096"),
		chromedp.Flag("disable-features", "IsolateOrigins,site-per-process"),
		chromedp.UserAgent("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"),
		chromedp.WindowSize(1366, 768),
	)

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)

	rootCtx, rootCancel := chromedp.NewContext(allocCtx)
	if err := chromedp.Run(rootCtx, chromedp.EmulateViewport(1366, 768)); err != nil {
		rootCancel()
		allocCancel()
		return nil, fmt.Errorf("viewport: %w", err)
	}

	bm := &BrowserManager{
		allocCtx:    allocCtx,
		allocCancel: allocCancel,
		rootCtx:     rootCtx,
		rootCancel:  rootCancel,
		healthy:     true,
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

	if bm.rootCancel != nil {
		bm.rootCancel()
	}
	if bm.allocCancel != nil {
		bm.allocCancel()
	}

	newBM, err := NewBrowserManager()
	if err != nil {
		return err
	}

	bm.allocCtx = newBM.allocCtx
	bm.allocCancel = newBM.allocCancel
	bm.rootCtx = newBM.rootCtx
	bm.rootCancel = newBM.rootCancel
	bm.healthy = newBM.healthy

	return nil
}

func (bm *BrowserManager) Shutdown() {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if bm.rootCancel != nil {
		bm.rootCancel()
	}
	if bm.allocCancel != nil {
		bm.allocCancel()
	}
	bm.healthy = false
}
