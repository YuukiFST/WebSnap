package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStoreNetworkResourceRespectsCap(t *testing.T) {
	d := NewWebsiteDownloader("http://example.com", t.TempDir(), func(string) {})

	for i := 0; i < maxTotalNetworkResources; i++ {
		url := fmt.Sprintf("http://example.com/img%d.png", i)
		if !d.storeNetworkResource(url, []byte("data"), "image/png") {
			t.Errorf("expected store to succeed at index %d", i)
			return
		}
	}

	// 5001st should fail
	if d.storeNetworkResource("http://example.com/extra.png", []byte("data"), "image/png") {
		t.Error("expected store to fail when cap is reached")
	}

	if len(d.networkResources) != maxTotalNetworkResources {
		t.Errorf("expected %d resources, got %d", maxTotalNetworkResources, len(d.networkResources))
	}
}

func TestDownloadMissingResources(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("fake-image-data"))
	}))
	defer ts.Close()

	d := NewWebsiteDownloader(ts.URL, t.TempDir(), func(string) {})
	d.baseURL = ts.URL
	d.httpClient = newCookiedHTTPClient(nil, ts.URL)

	urls := []string{ts.URL + "/image.png"}
	d.downloadMissingResources(urls)

	d.networkResourcesMu.RLock()
	defer d.networkResourcesMu.RUnlock()
	if len(d.networkResources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(d.networkResources))
	}
	res, ok := d.networkResources[ts.URL+"/image.png"]
	if !ok {
		t.Fatal("expected resource to be stored")
	}
	if string(res.body) != "fake-image-data" {
		t.Errorf("expected body 'fake-image-data', got %q", string(res.body))
	}
	if res.contentType != "image/png" {
		t.Errorf("expected content type 'image/png', got %q", res.contentType)
	}
}

func TestDownloadMissingResourcesSkipsExisting(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not be called for existing resource")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	d := NewWebsiteDownloader(ts.URL, t.TempDir(), func(string) {})
	d.baseURL = ts.URL
	d.httpClient = newCookiedHTTPClient(nil, ts.URL)
	d.storeNetworkResource(ts.URL+"/existing.png", []byte("existing"), "image/png")

	urls := []string{ts.URL + "/existing.png"}
	d.downloadMissingResources(urls)

	d.networkResourcesMu.RLock()
	defer d.networkResourcesMu.RUnlock()
	if len(d.networkResources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(d.networkResources))
	}
	res := d.networkResources[ts.URL+"/existing.png"]
	if string(res.body) != "existing" {
		t.Errorf("expected body unchanged, got %q", string(res.body))
	}
}

func TestDownloadMissingResourcesEmpty(t *testing.T) {
	d := NewWebsiteDownloader("http://example.com", t.TempDir(), func(string) {})
	d.downloadMissingResources(nil)
	if len(d.networkResources) != 0 {
		t.Errorf("expected 0 resources, got %d", len(d.networkResources))
	}
	d.downloadMissingResources([]string{})
	if len(d.networkResources) != 0 {
		t.Errorf("expected 0 resources, got %d", len(d.networkResources))
	}
}

func TestDownloadMissingResourcesNoHTTPClient(t *testing.T) {
	d := NewWebsiteDownloader("http://example.com", t.TempDir(), func(string) {})
	d.httpClient = nil
	urls := []string{"http://example.com/image.png"}
	d.downloadMissingResources(urls)
	if len(d.networkResources) != 0 {
		t.Errorf("expected 0 resources when httpClient is nil, got %d", len(d.networkResources))
	}
}

func TestCollectDynamicURLsHandlesError(t *testing.T) {
	bm, err := NewBrowserManager()
	if err != nil {
		t.Skipf("Cannot start browser: %v", err)
	}
	defer bm.Shutdown()

	ctx, cancel := bm.NewTab()
	defer cancel()

	d := NewWebsiteDownloader("about:blank", t.TempDir(), func(string) {})

	// Should not panic on a blank page
	urls := d.collectDynamicURLs(ctx)
	if urls == nil {
		t.Fatal("expected empty slice, got nil")
	}
	if len(urls) != 0 {
		t.Fatalf("expected 0 URLs on blank page, got %d", len(urls))
	}
}
