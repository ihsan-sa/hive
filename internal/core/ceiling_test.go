package core

import (
	"strings"
	"testing"
)

func TestRouteByTierAndAutonomy(t *testing.T) {
	cases := []struct {
		tier     string
		autonomy int
		ceiling  string
		want     string
	}{
		{"T1", 2, "", "warden"}, // a T1 ask is a step the asker can't take itself (#1991)
		{"T1", 1, "", "warden"}, // inside the Hive: no yes first
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
	if err := CeilingGuard(script, nil, false); err == nil {
		t.Fatal("a money script ran without the person")
	}
	if err := CeilingGuard(script, nil, true); err != nil {
		t.Fatalf("the person's yes should run it: %v", err)
	}
	if err := CeilingGuard("apt-get install -y jq", nil, false); err != nil {
		t.Fatalf("an ordinary step was refused: %v", err)
	}
}

// The person's floor raises a step that touches it, whoever approved it.
func TestFloor(t *testing.T) {
	floor := []string{"/srv/app", "*.pem", "billing-db"}
	for text, want := range map[string]string{
		"rsync -a build/ /srv/app/":     "/srv/app",
		"cp key.pem /tmp/x":             "*.pem",
		"cat ./certs/server.pem":        "*.pem",
		`psql "billing-db" -c 'select'`: "billing-db",
		"npm install left-pad":          "",
		"cp pem.txt /tmp":               "",
	} {
		if got := FloorHit(floor, text); got != want {
			t.Errorf("FloorHit(%q) = %q, want %q", text, got, want)
		}
	}
	if FloorHit(nil, "ls /srv/app") != "" {
		t.Error("an empty floor raised a step")
	}
	if err := CeilingGuard("cp new.pem /etc/ssl", floor, false); err == nil {
		t.Fatal("a floor script ran without the person")
	}
	if err := CeilingGuard("cp new.pem /etc/ssl", floor, true); err != nil {
		t.Fatalf("the person's yes should run it: %v", err)
	}
}

// An ask that names a ceiling word only to rule it out is not the person's;
// one that does the thing still is, and the script guard counts every mention.
func TestAskCeilingSkipsRuledOutMentions(t *testing.T) {
	for _, text := range []string{
		"install Ollama; do not set OLLAMA_HOST=0.0.0.0",
		"no credentials needed",
		"it needs no API key",
		"serve it on loopback only: listen on 127.0.0.1:11434",
		"listen on localhost only",
		"never expose the port",
		"runs without a token",
		"we won't send an email to anyone",
		"Don't open a port, it stays local",
	} {
		if k, p := AskCeilingMatch(text); k != "" {
			t.Errorf("AskCeilingMatch(%q) = %q (%q), want none", text, k, p)
		}
		if CeilingGuard(text, nil, false) == nil {
			t.Errorf("CeilingGuard let %q run as a script", text)
		}
	}
	for text, want := range map[string]string{
		"set OLLAMA_HOST=0.0.0.0 so the LAN can reach it":                                 "open",
		"no root needed, but open a port for it":                                          "open",
		"don't forget to open the port":                                                   "open",
		"not just the cache, expose the port too":                                         "open",
		"do not set OLLAMA_HOST=0.0.0.0, then listen on 0.0.0.0":                          "open",
		"listen on 192.168.1.4":                                                           "open",
		"no admin needed and the api key goes in .env":                                    "credential",
		"no install step. Copy the token from ~/.netrc":                                   "credential",
		"no credentials needed\ncat ~/.config/gh/hosts.yml":                               "credential",
		"nothing outward; buy a domain for $12":                                           "money",
		"run it without the firewall":                                                     "open",
		"no firewall needed on this box":                                                  "open",
		"do not block inbound connections":                                                "open",
		"never use ufw here":                                                              "open",
		"systemctl disable --now firewalld":                                               "open",
		"don't spend more than $50 on a domain":                                           "money",
		"don't pay more than $10":                                                         "money",
		"no subscription, a one-off charge of $40":                                        "money",
		"don't listen on more than one address":                                           "open",
		"do not expose over two ports":                                                    "open",
		"do not revoke the egress grant for 127.0.0.1:11434":                              "open",
		"no egress.local change, just restart":                                            "open",
		"Ollama can't be reached from the LAN without OLLAMA_HOST=0.0.0.0; please set it": "open",
		"it won't work without your API key":                                              "credential",
		"doesn't work without listening on 0.0.0.0":                                       "open",
		"no reason not to expose port 11434":                                              "open",
		"it cannot run without a token":                                                   "credential",
		"don't worry, send an email to the client":                                        "outward",
	} {
		if k, _ := AskCeilingMatch(text); k != want {
			t.Errorf("AskCeilingMatch(%q) = %q, want %q", text, k, want)
		}
	}
	// The guard on the script that would run counts every mention outside a
	// comment, and in a comment every one the comment doesn't rule out.
	for _, script := range []string{
		"# do not set OLLAMA_HOST=0.0.0.0\nOLLAMA_HOST=0.0.0.0 ollama serve",
		"echo 'no tokens' && cat ~/.netrc",
		"export GH_TOKEN=x # no tokens here",
		"# rotate the token first\n./renew.sh",
		"# don't forget the token\n./renew.sh",
		"# no firewall on this box\nsystemctl stop ufw",
		"# no reason not to expose it\n./serve.sh",
		"cat <<EOF\nno tokens\nEOF",
	} {
		if CeilingGuard(script, nil, false) == nil {
			t.Errorf("the script guard let %q run", script)
		}
	}
	// A whole-line comment never runs: what it rules out isn't asked for.
	for _, script := range []string{
		"# do not set OLLAMA_HOST=0.0.0.0\nollama serve",
		"set -u\n  # Scrubbed environment: no tokens, a throwaway HOME/config (so gate.key and Go caches stay in $D), host proxy kept if any.\nenv -i HOME=$D/home go test ./...",
		"# local repos only (no org, no tokens)\ngit clone -q https://github.com/ihsan-sa/hive.git",
	} {
		if err := CeilingGuard(script, nil, false); err != nil {
			t.Errorf("CeilingGuard(%q) = %v, want nil", script, err)
		}
	}
	if CeilingGuard("systemctl disable --now firewalld", nil, false) == nil {
		t.Error("the script guard let a firewall be turned off")
	}
}

// The reason names the category and the words that matched.
func TestCeilingNamesWhatMatched(t *testing.T) {
	why := AskCeiling(nil, "install it", "listen on 0.0.0.0:11434")
	if !strings.Contains(why, "open") || !strings.Contains(why, `"listen on"`) {
		t.Fatalf("got %q", why)
	}
	if why := Ceiling(nil, "stripe charges create"); !strings.Contains(why, "money") || !strings.Contains(why, `"stripe"`) {
		t.Fatalf("got %q", why)
	}
}

// A hand merge of the Hive's own reviewed fix, or a release cut on the
// person's own repository, isn't on the ceiling: it routes to a warden, who
// decides it (the person, 2026-10-24: "the intended behaviour is ALWAYS max
// autonomy"). These are the asks and scripts sent to the person that night.
func TestOwnRepoMergeAndReleaseAreTheWardens(t *testing.T) {
	asks := []string{
		"Squash-merge PR #524 on ihsan-sa/hive by hand at head 42d1f970, if it is open, MERGEABLE, changes only .hive/goals.md, and every check except test (windows-latest) is green.",
		"Cut pre-release v0.1.69 on ihsan-sa/hive at the newest main commit whose ci run passed, unless v0.1.69 already exists. Then report the tag, sha and the release workflow's run.",
	}
	scripts := []string{
		`set -eu
R=ihsan-sa/hive
J=$(gh pr view 392 -R "$R" --json headRefOid,mergeable,state,statusCheckRollup)
H=$(printf '%s' "$J" | jq -r .headRefOid)
BAD=$(printf '%s' "$J" | jq '[.statusCheckRollup[] | (.conclusion // .state) | select(. != "SUCCESS" and . != "NEUTRAL" and . != "SKIPPED")] | length')
[ "$BAD" -eq 0 ] || exit 1
gh pr merge 392 -R "$R" --squash --match-head-commit "$H"
echo "head=$H merge=$(gh pr view 392 -R "$R" --json mergeCommit -q .mergeCommit.oid)"`,
		`set -u
R=ihsan-sa/hive
for SHA in $(gh api "repos/$R/commits?sha=main&per_page=10" --jq ".[].sha"); do
  ok=$(gh run list -R "$R" --workflow ci.yml --commit "$SHA" --json conclusion --jq ".[0].conclusion")
  if [ "$ok" = success ]; then TARGET=$SHA; break; fi
done
gh release create v0.1.69 -R "$R" --target "$TARGET" --prerelease --title v0.1.69 --notes "Hand-cut pre-release at $TARGET (newest main commit with passing CI)."
gh run list -R "$R" --workflow release.yml -L 2`,
	}
	for _, a := range asks {
		if why := AskCeiling(nil, a); why != "" || Route("T2", 3, why) != "warden" {
			t.Errorf("%q is the person's: %s", a, why)
		}
	}
	for _, s := range scripts {
		if err := CeilingGuard(s, nil, false); err != nil {
			t.Errorf("%v\n%s", err, s)
		}
	}
}

// An ask that only mentions a credential, saying what the agent lacks or
// quoting an error, reaches a warden (#1990: a lead's hand-merge ask went to
// the person because its why said "which the lead can't do (no
// credentials)"). One that asks for a credential, or a script that reads
// one, is still the person's.
func TestMentionedCredentialsAreTheWardens(t *testing.T) {
	for _, text := range []string{
		"Hand-merge board PRs #30/#31/#10: a merge on GitHub, which the lead can't do (no credentials)",
		"the lead has no credentials",
		"gh pr checks fails: Resource not accessible by personal access token (HTTP 403)",
		"fine-grained tokens can't use the Checks API",
		"gh api says 'HTTP 401: Bad credentials' for the org repo",
		"the Hive's token doesn't reach the org's repo",
	} {
		if why := AskCeiling(nil, text); why != "" || Route("T2", 3, why) != "warden" {
			t.Errorf("%q is the person's: %s", text, why)
		}
	}
	for text, want := range map[string]string{
		"the API key isn't set; please give me one":             "credential",
		"the token isn't there yet":                             "credential",
		"HTTP 403 from the API; please mint a fine-grained one": "credential",
		"it got a 403, so rotate the token":                     "credential",
		"gh auth refresh -s admin:org":                          "credential",
		"add a token with repo scope":                           "credential",
	} {
		if k, _ := AskCeilingMatch(text); k != want {
			t.Errorf("AskCeilingMatch(%q) = %q, want %q", text, k, want)
		}
	}
	// gh auth setup-git only points git at gh's login, the Hive's own, to
	// push or merge on the person's repo; a token read is still theirs.
	for _, s := range []string{
		"set -eu\ngh auth setup-git\ngit push origin hive/warden-x",
		"gh auth status && gh pr merge 95 --squash",
	} {
		if err := CeilingGuard(s, nil, false); err != nil {
			t.Errorf("%v\n%s", err, s)
		}
	}
	for _, s := range []string{"gh auth token", "gh auth setup-git && gh auth refresh", "cat ~/.config/gh/hosts.yml"} {
		if CeilingGuard(s, nil, false) == nil {
			t.Errorf("the script guard let %q run", s)
		}
	}
}

// A protected merge's diff can talk about money without spending any: a
// hardware project's score is its unit cost (#1993, hifi-receiver-2 PRs #27
// and #29 changing tools/hwcheck.py). The decision ceiling skips money and
// keeps the rest.
func TestDecisionCeilingSkipsMoney(t *testing.T) {
	diff := `Merge "Bench scripts for receiver bring-up and test"? It changes tools/hwcheck.py

These hold the project's goals, score or settings. PR: https://github.com/Hive-RSI-Lab/hifi-receiver/pull/29

diff --git a/tools/hwcheck.py b/tools/hwcheck.py
-    python3 tools/hwcheck.py test    the check: docs/product.md, then every
-                                     board's hwde gates
+    python3 tools/hwcheck.py test    the check: docs/product.md, the software's
+                                     unit tests, then every board's hwde gates
     python3 tools/hwcheck.py score   the first-built tier's unit cost at 100,
                                      in USD, from its board(s)' BOM, printed
                                      only when their gates pass (lower is better)
+def say_cost(c: dict) -> None:
+    print(f"unit cost at {QTY}: ${c['total']:.2f}, price per part from the BOM")`
	if Ceiling(nil, diff) == "" {
		t.Fatal("the strict ceiling no longer sees USD; this test no longer shows the case")
	}
	if why := DecisionCeiling(nil, diff); why != "" {
		t.Errorf("a cost-scored merge is the person's: %s", why)
	}
	for text, want := range map[string]string{
		diff + "\n+subprocess.run(['cat', os.path.expanduser('~/.netrc')])": "credential",
		diff + "\n+# send an email to the client with the BOM":              "outward",
	} {
		if k, _ := DecisionMatch(text); k != want {
			t.Errorf("DecisionMatch(...%q) = %q, want %q", text[len(diff):], k, want)
		}
	}
}
