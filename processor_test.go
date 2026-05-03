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
	workDir := t.TempDir()
	html := `<!DOCTYPE html><html><head><script src="/_next/static/chunks/main.js"></script></head><body></body></html>`
	d := NewWebsiteDownloader("https://example.com", workDir, func(string) {})
	d.processHTML(html)

	result, err := os.ReadFile(workDir + "/index.html")
	if err != nil {
		t.Fatal(err)
	}
	content := string(result)
	if !strings.Contains(content, `src="assets/`) && !strings.Contains(content, `src="/_next/`) {
		t.Fatal("script src was removed or not rewritten")
	}
}

func TestImportMapInjection(t *testing.T) {
	html := `<!DOCTYPE html><html><head></head><body></body></html>`
	d := NewWebsiteDownloader("https://example.com", "test_output_importmap", func(string) {})
	// Simulate captured resource
	d.resourceCache["https://cdn.jsdelivr.net/npm/liquidglass/dist/index.js"] = "assets/liquidglass_index.js"
	os.MkdirAll("test_output_importmap", 0755)
	d.processHTML(html)

	result, err := os.ReadFile("test_output_importmap/index.html")
	if err != nil {
		t.Fatal(err)
	}
	content := string(result)
	if !strings.Contains(content, `<script type="importmap"`) {
		t.Fatal("import map not injected")
	}
	if !strings.Contains(content, `"https://cdn.jsdelivr.net/npm/liquidglass/dist/index.js"`) {
		t.Fatal("CDN URL not in import map")
	}
	if !strings.Contains(content, `"assets/liquidglass_index.js"`) {
		t.Fatal("local path not in import map")
	}
	os.RemoveAll("test_output_importmap")
}
