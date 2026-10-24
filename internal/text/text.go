// Package text makes strings from outside the Hive safe to print on a
// terminal.
package text

import (
	"fmt"
	"strings"
	"unicode"
)

// Visible spells out what a terminal would act on or hide rather than show:
// control characters as \xNN (ESC as \x1b, so no escape sequence reaches the
// screen), Unicode format characters (bidi embeddings, overrides and
// isolates, zero-width characters, the BOM, the word joiner) and the blank
// Hangul fillers as \uXXXX. Tabs become spaces.
func Visible(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\t':
			b.WriteString("    ")
		case unicode.IsControl(r):
			fmt.Fprintf(&b, "\\x%02x", r)
		case unicode.Is(unicode.Cf, r), filler(r):
			fmt.Fprintf(&b, "\\u%04x", r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// filler is a Hangul filler letter, which draws as blank space.
func filler(r rune) bool {
	return r == 0x115f || r == 0x1160 || r == 0x3164 || r == 0xffa0
}
