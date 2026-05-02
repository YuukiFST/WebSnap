package main

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

func TestNeutralizeDOMTransformScripts_CircleType(t *testing.T) {
	html := `<html><head>
		<script src="https://cdn.jsdelivr.net/npm/circletype@2.3.0/dist/circletype.min.js"></script>
	</head><body>
		<div id="circletext" class="circle-text">
			<div aria-label="Hello World" style="position: relative;">
				<span style="position: absolute; transform: rotate(-45deg);">H</span>
				<span style="position: absolute; transform: rotate(-30deg);">e</span>
			</div>
		</div>
		<script>new CircleType(document.getElementById('circletext'));</script>
		<script>
			// Some other script
			console.log("keep me");
		</script>
	</body></html>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}

	d := &WebsiteDownloader{logCallback: func(s string) {}}
	d.neutralizeDOMTransformScripts(doc)

	result, _ := doc.Html()

	if strings.Contains(result, "circletype") {
		t.Error("CircleType library script was not removed")
	}

	if strings.Contains(result, "new CircleType") {
		t.Error("CircleType initialization script was not removed")
	}

	if !strings.Contains(result, "keep me") {
		t.Error("Unrelated script was incorrectly removed")
	}

	if !strings.Contains(result, "rotate(-45deg)") {
		t.Error("Transformed DOM content was incorrectly removed")
	}
}

func TestHandleWebflowOffline_NoDisplayBlock(t *testing.T) {
	d := &WebsiteDownloader{
		logCallback: func(msg string) {},
	}

	html := `<!DOCTYPE html><html><head></head><body><span data-w-id="abc">inline text</span></body></html>`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}

	animCtx := animationContext{HasWebflow: true}
	d.handleWebflowOffline(doc, animCtx)

	styleEl := doc.Find(`style[data-webcopy-webflow-fix="true"]`)
	if styleEl.Length() == 0 {
		t.Fatal("expected Webflow fix style element to be injected")
	}

	css := styleEl.Text()

	if strings.Contains(css, "display") {
		t.Errorf("Webflow fallback CSS must NOT set display property (breaks inline elements):\n%s", css)
	}
}

func TestNeutralizeDOMTransformScripts_NoLibrary(t *testing.T) {
	html := `<html><head></head><body>
		<script>console.log("hello");</script>
		<script src="https://example.com/app.js"></script>
	</body></html>`

	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(html))
	d := &WebsiteDownloader{logCallback: func(s string) {}}
	d.neutralizeDOMTransformScripts(doc)

	result, _ := doc.Html()
	if !strings.Contains(result, "hello") {
		t.Error("Scripts should not be removed when no DOM-transform library detected")
	}
	if !strings.Contains(result, "app.js") {
		t.Error("External scripts should not be removed when no DOM-transform library detected")
	}
}

func TestNeutralizeDOMTransformScripts_SplittingJS(t *testing.T) {
	html := `<html><head>
		<script src="https://unpkg.com/splitting/dist/splitting.min.js"></script>
	</head><body>
		<div data-splitting>Hello</div>
		<script>Splitting();</script>
	</body></html>`

	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(html))
	d := &WebsiteDownloader{logCallback: func(s string) {}}
	d.neutralizeDOMTransformScripts(doc)

	result, _ := doc.Html()
	if strings.Contains(result, "splitting.min.js") {
		t.Error("Splitting.js library script should be removed")
	}
	if strings.Contains(result, "Splitting()") {
		t.Error("Splitting initialization should be removed")
	}
	if !strings.Contains(result, "data-splitting") {
		t.Error("data-splitting attribute should be preserved")
	}
}

func TestNeutralizeDOMTransformScripts_PreservesWebflow(t *testing.T) {
	html := `<html><head>
		<script src="https://cdn.jsdelivr.net/npm/circletype@2.3.0/dist/circletype.min.js"></script>
		<script src="https://assets.website-files.com/js/webflow.js"></script>
	</head><body>
		<script>new CircleType(document.getElementById('ct'));</script>
	</body></html>`

	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(html))
	d := &WebsiteDownloader{logCallback: func(s string) {}}
	d.neutralizeDOMTransformScripts(doc)

	result, _ := doc.Html()
	if strings.Contains(result, "circletype") {
		t.Error("CircleType should be removed")
	}
	if !strings.Contains(result, "webflow") {
		t.Error("Webflow script should NOT be removed")
	}
}

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
