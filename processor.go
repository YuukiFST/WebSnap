package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// animationContext holds detected animation framework information
// so downstream functions know what NOT to destroy.
type animationContext struct {
	HasWebflow bool
}

func (d *WebsiteDownloader) detectAnimationFrameworks(doc *goquery.Document) animationContext {
	ctx := animationContext{}

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

	if ctx.HasWebflow {
		d.log("> Animation framework detected: Webflow IX2")
	}

	return ctx
}

func (d *WebsiteDownloader) processHTML(htmlContent string) error {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		return err
	}

	animCtx := d.detectAnimationFrameworks(doc)

	d.removeWrapperIframes(doc)
	d.ensureWebFontLinks(doc)
	d.processStylesheets(doc)
	d.processInlineStyles(doc)
	d.processScripts(doc)
	d.processModuleScripts(doc)
	d.processImages(doc)
	d.processInlineStyleAttrs(doc)
	d.processFavicons(doc)
	d.processMetaImages(doc)
	d.processBackgroundData(doc)
	d.processLottieAssets(doc)
	d.fixNavigationLinks(doc)
	d.handleWebflowOffline(doc, animCtx)
	d.injectImportMap(doc)

	htmlOutput, err := doc.Html()
	if err != nil {
		return err
	}

	outputPath := d.outputDir + "/index.html"
	return os.WriteFile(outputPath, []byte("<!DOCTYPE html>\n"+htmlOutput), 0644)
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
			cssContent = d.processCSSImports(cssContent, absURL, false, 0)
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
			newText := d.processCSSImports(scriptText, d.baseURL, true, 0)
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

var moduleImportPattern = regexp.MustCompile(`import\s+(?:\{[^}]+\}|\*\s+as\s+\w+|\w+)?\s*from\s+['"]([^'"]+)['"]`)
var moduleSideEffectImportPattern = regexp.MustCompile(`import\s+['"]([^'"]+)['"]`)

func (d *WebsiteDownloader) processModuleScripts(doc *goquery.Document) {
	d.log("> Processing module scripts...")
	doc.Find("script[type='module']").Each(func(i int, s *goquery.Selection) {
		scriptText := s.Text()
		if scriptText == "" {
			return
		}

		newText := d.rewriteModuleImports(scriptText)
		if newText != scriptText {
			if len(s.Nodes) > 0 && s.Nodes[0].FirstChild != nil {
				s.Nodes[0].FirstChild.Data = newText
			}
		}
	})
}

func (d *WebsiteDownloader) rewriteModuleImports(scriptText string) string {
	rewriteImport := func(match string) string {
		groups := moduleImportPattern.FindStringSubmatch(match)
		if len(groups) < 2 {
			groups = moduleSideEffectImportPattern.FindStringSubmatch(match)
		}
		if len(groups) < 2 {
			return match
		}
		url := groups[1]
		if url == "" || strings.HasPrefix(url, "data:") || strings.HasPrefix(url, "blob:") {
			return match
		}

		localPath := d.getResource(url, "")
		if localPath != "" && localPath != url {
			return strings.Replace(match, url, localPath, 1)
		}
		return match
	}

	result := moduleImportPattern.ReplaceAllStringFunc(scriptText, rewriteImport)
	result = moduleSideEffectImportPattern.ReplaceAllStringFunc(result, rewriteImport)
	return result
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

	d.log("> Injected w-mod-js and w-mod-ix classes")
}

func (d *WebsiteDownloader) injectImportMap(doc *goquery.Document) {
	mappings := make(map[string]string)

	d.resourceCacheMu.Lock()
	for absURL, localPath := range d.resourceCache {
		// Only map JS assets that look like CDN modules
		absLower := strings.ToLower(absURL)
		isCDN := strings.Contains(absLower, "/npm/") ||
			strings.Contains(absLower, "jsdelivr") ||
			strings.Contains(absLower, "unpkg") ||
			strings.Contains(absLower, "skypack") ||
			strings.Contains(absLower, "esm.sh")
		isModule := strings.HasSuffix(absLower, ".js") || strings.HasSuffix(absLower, ".mjs")
		if isCDN && isModule {
			mappings[absURL] = localPath
		}
	}
	d.resourceCacheMu.Unlock()

	if len(mappings) == 0 {
		return
	}

	importMap := map[string]interface{}{
		"imports": mappings,
	}
	jsonData, err := json.MarshalIndent(importMap, "", "  ")
	if err != nil {
		return
	}

	head := doc.Find("head")
	if head.Length() > 0 {
		head.PrependHtml(fmt.Sprintf(`<script type="importmap" data-webcopy-importmap="true">\n%s\n</script>`, string(jsonData)))
		d.log(fmt.Sprintf("> Injected import map with %d entries", len(mappings)))
	}
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

const maxCSSImportDepth = 5

func (d *WebsiteDownloader) processCSSImports(cssContent, cssBaseURL string, isInline bool, depth int) string {
	if depth > maxCSSImportDepth {
		d.log(fmt.Sprintf("> CSS import depth limit (%d) reached, stopping recursion", maxCSSImportDepth))
		return cssContent
	}
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
			importedCSS = d.processCSSImports(importedCSS, absURL, false, depth+1)
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
