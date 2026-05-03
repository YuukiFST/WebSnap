package main

import (
	"os"
	"strings"
	"testing"
)

func TestIntegrationShopifyEditions(t *testing.T) {
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

	workDir := "downloads/test_shopify"
	os.RemoveAll(workDir)

	d := NewWebsiteDownloader("https://www.shopify.com/ca/editions/winter2026", workDir, func(msg string) {
		t.Log(msg)
	})

	if err := d.Process(tabCtx); err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	htmlData, err := os.ReadFile(workDir + "/index.html")
	if err != nil {
		t.Fatalf("index.html not found: %v", err)
	}

	content := string(htmlData)
	checks := []string{
		"Winter",
		"Sidekick",
		"Agentic",
		"Online",
		"Retail",
		"Marketing",
		"Checkout",
		"Operations",
		"Shop app",
		"B2B",
		"Finance",
		"Shipping",
		"Developer",
	}

	missing := []string{}
	for _, check := range checks {
		if !strings.Contains(content, check) {
			missing = append(missing, check)
		}
	}

	if len(missing) > 0 {
		t.Fatalf("Missing sections: %v", missing)
	}

	t.Logf("Shopify test passed — %d bytes HTML", len(htmlData))
}
