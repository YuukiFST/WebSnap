package main

import (
	"fmt"
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
