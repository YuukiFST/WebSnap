package main

import (
	"bytes"
	"context"
	"image/png"
	"os"
	"path/filepath"
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
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		t.Fatalf("failed to get absolute path: %v", err)
	}
	replicaPath := "file:///" + filepath.ToSlash(absWorkDir) + "/index.html"
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
			diff += uint64(absInt(int(r1)-int(r2)) + absInt(int(g1)-int(g2)) + absInt(int(b1)-int(b2)))
		}
	}

	maxDiff := uint64(width * height * 3 * 65535)
	if maxDiff == 0 {
		return 1.0
	}
	return 1.0 - float64(diff)/float64(maxDiff)
}

func absInt(a int) int {
	if a < 0 {
		return -a
	}
	return a
}
