package core

import "testing"

func TestRouteByTierAndAutonomy(t *testing.T) {
	cases := []struct {
		tier     string
		autonomy int
		ceiling  string
		want     string
	}{
		{"T1", 2, "", "self"},
		{"T1", 1, "", "self"},
		{"T2", 2, "", "warden"},
		{"T2", 3, "", "warden"},
		{"T2", 1, "", "person-first"}, // turning autonomy down makes T2 ask first
		{"T3", 2, "", "tribunal"},
		{"T3", 1, "", "person-first"},
		{"", 2, "", "warden"},             // unknown tier is T2
		{"T1", 3, "money", "person"},      // the ceiling outranks a T1 label
		{"T3", 3, "credential", "person"}, // and every autonomy level
	}
	for _, c := range cases {
		if got := Route(c.tier, c.autonomy, c.ceiling); got != c.want {
			t.Errorf("Route(%q, %d, %q) = %q, want %q", c.tier, c.autonomy, c.ceiling, got, c.want)
		}
	}
}

func TestCeilingHit(t *testing.T) {
	for text, want := range map[string]string{
		"npm install left-pad":                 "",
		"buy a domain for $12":                 "money",
		"gh auth refresh -s admin":             "credential",
		"cloudflared tunnel run app":           "open",
		"send an email to the client":          "outward",
		"curl -X POST https://x/charge?usd=10": "money",
		"renew it for $4.99":                   "money",
		"awk '{print $1}' f | xargs echo $2":   "",
		"dump secrets to a file":               "credential",
		"cat ~/.config/gh/hosts.yml":           "credential",
		"cat ~/.netrc":                         "credential",
	} {
		if got := CeilingHit(text); got != want {
			t.Errorf("CeilingHit(%q) = %q, want %q", text, got, want)
		}
	}
}

// The guard runs on the exact script at the point of effect, so a step that
// passed a warden or panel as harmless is still refused when what would run
// is the person's.
func TestCeilingGuardRefusesAfterAllow(t *testing.T) {
	script := "pip install stripe && stripe charges create --amount 500"
	if err := CeilingGuard(script, false); err == nil {
		t.Fatal("a money script ran without the person")
	}
	if err := CeilingGuard(script, true); err != nil {
		t.Fatalf("the person's yes should run it: %v", err)
	}
	if err := CeilingGuard("apt-get install -y jq", false); err != nil {
		t.Fatalf("an ordinary step was refused: %v", err)
	}
}
