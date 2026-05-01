package main

import (
	"io"
	"net/url"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

func getSiteName(targetURL string) string {
	parsed, err := url.Parse(targetURL)
	if err != nil {
		return "download"
	}

	domain := strings.Replace(parsed.Host, "www.", "", 1)
	cleanName := regexp.MustCompile(`[^a-zA-Z0-9.-]`).ReplaceAllString(domain, "_")

	if parsed.Path != "" && parsed.Path != "/" {
		pathPart := regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(
			strings.Trim(parsed.Path, "/"), "_")
		if len(pathPart) > 30 {
			pathPart = pathPart[:30]
		}
		cleanName = cleanName + "_" + pathPart
	}

	return cleanName
}

func parseHTML(r io.Reader) (*goquery.Document, error) {
	return goquery.NewDocumentFromReader(r)
}
