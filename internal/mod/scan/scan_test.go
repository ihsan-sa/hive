package scan

import (
	"errors"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gen is n characters from set, made at run time so no key-shaped literal
// sits in the source for a forge's push protection to refuse.
func gen(r *rand.Rand, n int, set string) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = set[r.Intn(len(set))]
	}
	return string(b)
}

const (
	alnum = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	upper = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	hexs  = "0123456789abcdef"
	b64   = alnum + "+/"
	url   = alnum + "_-"
	// begin starts a private key's armour, split so this file passes the
	// scan it tests: the repository's tests are published and scanned too.
	begin = "-----" + "BEGIN "
)

// keyLines are lines that each hold one key of a common format, with what
// the scan must call it.
func keyLines() map[string]string {
	r := rand.New(rand.NewSource(1))
	g := func(n int, set string) string { return gen(r, n, set) }
	return map[string]string{
		begin + "OPENSSH PRIVATE KEY-----":                 "a private key",
		begin + "RSA PRIVATE KEY-----":                     "a private key",
		begin + "PGP PRIVATE KEY BLOCK-----":               "a private key",
		"AGE-SECRET-KEY-1" + g(58, upper):                  "an age secret key",
		"GH_TOKEN=ghp_" + g(36, alnum):                     "a GitHub token",
		"gho_" + g(36, alnum):                              "a GitHub token",
		"ghs_" + g(36, alnum):                              "a GitHub token",
		"github_pat_" + g(22, alnum) + "_" + g(59, alnum):  "a GitHub token",
		"ANTHROPIC_API_KEY=sk-ant-api03-" + g(80, url):     "an Anthropic key",
		"OPENAI_API_KEY=sk-proj-" + g(48, url):             "an OpenAI-style key",
		"sk-or-v1-" + g(64, hexs):                          "an OpenAI-style key",
		"sk_live_" + g(24, alnum):                          "a Stripe key",
		"rk_test_" + g(24, alnum):                          "a Stripe key",
		"xoxb-" + g(12, "0123456789") + "-" + g(24, alnum): "a Slack token",
		"https://hooks.slack.com/services/T" + g(8, upper) + "/B" + g(8, upper) + "/" + g(24, alnum): "a Slack webhook",
		"https://discord.com/api/webhooks/1234567890/" + g(60, url):                                  "a Discord webhook",
		"aws_access_key_id = AKIA" + g(16, upper):                                                    "an AWS access key",
		"aws_secret_access_key = " + g(40, b64):                                                      "an AWS secret key",
		"key=AIza" + g(35, url):                                                                      "a Google API key",
		"ya29." + g(60, url):                                                                         "a Google OAuth token",
		"glpat-" + g(20, url):                                                                        "a GitLab token",
		"//registry.npmjs.org/:_authToken=npm_" + g(36, alnum):                                       "an npm token",
		"pypi-AgEIcHlwaS5vcmc" + g(60, url):                                                          "a PyPI token",
		"HF_TOKEN=hf_" + g(34, alnum):                                                                "a Hugging Face token",
		"SG." + g(22, url) + "." + g(43, url):                                                        "a SendGrid key",
		"SK" + g(32, hexs):                                                                           "a Twilio key",
		"bot" + g(9, "123456789") + ":AA" + g(33, url):                                               "a Telegram bot token",
		"M" + g(24, alnum) + "." + g(6, url) + "." + g(27, url):                                      "a Discord bot token",
		"dop_v1_" + g(64, hexs):                                                                      "a DigitalOcean token",
		"shpat_" + g(32, hexs):                                                                       "a Shopify token",
		"DefaultEndpointsProtocol=https;AccountKey=" + g(88, b64):                                    "an Azure storage key",
		"eyJ" + g(20, url) + ".eyJ" + g(30, url) + "." + g(43, url):                                  "a JSON web token",
		"git clone https://ann:" + g(16, alnum) + "@github.com/x/y":                                  "a password in a URL",
		"Authorization: Bearer " + g(40, alnum):                                                      "a bearer token",
		`{"client_secret": "` + g(32, alnum) + `"}`:                                                  "a secret assigned to a name",
		"DB_PASSWORD=" + g(24, alnum):                                                                "a secret assigned to a name",
	}
}

func lists() Lists { return Lists{Public: []string{"**"}, Handles: []string{"nobody-at-all"}} }

func has(hits []Hit, what string) bool {
	for _, h := range hits {
		if h.What == what {
			return true
		}
	}
	return false
}

// Every common key format is caught, and named by its kind.
func TestCatchesEveryKeyFormat(t *testing.T) {
	for line, what := range keyLines() {
		if hits := Text("f", []byte("x\n"+line+"\n"), lists()); !has(hits, what) || hits[0].Line != 2 {
			t.Errorf("%q: want %q on line 2, got %v", line, what, hits)
		}
	}
}

// What a hit says never holds the key itself.
func TestHitNeverPrintsTheKey(t *testing.T) {
	var names []string
	files := map[string][]byte{}
	for line := range keyLines() {
		name := "f" + string(rune('a'+len(names)))
		names, files[name] = append(names, name), []byte(line)
	}
	err := Run(names, func(n string) ([]byte, error) { return files[n], nil }, Lists{Public: []string{"**"}, Handles: []string{"nobody-at-all"}})
	if err == nil {
		t.Fatal("keys passed the scan")
	}
	for line := range keyLines() {
		for _, f := range strings.FieldsFunc(line, func(r rune) bool { return strings.ContainsRune(`=:/."{} `, r) }) {
			if len(f) >= 20 && strings.Contains(err.Error(), f) {
				t.Fatalf("the failure prints a key: %q in\n%v", f, err)
			}
		}
	}
}

// A real key on a line that says it is a fake token is still a key: a label
// never excuses a value.
func TestRealKeyOnAFakeLabelledLine(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	for _, line := range []string{
		"fake := \"ghp_" + gen(r, 36, alnum) + "\" // a fake token for the tests",
		"# example only, not a real key: sk-ant-api03-" + gen(r, 60, url),
		"FAKE_TOKEN=AKIA" + gen(r, 16, upper) + "  # dummy",
		"test_token: " + gen(r, 32, alnum) + " (placeholder)",
		"token: 261024-225610-163a3bc0d0" + "-" + strings.ToLower(gen(r, 32, alnum)),
	} {
		if hits := Text("f", []byte(line), lists()); len(hits) == 0 {
			t.Errorf("a key on a line labelled fake passed: %q", line)
		}
	}
	// What can pass is a value that is no key at all, whatever the line says.
	for _, line := range []string{
		"token := strings.TrimSpace(os.Getenv(\"GH_TOKEN\"))",
		"GH_TOKEN: ${{ github.token }}",
		"ghp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
		"sk-ant-...",
		"password: [secret]",
		"https://x-access-token:${TOKEN}@github.com/me/repo",
		"a brokered org leg with no child token: 261024-225610-163a3bc0d0.",
		"`CheckReach` reads the owner's role as the token's reach: 261024-163142-db224b11e8.",
		"child token: 261024-163142-db224b11e8",
		"token: r-261025-081832-f0ffb403d4-43295337-0",
		"token: s-261025-050219-fa27843a7a-889bd97f-security-0",
	} {
		if hits := Text("f", []byte(line), lists()); len(hits) != 0 {
			t.Errorf("no key, but %q hit: %v", line, hits)
		}
	}
}

// A member's handle is caught in any case, as a whole name, and the failure
// prints no handle: neither the one found nor the other members'.
func TestHandleHitPrintsNoHandles(t *testing.T) {
	l := Lists{Public: []string{"**"}, Handles: []string{"alice-k", "Bob_99", "carol"}}
	files := map[string][]byte{
		"README.md": []byte("Thanks to BOB_99 for the review.\n"),
		"docs/a.md": []byte("carolina and carols are not a handle\nsee github.com/carol/hive\n"),
		"docs/b.md": []byte("annotate, alice-kay\n"),
	}
	err := Run([]string{"README.md", "docs/a.md", "docs/b.md"}, func(n string) ([]byte, error) { return files[n], nil }, l)
	if err == nil {
		t.Fatal("a member's handle passed the scan")
	}
	msg := err.Error()
	if !strings.Contains(msg, "README.md:1: handle 2 of the handle list") || !strings.Contains(msg, "docs/a.md:2: handle 3 of the handle list") || strings.Contains(msg, "docs/b.md") {
		t.Fatalf("want the two hits by position only, got:\n%s", msg)
	}
	for _, h := range l.Handles {
		if strings.Contains(strings.ToLower(msg), strings.ToLower(h)) {
			t.Fatalf("the failure prints the handle %q:\n%s", h, msg)
		}
	}
}

// The deny list's words block in any case and are never printed; a deny
// list put among the published files blocks itself.
func TestDenyListNeverShipsItself(t *testing.T) {
	l := Lists{Public: []string{"**"}, Deny: []string{"Project Nightjar", "box-internal"}, Handles: []string{"nobody-at-all"}}
	files := map[string][]byte{
		"notes.md": []byte("about project NIGHTJAR\n"),
		"deny.txt": []byte("Project Nightjar\nbox-internal\n"),
	}
	err := Run([]string{"notes.md", "deny.txt"}, func(n string) ([]byte, error) { return files[n], nil }, l)
	if err == nil || !strings.Contains(err.Error(), "notes.md:1: word 1 of the deny list") ||
		!strings.Contains(err.Error(), "deny.txt:1: word 1") || !strings.Contains(err.Error(), "deny.txt:2: word 2") {
		t.Fatalf("want the deny words and the shipped list itself to block, got %v", err)
	}
	if strings.Contains(strings.ToLower(err.Error()), "nightjar") || strings.Contains(err.Error(), "box-internal") {
		t.Fatalf("the failure prints a deny word: %v", err)
	}
}

// Only the files on the public list are read; "!glob" leaves some out.
func TestScansOnlyThePublicList(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	key := []byte("ghp_" + gen(r, 36, alnum))
	read := []string{}
	files := map[string][]byte{"README.md": []byte("hello"), "docs/x.md": []byte("hi"), "internal/a_test.go": key, "private/notes": key}
	err := Run([]string{"README.md", "docs/x.md", "internal/a_test.go", "private/notes"}, func(n string) ([]byte, error) {
		read = append(read, n)
		return files[n], nil
	}, Lists{Public: []string{"*.md", "docs/**", "internal/**", "!**/*_test.go"}, Handles: []string{"nobody-at-all"}})
	if err != nil || strings.Join(read, " ") != "README.md docs/x.md" {
		t.Fatalf("want only README.md and docs/x.md read and passing, read %v: %v", read, err)
	}
	if err := Run([]string{"README.md"}, func(string) ([]byte, error) { return nil, nil }, Lists{Handles: []string{"x"}}); err == nil {
		t.Fatal("no public list passed")
	}
	if err := Run([]string{"README.md"}, func(string) ([]byte, error) { return nil, nil }, Lists{Public: []string{"docs/**"}, Handles: []string{"x"}}); err == nil {
		t.Fatal("a public list that names nothing passed")
	}
	if err := Run([]string{"README.md"}, func(string) ([]byte, error) { return nil, errors.New("gone") }, Lists{Public: []string{"**"}, Handles: []string{"x"}}); err == nil {
		t.Fatal("a published file the scan can't read passed")
	}
}

// With no handle list the scan fails, never warns and passes.
func TestNoHandleListFails(t *testing.T) {
	ok := func(string) ([]byte, error) { return []byte("clean"), nil }
	if err := Run([]string{"a"}, ok, Lists{Public: []string{"**"}}); err == nil || !strings.Contains(err.Error(), "no handle list") {
		t.Fatalf("no handles: %v", err)
	}
	none := func(string) string { return "" }
	private := t.TempDir()
	if _, _, err := RunTime(none, private); err == nil || !strings.Contains(err.Error(), "no handle list") {
		t.Fatalf("no handle file: %v", err)
	}
	os.WriteFile(filepath.Join(private, HandlesFile), []byte("# nobody yet\n\n"), 0o600)
	if _, _, err := RunTime(none, private); err == nil {
		t.Fatal("a handle list naming nobody passed")
	}
	os.WriteFile(filepath.Join(private, HandlesFile), []byte("alice-k\nbob, carol # the box\n"), 0o600)
	os.WriteFile(filepath.Join(private, DenyFile), []byte("Project Nightjar\n"), 0o600)
	deny, handles, err := RunTime(none, private)
	if err != nil || strings.Join(handles, " ") != "alice-k bob carol" || strings.Join(deny, "|") != "Project Nightjar" {
		t.Fatalf("from the private files: %v %v %v", deny, handles, err)
	}
	env := func(k string) string { return map[string]string{HandlesEnv: "dave\nerin", DenyEnv: "a word"}[k] }
	deny, handles, err = RunTime(env, private)
	if err != nil || strings.Join(handles, " ") != "dave erin" || strings.Join(deny, "|") != "a word" {
		t.Fatalf("the environment goes first: %v %v %v", deny, handles, err)
	}
}

// Scrub leaves nothing Text would flag: every key goes as its kind (a private
// key with its body, a named secret keeping its name), and so do deny words
// and handles, while placeholders and plain words stay.
func TestScrubLeavesNothingToFlag(t *testing.T) {
	l := Lists{Public: []string{"**"}, Deny: []string{"Project Nightjar"}, Handles: []string{"carol", "alice-k"}}
	var b strings.Builder
	for line, what := range keyLines() {
		if what != "a private key" { // an armour with no end blocks: see below
			b.WriteString("logged " + line + " here\n")
		}
	}
	body := gen(rand.New(rand.NewSource(4)), 64, b64)
	b.WriteString(begin + "OPENSSH PRIVATE KEY-----\n" + body + "\n-----END OPENSSH PRIVATE KEY-----\n")
	b.WriteString(`{"text":"` + begin + `RSA PRIVATE KEY-----\n` + body + `\n-----END RSA PRIVATE KEY-----","next":1}` + "\n")
	b.WriteString("about project NIGHTJAR, by Carol,carol and alice-k; carolina stays\n")
	b.WriteString("ghp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx and token: 261024-225610-163a3bc0d0\n")
	out := string(Scrub([]byte(b.String()), l))
	if hits := Text("f", []byte(out), l); len(hits) != 0 {
		t.Fatalf("the scrubbed text still hits %v:\n%s", hits, out)
	}
	for _, want := range []string{"[a private key]", `{"text":"[a private key]","next":1}`, "[a GitHub token]", "DB_PASSWORD=[a secret assigned to a name]", "about [a deny word], by [a handle],[a handle] and [a handle]; carolina stays", "ghp_xxxx", "token: 261024-225610-163a3bc0d0"} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, body) {
		t.Errorf("a private key's body survived:\n%s", out)
	}
}

// A private key whose lines carry a prefix (a Read tool's line numbers in a
// JSON turn log, quoting, indentation) goes whole, prefixes and all; one
// whose end isn't there keeps its armour, so the scan still blocks it.
func TestScrubPrefixedAndCutOffKeys(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	l := Lists{Public: []string{"**"}, Handles: []string{"nobody-at-all"}}
	b1, b2 := gen(r, 64, b64), gen(r, 40, b64)
	for name, in := range map[string]string{
		"numbered in JSON": `{"type":"tool_result","content":"     1\t` + begin + `RSA PRIVATE KEY-----\n     2\t` + b1 + `\n     3\t` + b2 + `\n     4\t-----END RSA PRIVATE KEY-----\n"}`,
		"quoted":           "> " + begin + "OPENSSH PRIVATE KEY-----\n> " + b1 + "\n> " + b2 + "\n> -----END OPENSSH PRIVATE KEY-----\n",
		"indented":         "key: |\n  " + begin + "EC PRIVATE KEY-----\n  " + b1 + "\n  " + b2 + "\n  -----END EC PRIVATE KEY-----\nnext: 1\n",
	} {
		out := string(Scrub([]byte(in), l))
		if strings.Contains(out, b1) || strings.Contains(out, b2) || !strings.Contains(out, "[a private key]") || len(Text("f", []byte(out), l)) != 0 {
			t.Errorf("%s: want the key gone, got:\n%s", name, out)
		}
	}
	for name, in := range map[string]string{
		"cut off":      `{"content":"     1\t` + begin + `RSA PRIVATE KEY-----\n     2\t` + b1 + `\n     3\t` + b2 + ` (clipped)"}`,
		"end too far":  begin + "RSA PRIVATE KEY-----\n" + strings.Repeat(b1+"\n", maxKeyBlock/64) + "-----END RSA PRIVATE KEY-----\n",
		"a second key": begin + "RSA PRIVATE KEY-----\n" + b1 + "\n" + begin + "EC PRIVATE KEY-----\n" + b2 + "\n-----END EC PRIVATE KEY-----\n",
	} {
		if out := Scrub([]byte(in), l); !has(Text("f", out, l), "a private key") {
			t.Errorf("%s: a key with no end of its own passed the scan after the scrub:\n%s", name, out)
		}
	}
}

// A published file's name is scanned too, and a name that holds a handle is
// never printed: the hits name the file by its place.
func TestFileNameHoldingAHandle(t *testing.T) {
	l := Lists{Public: []string{"**"}, Handles: []string{"alice-k"}}
	err := Run([]string{"README.md", "notes/alice-k/todo.md"}, func(string) ([]byte, error) { return []byte("ask alice-k\n"), nil }, l)
	if err == nil || !strings.Contains(err.Error(), "published file 2:0: handle 1 of the handle list in its name") || !strings.Contains(err.Error(), "published file 2:1: handle 1") {
		t.Fatalf("want the name and its line hit by the file's place, got %v", err)
	}
	if strings.Contains(err.Error(), "alice") {
		t.Fatalf("the failure prints the handle: %v", err)
	}
}
