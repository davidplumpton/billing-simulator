package app

import (
	"sort"
	"strings"
)

type keyValueView struct {
	Key   string
	Value string
}

func keyValueViews(values map[string]string) []keyValueView {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	views := make([]keyValueView, 0, len(keys))
	for _, key := range keys {
		views = append(views, keyValueView{Key: key, Value: values[key]})
	}
	return views
}

// displayOptionalValue gives blank optional fields a stable printable label.
func displayOptionalValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "none"
	}
	return value
}

// titleLabel converts stored enum tokens into compact human-readable labels.
func titleLabel(value string) string {
	value = strings.ReplaceAll(value, "_", " ")
	value = strings.ReplaceAll(value, "-", " ")
	words := strings.Fields(value)
	for idx, word := range words {
		if word == "" {
			continue
		}
		words[idx] = strings.ToUpper(word[:1]) + word[1:]
	}
	return strings.Join(words, " ")
}
