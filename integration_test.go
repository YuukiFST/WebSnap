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
