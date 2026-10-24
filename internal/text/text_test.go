package text

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

func TestVisibleSpellsOutHiddenCharacters(t *testing.T) {
	for in, want := range map[string]string{
		"five_hour":                   "five_hour",
		"\x1b[2J\x1b[Hfake":           `\x1b[2J\x1b[Hfake`,
		"\u009b31mred":                `\x9b31mred`,
		"a\rb\nc\x00d\x7f":            `a\x0db\x0ac\x00d\x7f`,
		"\u202eevil\u202c":            `\u202eevil\u202c`,
		"\u2066x\u2069":               `\u2066x\u2069`,
		"zero\u200bwidth\ufeff\u2060": `zero\u200bwidth\ufeff\u2060`,
		"\u3164blank":                 `\u3164blank`,
		"a\tb":                        "a    b",
		"seven_day 日本":                "seven_day 日本",
		"bad\xff\xfe utf8":            `bad\xff\xfe utf8`,
		"cut \xe6\x97":                `cut \xe6\x97`,
		"\xc0\x80":                    `\xc0\x80`,
		"cut \xef\xbf":                `cut \xef\xbf`,
		"a real \ufffd":               "a real \ufffd",
	} {
		got := Visible(in)
		if got != want {
			t.Errorf("Visible(%q) = %q, want %q", in, got, want)
		}
		if !utf8.ValidString(got) {
			t.Errorf("Visible(%q) = %q, not valid UTF-8", in, got)
		}
		if i := strings.IndexFunc(got, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }); i >= 0 {
			t.Errorf("Visible(%q) still carries a hidden character: %q", in, got)
		}
	}
}
