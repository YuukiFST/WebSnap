package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
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

	d := NewWebsiteDownloader("https://httpbin.org", workDir, func(msg string) {
		t.Log(msg)
	})

	if err := d.Process(tabCtx); err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	htmlData, err := os.ReadFile(workDir + "/index.html")
	if err != nil {
		t.Fatal("index.html not found")
	}

if len(htmlData) < 1000 {
    t.Fatal("index.html too small — likely incomplete capture")
}

	content := string(htmlData)
	if !strings.Contains(content, "<html") {
		t.Fatal("index.html missing <html> tag")
	}
	if !strings.Contains(content, "<body") {
		t.Fatal("index.html missing <body> tag")
	}

	assetsDir := workDir + "/assets"
	entries, err := os.ReadDir(assetsDir)
	if err != nil {
		t.Fatalf("assets dir not found: %v", err)
	}

	os.RemoveAll(workDir)
	t.Logf("Accuracy test passed — %d assets, %d bytes HTML", len(entries), len(htmlData))
}

func TestHeavyPageDoesNotCrash(t *testing.T) {
	bm, err := NewBrowserManager()
	if err != nil {
		t.Skipf("Cannot start browser: %v", err)
	}
	defer bm.Shutdown()

	workDir := "downloads/test_heavy"
	os.RemoveAll(workDir)

	d := NewWebsiteDownloader("https://example.com", workDir, func(msg string) {
		t.Log(msg)
	})

	tabCtx, tabCancel := bm.NewTab()
	defer tabCancel()

	processCtx, processCancel := context.WithTimeout(tabCtx, 30*time.Second)
	defer processCancel()

	err = d.Process(processCtx)
	if err != nil {
		t.Fatalf("process failed: %v", err)
	}

	// Verify output was produced
	if _, err := os.Stat(workDir + "/index.html"); os.IsNotExist(err) {
		t.Fatal("index.html was not created")
	}

	os.RemoveAll(workDir)
	t.Log("Heavy page crash regression test passed")
}

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
