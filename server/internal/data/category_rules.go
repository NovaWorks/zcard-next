package data

import (
	"golang.org/x/text/unicode/norm"
	"strings"
)

func CategoryKeywordMatch(name string, keywords, excludes []string, all bool) bool {
	if len(keywords) == 0 {
		return false
	}
	name = strings.ToLower(norm.NFKC.String(name))
	contains := func(word string) bool {
		word = strings.TrimSpace(word)
		return word != "" && strings.Contains(name, strings.ToLower(norm.NFKC.String(word)))
	}
	for _, word := range excludes {
		if contains(word) {
			return false
		}
	}
	matches := 0
	for _, word := range keywords {
		if contains(word) {
			matches++
		}
	}
	return matches > 0 && (!all || matches == len(keywords))
}
