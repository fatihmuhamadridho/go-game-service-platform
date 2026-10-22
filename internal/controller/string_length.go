package controller

import (
	"strings"
	"unicode"
	"unicode/utf16"
)

func JSTrim(value string) string {
	return strings.TrimFunc(value, func(r rune) bool { return unicode.IsSpace(r) || r == '\uFEFF' })
}

// JSStringLength matches JavaScript String.length for source API validations.
func JSStringLength(value string) int {
	return len(utf16.Encode([]rune(value)))
}

func JSStringPrefix(value string, maxUnits int) string {
	if maxUnits <= 0 {
		return ""
	}
	units := utf16.Encode([]rune(value))
	if maxUnits > len(units) {
		maxUnits = len(units)
	}
	return string(utf16.Decode(units[:maxUnits]))
}
