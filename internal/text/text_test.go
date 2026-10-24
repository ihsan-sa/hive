package text

import (
	"strings"
	"testing"
	"unicode"
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
	} {
		got := Visible(in)
		if got != want {
			t.Errorf("Visible(%q) = %q, want %q", in, got, want)
		}
		if i := strings.IndexFunc(got, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }); i >= 0 {
			t.Errorf("Visible(%q) still carries a hidden character: %q", in, got)
		}
	}
}
