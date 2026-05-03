package main

import (
	"testing"
	"time"

	"github.com/chromedp/chromedp"
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

	_, cancel1 := bm.NewTab()
	defer cancel1()

	if !bm.Healthy() {
		t.Fatal("Expected healthy with one tab")
	}

	cancel1()
	time.Sleep(100 * time.Millisecond)

	_, cancel2 := bm.NewTab()
	defer cancel2()

	if !bm.Healthy() {
		t.Fatal("Expected healthy after closing tab and opening new one")
	}
}
