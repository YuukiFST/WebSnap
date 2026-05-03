package main

import (
	"os"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

func TestEnsureWebFontLinks(t *testing.T) {
	html := `<html><head>
		<script src="https://ajax.googleapis.com/ajax/libs/webfont/1.6.26/webfont.js"></script>
		<script>WebFont.load({  google: {    families: ["Inconsolata:400,700","Roboto:300,400"]  }});</script>
	</head><body></body></html>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}

	d := &WebsiteDownloader{logCallback: func(s string) {}}
	d.ensureWebFontLinks(doc)

	if doc.Find("link[href*='fonts.googleapis.com'][href*='Inconsolata'][href*='Roboto']").Length() == 0 {
		t.Error("Expected Google Fonts link element with Inconsolata and Roboto")
	}
}

func TestProcessCSSImportsStopsAtMaxDepth(t *testing.T) {
	d := NewWebsiteDownloader("http://example.com", t.TempDir(), func(string) {})
	// Circular import: a.css imports b.css, b.css imports a.css
	d.networkResources["http://example.com/a.css"] = &networkResource{
		body:        []byte(`@import url("b.css");`),
		contentType: "text/css",
	}
	d.networkResources["http://example.com/b.css"] = &networkResource{
		body:        []byte(`@import url("a.css");`),
		contentType: "text/css",
	}

	// Without a depth limit this would infinite-recurse / stack-overflow.
	// With the cap it should return successfully.
	result := d.processCSSImports(`@import url("a.css");`, "http://example.com", false, 0)
	if result == "" {
		t.Error("expected non-empty result")
	}
}

func TestScriptsPreserved(t *testing.T) {
	html := `<!DOCTYPE html><html><head><script src="/_next/static/chunks/main.js"></script></head><body></body></html>`
	os.RemoveAll("test_output")
	os.MkdirAll("test_output", 0755)
	d := NewWebsiteDownloader("https://example.com", "test_output", func(string) {})
	d.processHTML(html)

	result, err := os.ReadFile("test_output/index.html")
	if err != nil {
		t.Fatal(err)
	}
	content := string(result)
	if !strings.Contains(content, `src="assets/`) && !strings.Contains(content, `src="/_next/`) {
		t.Fatal("script src was removed or not rewritten")
	}
	os.RemoveAll("test_output")
}
