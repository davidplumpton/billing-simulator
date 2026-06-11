package app

import (
	"html/template"
	"net/url"
	"strings"
)

func urlQueryEscape(value string) string {
	return strings.ReplaceAll(template.URLQueryEscaper(value), "+", "%20")
}

func appendQueryValue(values url.Values, key, value string) {
	value = strings.TrimSpace(value)
	if value != "" {
		values.Set(key, value)
	}
}
