package main

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// animationContext holds detected animation framework information
// so downstream functions know what NOT to destroy.
type animationContext struct {
	HasWebflow      bool
	HasGSAP         bool
	HasAOS          bool
	HasScrollReveal bool
	HasWOW          bool
	HasLottie       bool
	HasFramerMotion bool
	HasAnimations   bool // true if any framework detected
}

type domTransformLib struct {
	name           string
	scriptPatterns []string
	initPatterns   []string
}

var domTransformLibraries = []domTransformLib{
	{
		name:           "CircleType.js",
		scriptPatterns: []string{"/circletype.js", "/circletype.min.js", "/circletype/"},
		initPatterns:   []string{"new circletype(", "circletype(", ".radius("},
	},
	{
		name:           "Splitting.js",
		scriptPatterns: []string{"/splitting.js", "/splitting.min.js", "/splitting/dist/"},
		initPatterns:   []string{"splitting(", "splitting.html("},
	},
	{
		name:           "Typed.js",
		scriptPatterns: []string{"/typed.min.js", "/typed.js"},
		initPatterns:   []string{"new typed("},
	},
	{
		name:           "Vivus.js",
		scriptPatterns: []string{"/vivus.js", "/vivus.min.js", "/vivus/"},
		initPatterns:   []string{"new vivus("},
	},
	{
		name:           "CountUp.js",
		scriptPatterns: []string{"/countup.js", "/countup.min.js", "/countup/"},
		initPatterns:   []string{"new countup("},
	},
	{
		name:           "Textillate.js",
		scriptPatterns: []string{"/textillate.js", "/textillate.min.js"},
		initPatterns:   []string{".textillate("},
	},
	{
		name:           "Lettering.js",
		scriptPatterns: []string{"/lettering.js", "/lettering.min.js", "/lettering/"},
		initPatterns:   []string{".lettering("},
	},
	{
		name:           "FitText.js",
		scriptPatterns: []string{"/fittext.js", "/fittext.min.js"},
		initPatterns:   []string{".fittext(", "fittext("},
	},
}

func (d *WebsiteDownloader) detectAnimationFrameworks(doc *goquery.Document) animationContext {
	ctx := animationContext{}

	htmlStr, _ := doc.Html()
	htmlLower := strings.ToLower(htmlStr)

	if doc.Find("[data-w-id]").Length() > 0 ||
		doc.Find("[data-wf-page]").Length() > 0 ||
		doc.Find("[data-wf-site]").Length() > 0 {
		ctx.HasWebflow = true
	}
	doc.Find("script[src]").Each(func(i int, s *goquery.Selection) {
		src, _ := s.Attr("src")
		srcLower := strings.ToLower(src)
		if strings.Contains(srcLower, "webflow") && strings.HasSuffix(srcLower, ".js") {
			ctx.HasWebflow = true
		}
	})

	doc.Find("script[src]").Each(func(i int, s *goquery.Selection) {
		src, _ := s.Attr("src")
		srcLower := strings.ToLower(src)
		if strings.Contains(srcLower, "gsap") || strings.Contains(srcLower, "greensock") ||
			strings.Contains(srcLower, "scrolltrigger") || strings.Contains(srcLower, "scrollsmoother") {
			ctx.HasGSAP = true
		}
	})
	doc.Find("script").Each(func(i int, s *goquery.Selection) {
		text := s.Text()
		if strings.Contains(text, "gsap.") || strings.Contains(text, "ScrollTrigger") ||
			strings.Contains(text, "ScrollSmoother") {
			ctx.HasGSAP = true
		}
	})
	if doc.Find("[data-speed]").Length() > 0 || doc.Find("[data-lag]").Length() > 0 ||
		doc.Find("#smooth-wrapper").Length() > 0 {
		ctx.HasGSAP = true
	}

	if doc.Find("[data-aos]").Length() > 0 {
		ctx.HasAOS = true
	}

	htmlClass := doc.Find("html").AttrOr("class", "")
	if strings.Contains(htmlClass, "sr") {
		ctx.HasScrollReveal = true
	}
	if doc.Find("[data-sr-id]").Length() > 0 {
		ctx.HasScrollReveal = true
	}

	if doc.Find(".wow").Length() > 0 {
		ctx.HasWOW = true
	}
	if strings.Contains(htmlLower, "new wow") {
		ctx.HasWOW = true
	}

	if doc.Find("[data-animation-type='lottie']").Length() > 0 ||
		doc.Find(".lottie").Length() > 0 ||
		doc.Find(".bodymovin").Length() > 0 ||
		doc.Find("[data-animation-path]").Length() > 0 ||
		doc.Find("lottie-player").Length() > 0 ||
		doc.Find("dotlottie-wc").Length() > 0 {
		ctx.HasLottie = true
	}

	if doc.Find("[data-framer-component-type]").Length() > 0 ||
		doc.Find("[data-framer-name]").Length() > 0 ||
		doc.Find("[data-framer-appear-id]").Length() > 0 {
		ctx.HasFramerMotion = true
	}

	ctx.HasAnimations = ctx.HasWebflow || ctx.HasGSAP || ctx.HasAOS ||
		ctx.HasScrollReveal || ctx.HasWOW || ctx.HasLottie || ctx.HasFramerMotion

	if ctx.HasAnimations {
		var detected []string
		if ctx.HasWebflow {
			detected = append(detected, "Webflow IX2")
		}
		if ctx.HasGSAP {
			detected = append(detected, "GSAP")
		}
		if ctx.HasAOS {
			detected = append(detected, "AOS")
		}
		if ctx.HasScrollReveal {
			detected = append(detected, "ScrollReveal")
		}
		if ctx.HasWOW {
			detected = append(detected, "WOW.js")
		}
		if ctx.HasLottie {
			detected = append(detected, "Lottie")
		}
		if ctx.HasFramerMotion {
			detected = append(detected, "Framer Motion")
		}
		d.log(fmt.Sprintf("> Animation frameworks detected: %s", strings.Join(detected, ", ")))
	}

	return ctx
}

func (d *WebsiteDownloader) processHTML(htmlContent string) error {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		return err
	}

	animCtx := d.detectAnimationFrameworks(doc)

	d.fixScrollBlocking(doc, animCtx)
	d.removeWrapperIframes(doc)
	d.neutralizeDOMTransformScripts(doc)
	d.ensureWebFontLinks(doc)
	d.processStylesheets(doc)
	d.processInlineStyles(doc)
	d.processScripts(doc)
	d.processImages(doc)
	d.processInlineStyleAttrs(doc)
	d.processFavicons(doc)
	d.processMetaImages(doc)
	d.processBackgroundData(doc)
	d.processLottieAssets(doc)
	d.fixNavigationLinks(doc)
	d.handleSPAFrameworks(doc, animCtx)
	d.handleWebflowOffline(doc, animCtx)
	d.injectAnimationFallbacks(doc, animCtx)

	htmlOutput, err := doc.Html()
	if err != nil {
		return err
	}

	outputPath := d.outputDir + "/index.html"
	return os.WriteFile(outputPath, []byte("<!DOCTYPE html>\n"+htmlOutput), 0644)
}

func (d *WebsiteDownloader) fixScrollBlocking(doc *goquery.Document, animCtx animationContext) {
	d.log("> Fixing scroll issues for offline viewing...")

	htmlElem := doc.Find("html")
	if htmlElem.Length() > 0 {
		classes := htmlElem.AttrOr("class", "")
		if classes != "" {
			newClasses := removeScrollBlockingClasses(classes, []string{
				"lenis", "lenis-smooth", "lenis-scrolling", "lenis-stopped",
				"has-scroll-smooth", "has-scroll-init", "locomotive-scroll",
			})
			htmlElem.SetAttr("class", newClasses)
		}
	}

	body := doc.Find("body")
	if body.Length() > 0 {
		classes := body.AttrOr("class", "")
		if classes != "" {
			newClasses := removeScrollBlockingClasses(classes, []string{
				"overflow-hidden", "no-scroll", "scroll-lock",
				"lenis", "lenis-smooth", "has-scroll-smooth",
			})
			if !animCtx.HasAnimations {
				newClasses = removeScrollBlockingClasses(newClasses, []string{"fixed"})
			}
			body.SetAttr("class", newClasses)
		}
	}

	scrollFixCSS := `
/* WebCopy: scroll fix */
html, body {
    overflow-y: auto !important;
    overflow-x: hidden !important;
    scroll-behavior: auto !important;
}
html.lenis, html.lenis-smooth,
body.lenis, body.lenis-smooth,
.lenis-wrapper, .lenis-content,
[data-lenis-prevent] {
    overflow: visible !important;
    height: auto !important;
}`

	head := doc.Find("head")
	if head.Length() > 0 {
		head.AppendHtml(fmt.Sprintf(`<style data-webcopy-scroll-fix="true">%s</style>`, scrollFixCSS))
		d.log("> Injected CSS scroll fixes")
	}
}

func removeScrollBlockingClasses(classes string, toRemove []string) string {
	classSlice := strings.Fields(classes)
	removeLower := make(map[string]bool)
	for _, c := range toRemove {
		removeLower[strings.ToLower(c)] = true
	}
	var filtered []string
	for _, c := range classSlice {
		if !removeLower[strings.ToLower(c)] {
			filtered = append(filtered, c)
		}
	}
	return strings.Join(filtered, " ")
}

func (d *WebsiteDownloader) removeWrapperIframes(doc *goquery.Document) {
	doc.Find("iframe").Each(func(i int, s *goquery.Selection) {
		_, hasSrcdoc := s.Attr("srcdoc")
		class, _ := s.Attr("class")
		if hasSrcdoc || strings.Contains(strings.ToLower(class), "preview") {
			s.Remove()
		}
	})
}

func (d *WebsiteDownloader) neutralizeDOMTransformScripts(doc *goquery.Document) {
	detectedLibs := make(map[string]bool)

	doc.Find("script[src]").Each(func(i int, s *goquery.Selection) {
		src, _ := s.Attr("src")
		srcLower := strings.ToLower(src)
		for _, lib := range domTransformLibraries {
			for _, pattern := range lib.scriptPatterns {
				if strings.Contains(srcLower, pattern) {
					detectedLibs[lib.name] = true
					s.Remove()
					break
				}
			}
		}
	})

	if len(detectedLibs) == 0 {
		return
	}

	var detected []string
	for name := range detectedLibs {
		detected = append(detected, name)
	}
	sort.Strings(detected)
	d.log(fmt.Sprintf("> DOM-transform libraries detected: %s — removing to prevent double-execution", strings.Join(detected, ", ")))

	doc.Find("script:not([src])").Each(func(i int, s *goquery.Selection) {
		text := s.Text()
		textLower := strings.ToLower(text)
		for _, lib := range domTransformLibraries {
			if !detectedLibs[lib.name] {
				continue
			}
			for _, pattern := range lib.initPatterns {
				if strings.Contains(textLower, strings.ToLower(pattern)) {
					s.Remove()
					return
				}
			}
		}
	})
}

func (d *WebsiteDownloader) ensureWebFontLinks(doc *goquery.Document) {
	var families []string

	doc.Find("script:not([src])").Each(func(i int, s *goquery.Selection) {
		text := s.Text()
		if !strings.Contains(text, "WebFont.load") && !strings.Contains(text, "webfont") {
			return
		}

		matches := webFontFamilyPattern.FindStringSubmatch(text)
		if len(matches) < 2 {
			return
		}

		items := webFontFamilyItemPattern.FindAllStringSubmatch(matches[1], -1)
		for _, item := range items {
			if len(item) >= 2 {
				families = append(families, item[1])
			}
		}
	})

	if len(families) == 0 {
		return
	}

	alreadyExists := false
	doc.Find("link[rel*='stylesheet'][href*='fonts.googleapis.com']").Each(func(i int, s *goquery.Selection) {
		alreadyExists = true
	})
	if alreadyExists {
		return
	}

	fontsURL := fmt.Sprintf("https://fonts.googleapis.com/css2?%s&display=swap",
		strings.Join(func() []string {
			var params []string
			for _, f := range families {
				parts := strings.SplitN(f, ":", 2)
				param := "family=" + url.QueryEscape(parts[0])
				if len(parts) > 1 {
					param += ":wght@" + strings.ReplaceAll(parts[1], ",", ";")
				}
				params = append(params, param)
			}
			return params
		}(), "&"))

	head := doc.Find("head")
	if head.Length() > 0 {
		linkTag := fmt.Sprintf(`<link href="%s" rel="stylesheet" data-webcopy-webfont="true">`, fontsURL)
		head.PrependHtml(linkTag)
		d.log(fmt.Sprintf("> Injected Google Fonts link for: %s", strings.Join(families, ", ")))
	}
}

func (d *WebsiteDownloader) processStylesheets(doc *goquery.Document) {
	d.log("> Processing stylesheets...")
	doc.Find("link[rel]").Each(func(i int, s *goquery.Selection) {
		rel, _ := s.Attr("rel")
		if !strings.Contains(strings.ToLower(rel), "stylesheet") {
			return
		}
		href, exists := s.Attr("href")
		if !exists || strings.HasPrefix(href, "data:") {
			return
		}

		absURL := d.absolutize(href, "")
		var cssContent string

		if res, ok := d.networkResources[absURL]; ok {
			cssContent = string(res.body)
		} else if d.httpClient != nil {
			if body, _, err := d.httpClient.getCSS(absURL); err == nil {
				cssContent = string(body)
			}
		}

		if cssContent != "" {
			cssContent = d.processCSSImports(cssContent, absURL, false)
			cssContent = d.rewriteCSSURLs(cssContent, absURL, false)
			localPath := d.saveResource(absURL, []byte(cssContent), "text/css")
			if localPath != "" {
				s.SetAttr("href", localPath)
			}
		}
	})
}

func (d *WebsiteDownloader) processInlineStyles(doc *goquery.Document) {
	d.log("> Processing inline styles...")
	doc.Find("style").Each(func(i int, s *goquery.Selection) {
		scriptText := s.Text()
		if scriptText != "" {
			newText := d.processCSSImports(scriptText, d.baseURL, true)
			newText = d.rewriteCSSURLs(newText, d.baseURL, true)
			if len(s.Nodes) > 0 && s.Nodes[0].FirstChild != nil {
				s.Nodes[0].FirstChild.Data = newText
			}
		}
	})
}

func (d *WebsiteDownloader) processScripts(doc *goquery.Document) {
	d.log("> Processing scripts...")
	doc.Find("script[src]").Each(func(i int, s *goquery.Selection) {
		src, _ := s.Attr("src")
		if src == "" || strings.HasPrefix(src, "data:") {
			return
		}

		localPath := d.getResource(src, "")
		if localPath != "" && localPath != src {
			s.SetAttr("src", localPath)
			for _, attr := range []string{"integrity", "crossorigin", "nonce"} {
				if _, exists := s.Attr(attr); exists {
					s.RemoveAttr(attr)
				}
			}
		}
	})
}

func (d *WebsiteDownloader) processImages(doc *goquery.Document) {
	d.log("> Processing images...")
	doc.Find("img, source, video, audio, picture, input").Each(func(i int, s *goquery.Selection) {
		src, hasSrc := s.Attr("src")

		for _, attr := range []string{"data-src", "data-original", "data-lazy-src", "data-url", "data-image", "data-bg"} {
			if lazySrc, exists := s.Attr(attr); exists && lazySrc != "" {
				localPath := d.getResource(lazySrc, "")
				if localPath != "" && localPath != lazySrc {
					s.SetAttr("src", localPath)
					s.RemoveAttr(attr)
					hasSrc = false
				}
				break
			}
		}

		if hasSrc && src != "" && !strings.HasPrefix(src, "data:") {
			localPath := d.getResource(src, "")
			if localPath != "" && localPath != src {
				s.SetAttr("src", localPath)
			}
		}

		if srcset, exists := s.Attr("srcset"); exists && srcset != "" {
			s.SetAttr("srcset", d.processSrcset(srcset))
		}
		if dataSrcset, exists := s.Attr("data-srcset"); exists && dataSrcset != "" {
			s.SetAttr("data-srcset", d.processSrcset(dataSrcset))
		}

		if goquery.NodeName(s) == "video" {
			if poster, exists := s.Attr("poster"); exists && poster != "" {
				localPath := d.getResource(poster, "")
				if localPath != "" && localPath != poster {
					s.SetAttr("poster", localPath)
				}
			}
		}

		s.RemoveAttr("loading")
	})
}

func (d *WebsiteDownloader) processInlineStyleAttrs(doc *goquery.Document) {
	d.log("> Processing inline style attributes...")
	doc.Find("[style]").Each(func(i int, s *goquery.Selection) {
		style, _ := s.Attr("style")
		if strings.Contains(style, "url(") {
			s.SetAttr("style", d.rewriteCSSURLs(style, d.baseURL, true))
		}
	})
}

func (d *WebsiteDownloader) processFavicons(doc *goquery.Document) {
	doc.Find("link[href][rel]").Each(func(i int, s *goquery.Selection) {
		rel, _ := s.Attr("rel")
		relLower := strings.ToLower(rel)
		if strings.Contains(relLower, "icon") || strings.Contains(relLower, "apple-touch") ||
			strings.Contains(relLower, "manifest") {
			href, _ := s.Attr("href")
			if href != "" && !strings.HasPrefix(href, "data:") {
				localPath := d.getResource(href, "")
				if localPath != "" && localPath != href {
					s.SetAttr("href", localPath)
				}
			}
		}
	})
}

func (d *WebsiteDownloader) processMetaImages(doc *goquery.Document) {
	doc.Find("meta[content]").Each(func(i int, s *goquery.Selection) {
		prop, _ := s.Attr("property")
		name, _ := s.Attr("name")
		if strings.Contains(strings.ToLower(prop), "image") ||
			strings.Contains(strings.ToLower(name), "image") {
			content, _ := s.Attr("content")
			if content != "" && !strings.HasPrefix(content, "data:") &&
				(strings.Contains(content, "http") || strings.HasPrefix(content, "/")) {
				localPath := d.getResource(content, "")
				if localPath != "" && localPath != content {
					s.SetAttr("content", localPath)
				}
			}
		}
	})
}

func (d *WebsiteDownloader) processBackgroundData(doc *goquery.Document) {
	doc.Find("[data-background]").Each(func(i int, s *goquery.Selection) {
		bg, _ := s.Attr("data-background")
		if bg != "" && !strings.HasPrefix(bg, "data:") {
			localPath := d.getResource(bg, "")
			if localPath != "" && localPath != bg {
				s.SetAttr("data-background", localPath)
				existingStyle := s.AttrOr("style", "")
				if existingStyle != "" {
					existingStyle = strings.TrimRight(existingStyle, ";") + "; "
				}
				s.SetAttr("style", existingStyle+fmt.Sprintf(`background-image: url("%s");`, localPath))
			}
		}
	})

	doc.Find("[data-bg]").Each(func(i int, s *goquery.Selection) {
		bg, _ := s.Attr("data-bg")
		if bg != "" && !strings.HasPrefix(bg, "data:") {
			localPath := d.getResource(bg, "")
			if localPath != "" && localPath != bg {
				existingStyle := s.AttrOr("style", "")
				if existingStyle != "" {
					existingStyle = strings.TrimRight(existingStyle, ";") + "; "
				}
				s.SetAttr("style", existingStyle+fmt.Sprintf(`background-image: url("%s");`, localPath))
				s.RemoveAttr("data-bg")
			}
		}
	})
}

func (d *WebsiteDownloader) processLottieAssets(doc *goquery.Document) {
	found := 0

	doc.Find("[data-animation-type='lottie'][data-src]").Each(func(i int, s *goquery.Selection) {
		src, _ := s.Attr("data-src")
		if src == "" || strings.HasPrefix(src, "data:") {
			return
		}
		localPath := d.getResource(src, "")
		if localPath != "" && localPath != src {
			s.SetAttr("data-src", localPath)
			found++
		}
	})

	doc.Find("[data-animation-path]").Each(func(i int, s *goquery.Selection) {
		src, _ := s.Attr("data-animation-path")
		if src == "" || strings.HasPrefix(src, "data:") {
			return
		}
		localPath := d.getResource(src, "")
		if localPath != "" && localPath != src {
			s.SetAttr("data-animation-path", localPath)
			found++
		}
	})

	doc.Find("lottie-player[src], dotlottie-wc[src]").Each(func(i int, s *goquery.Selection) {
		src, _ := s.Attr("src")
		if src == "" || strings.HasPrefix(src, "data:") {
			return
		}
		localPath := d.getResource(src, "")
		if localPath != "" && localPath != src {
			s.SetAttr("src", localPath)
			found++
		}
	})

	if found > 0 {
		d.log(fmt.Sprintf("🎬 Processados %d assets Lottie", found))
	}
}

func (d *WebsiteDownloader) fixNavigationLinks(doc *goquery.Document) {
	d.log("> Fixing navigation links...")
	doc.Find("a[href]").Each(func(i int, s *goquery.Selection) {
		href, _ := s.Attr("href")
		if href == "/" {
			s.SetAttr("href", "#")
		} else if strings.HasPrefix(href, "/") && !strings.HasPrefix(href, "//") {
			s.SetAttr("href", "#")
		}
	})
}

func (d *WebsiteDownloader) handleSPAFrameworks(doc *goquery.Document, animCtx animationContext) {
	isGatsby := doc.Find("#___gatsby").Length() > 0
	isNextJS := doc.Find("#__next").Length() > 0 || d.detectNextJS(doc)
	isNuxt := doc.Find("#__nuxt").Length() > 0

	if !isGatsby && !isNextJS && !isNuxt {
		return
	}

	framework := "Gatsby"
	if isNextJS {
		framework = "Next.js"
	} else if isNuxt {
		framework = "Nuxt"
	}
	d.log(fmt.Sprintf("> Detected %s - removing framework scripts...", framework))

	safeKeywords := []string{"google", "analytics", "gtm", "gtag", "facebook", "pixel",
		"elfsight", "hubspot", "intercom", "crisp", "drift", "hotjar",
		"clarity", "segment", "mixpanel", "amplitude", "adobe", "privacy"}

	if animCtx.HasWebflow {
		safeKeywords = append(safeKeywords, "webflow")
	}
	if animCtx.HasGSAP {
		safeKeywords = append(safeKeywords, "gsap", "greensock", "scrolltrigger", "scrollsmoother")
	}
	if animCtx.HasAOS {
		safeKeywords = append(safeKeywords, "aos")
	}
	if animCtx.HasLottie {
		safeKeywords = append(safeKeywords, "lottie", "bodymovin")
	}
	if animCtx.HasWOW {
		safeKeywords = append(safeKeywords, "wow")
	}
	if animCtx.HasScrollReveal {
		safeKeywords = append(safeKeywords, "scrollreveal")
	}
	safeKeywords = append(safeKeywords, "jquery")

	scriptsRemoved := 0
	doc.Find("script").Each(func(i int, s *goquery.Selection) {
		src, _ := s.Attr("src")
		scriptText := s.Text()

		isSafe := false
		srcLower := strings.ToLower(src)
		for _, kw := range safeKeywords {
			if strings.Contains(srcLower, kw) {
				isSafe = true
				break
			}
		}

		if isSafe {
			return
		}

		shouldRemove := false

		if isGatsby && (strings.Contains(src, "framework-") ||
			strings.Contains(src, "app-") || strings.Contains(src, "commons-") ||
			strings.Contains(src, "component-") || strings.Contains(src, "webpack-runtime") ||
			strings.Contains(src, "polyfill")) {
			shouldRemove = true
		}

		if isNextJS {
			if src != "" && !strings.HasPrefix(src, "http://") &&
				!strings.HasPrefix(src, "https://") && !strings.HasPrefix(src, "//") {
				shouldRemove = true
			}
			if strings.Contains(src, "_next/") ||
				strings.Contains(srcLower, "polyfill") {
				shouldRemove = true
			}
			if strings.Contains(scriptText, "__next") || strings.Contains(scriptText, "self.__next") {
				shouldRemove = true
			}
			if strings.Contains(src, "-") && strings.HasSuffix(src, ".js") && strings.Contains(src, "assets/") {
				shouldRemove = true
			}
		}

		if isNuxt && (strings.Contains(src, "_nuxt/") || strings.Contains(scriptText, "__NUXT__") ||
			strings.Contains(srcLower, "nuxt")) {
			shouldRemove = true
		}

		if strings.Contains(scriptText, "GATSBY") || strings.Contains(scriptText, "pageData") ||
			strings.Contains(scriptText, "self.__next") || strings.Contains(scriptText, "__NEXT_DATA__") {
			shouldRemove = true
		}

		if shouldRemove {
			s.Remove()
			scriptsRemoved++
		}
	})

	linksRemoved := 0
	doc.Find("link[rel]").Each(func(i int, s *goquery.Selection) {
		rel, _ := s.Attr("rel")
		relLower := strings.ToLower(rel)
		if strings.Contains(relLower, "preload") || strings.Contains(relLower, "prefetch") ||
			strings.Contains(relLower, "modulepreload") {
			href, _ := s.Attr("href")
			if strings.Contains(href, "_next/") || (strings.HasPrefix(href, "assets/") && strings.Contains(href, "-")) {
				s.Remove()
				linksRemoved++
			}
		}
	})

	d.log(fmt.Sprintf("> Removed %d scripts and %d preloads", scriptsRemoved, linksRemoved))
}

func (d *WebsiteDownloader) handleWebflowOffline(doc *goquery.Document, animCtx animationContext) {
	if !animCtx.HasWebflow {
		return
	}

	d.log("> Applying Webflow offline fixes...")

	htmlElem := doc.Find("html")

	classes := htmlElem.AttrOr("class", "")
	classSlice := strings.Fields(classes)
	hasModJS := false
	hasModIX := false
	for _, c := range classSlice {
		if c == "w-mod-js" {
			hasModJS = true
		}
		if c == "w-mod-ix" {
			hasModIX = true
		}
	}
	if !hasModJS {
		classSlice = append(classSlice, "w-mod-js")
	}
	if !hasModIX {
		classSlice = append(classSlice, "w-mod-ix")
	}
	htmlElem.SetAttr("class", strings.Join(classSlice, " "))

	webflowFallbackCSS := `
/* WebCopy: Webflow offline fallback */
[data-w-id] {
    opacity: 1 !important;
    visibility: visible !important;
}`

	head := doc.Find("head")
	if head.Length() > 0 {
		head.AppendHtml(fmt.Sprintf(`<style data-webcopy-webflow-fix="true">%s</style>`, webflowFallbackCSS))
	}

	doc.Find("[data-w-id]").Each(func(i int, s *goquery.Selection) {
		style, exists := s.Attr("style")
		if !exists {
			return
		}
		cleaned := d.cleanWebflowInlineStyle(style)
		if cleaned != style {
			if cleaned == "" {
				s.RemoveAttr("style")
			} else {
				s.SetAttr("style", cleaned)
			}
		}
	})

	body := doc.Find("body")
	if body.Length() > 0 {
		bodyStyle := body.AttrOr("style", "")
		if strings.Contains(bodyStyle, "opacity") {
			cleaned := d.cleanWebflowInlineStyle(bodyStyle)
			if cleaned != bodyStyle {
				if cleaned == "" {
					body.RemoveAttr("style")
				} else {
					body.SetAttr("style", cleaned)
				}
			}
		}
	}

	d.log("> Guaranteed w-mod-ix + CSS fallbacks")
}

var webflowStyleCleanRe = regexp.MustCompile(`\s*(?:opacity|display)\s*:\s*[\d.]+\s*;?`)
var webflowTransformRe = regexp.MustCompile(`\s*transform\s*:\s*translate3d\([^)]+\)\s*;?`)

func (d *WebsiteDownloader) cleanWebflowInlineStyle(style string) string {
	hasOpacity0 := strings.Contains(style, "opacity:0")

	cleaned := webflowStyleCleanRe.ReplaceAllString(style, "")

	if hasOpacity0 {
		cleaned = webflowTransformRe.ReplaceAllString(cleaned, "")
	}

	cleaned = strings.TrimSpace(cleaned)
	cleaned = strings.TrimRight(cleaned, ";")
	cleaned = strings.TrimSpace(cleaned)

	if hasOpacity0 && !strings.Contains(cleaned, "transform:") {
		if cleaned != "" {
			cleaned += "; transform: none"
		} else {
			cleaned = "transform: none"
		}
	}

	return cleaned
}

func (d *WebsiteDownloader) injectAnimationFallbacks(doc *goquery.Document, animCtx animationContext) {
	if !animCtx.HasAnimations {
		return
	}

	var cssBlocks []string

	if animCtx.HasAOS {
		cssBlocks = append(cssBlocks, `
/* AOS fallback */
body:not(.aos-init) [data-aos] {
    opacity: 1 !important;
    transform: none !important;
    transition: none !important;
}`)
	}

	if animCtx.HasScrollReveal {
		cssBlocks = append(cssBlocks, `
/* ScrollReveal fallback */
html.sr [data-sr-id] {
    visibility: visible !important;
}`)
	}

	if animCtx.HasWOW {
		cssBlocks = append(cssBlocks, `
/* WOW.js fallback */
.wow {
    visibility: visible !important;
    animation-name: none !important;
}`)
	}

	if len(cssBlocks) > 0 {
		fallbackCSS := strings.Join(cssBlocks, "\n")
		head := doc.Find("head")
		if head.Length() > 0 {
			head.AppendHtml(fmt.Sprintf(`<style data-webcopy-anim-fallback="true">%s</style>`, fallbackCSS))
		}
		d.log("> Injected animation fallbacks")
	}
}

func (d *WebsiteDownloader) detectNextJS(doc *goquery.Document) bool {
	found := false
	doc.Find("script").Each(func(i int, s *goquery.Selection) {
		scriptID, _ := s.Attr("id")
		scriptText := s.Text()
		if strings.Contains(scriptID, "__NEXT_DATA__") || strings.Contains(scriptText, "__NEXT_DATA__") {
			found = true
		}
		if strings.Contains(scriptText, "self.__next") {
			found = true
		}
	})
	if found {
		return true
	}

	doc.Find("script[src]").Each(func(i int, s *goquery.Selection) {
		src, _ := s.Attr("src")
		if strings.Contains(src, "_next/") {
			found = true
		}
	})
	if found {
		return true
	}

	doc.Find("link").Each(func(i int, s *goquery.Selection) {
		href, _ := s.Attr("href")
		if strings.Contains(href, "_next/") {
			found = true
		}
	})
	return found
}

func (d *WebsiteDownloader) processSrcset(srcset string) string {
	if srcset == "" {
		return srcset
	}

	var newParts []string
	parts := strings.Split(srcset, ",")

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		tokens := strings.Fields(part)
		if len(tokens) == 0 {
			continue
		}

		imgURL := tokens[0]
		descriptor := strings.Join(tokens[1:], " ")

		if strings.HasPrefix(imgURL, "data:") {
			newParts = append(newParts, part)
			continue
		}

		localPath := d.getResource(imgURL, "")
		if localPath != "" && localPath != imgURL {
			if descriptor != "" {
				newParts = append(newParts, localPath+" "+descriptor)
			} else {
				newParts = append(newParts, localPath)
			}
		} else {
			newParts = append(newParts, part)
		}
	}

	return strings.Join(newParts, ", ")
}

var webFontFamilyPattern = regexp.MustCompile(`families\s*:\s*\[([^\]]+)\]`)
var webFontFamilyItemPattern = regexp.MustCompile(`["']([^"']+)["']`)

var cssURLPattern = regexp.MustCompile(`url\(\s*([^)]+)\s*\)`)
var cssImportPattern = regexp.MustCompile(`@import\s+(?:url\(\s*["']?([^)"']+)["']?\s*\)|["']([^"']+)["'])([^;]*);?`)

func (d *WebsiteDownloader) processCSSImports(cssContent, cssBaseURL string, isInline bool) string {
	return cssImportPattern.ReplaceAllStringFunc(cssContent, func(match string) string {
		groups := cssImportPattern.FindStringSubmatch(match)
		urlPart := groups[1]
		if urlPart == "" {
			urlPart = groups[2]
		}
		urlPart = strings.TrimSpace(urlPart)
		mediaQuery := strings.TrimSpace(groups[3])

		if urlPart == "" || strings.HasPrefix(urlPart, "data:") {
			return match
		}

		absURL := d.absolutize(urlPart, cssBaseURL)

		var importedCSS string
		if res, ok := d.networkResources[absURL]; ok {
			importedCSS = string(res.body)
		} else if d.httpClient != nil {
			if body, _, err := d.httpClient.getCSS(absURL); err == nil {
				importedCSS = string(body)
			}
		}

		if importedCSS != "" {
			importedCSS = d.processCSSImports(importedCSS, absURL, false)
			importedCSS = d.rewriteCSSURLs(importedCSS, absURL, false)
			localPath := d.saveResource(absURL, []byte(importedCSS), "text/css")
			if localPath != "" {
				relPath := localPath
				if isInline {
					relPath = localPath
				} else {
					if idx := strings.LastIndex(localPath, "/"); idx >= 0 {
						relPath = localPath[idx+1:]
					}
				}
				if mediaQuery != "" {
					return fmt.Sprintf(`@import url("%s") %s;`, relPath, mediaQuery)
				}
				return fmt.Sprintf(`@import url("%s");`, relPath)
			}
		}

		return match
	})
}

func (d *WebsiteDownloader) rewriteCSSURLs(cssContent, cssBaseURL string, isInline bool) string {
	return cssURLPattern.ReplaceAllStringFunc(cssContent, func(match string) string {
		groups := cssURLPattern.FindStringSubmatch(match)
		if len(groups) < 2 {
			return match
		}
		urlContent := strings.TrimSpace(groups[1])

		if strings.HasPrefix(urlContent, "'") || strings.HasPrefix(urlContent, `"`) {
			urlContent = urlContent[1:]
		}
		if strings.HasSuffix(urlContent, "'") || strings.HasSuffix(urlContent, `"`) {
			urlContent = urlContent[:len(urlContent)-1]
		}

		if urlContent == "" || strings.HasPrefix(urlContent, "data:") {
			return match
		}

		absURL := d.absolutize(urlContent, cssBaseURL)
		localPath := d.getResource(absURL, "")

		if localPath != "" && strings.HasPrefix(localPath, "assets/") {
			if isInline {
				return fmt.Sprintf(`url("%s")`, localPath)
			}
			basename := localPath
			if idx := strings.LastIndex(basename, "/"); idx >= 0 {
				basename = basename[idx+1:]
			}
			return fmt.Sprintf(`url("%s")`, basename)
		}

		return match
	})
}
