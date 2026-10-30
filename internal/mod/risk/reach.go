package risk

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// A step is the asker's own only when its sandbox can carry it out. Its
// sandbox writes its checkout, its box, its home and its tmp, and, for a
// run that is a session in the hive user's home, that home; the daemon's
// workspace (host clones, other runs' checkouts, cache, the records) and the
// machine itself (its disks, its system folders) it can't change, and other
// machines it can't reach at all.

var (
	// otherHostRe is an ask that takes a step on another machine.
	otherHostRe = regexp.MustCompile(`(?i)\b(ssh|scp|rsync|sftp)\s+(-\S+\s+)*([\w.-]+@)?([a-z][\w-]*(\.[\w-]+)*)` +
		`|ssh://[\w.@-]+` +
		`|\b(another|other|a remote|remote|the person's own) (machine|host|box|computer|server)s?\b` +
		`|\bnetwork is unreachable\b|\bno route to host\b`)
	// machineRe is an ask about the machine, not a folder of the asker's.
	machineRe = regexp.MustCompile(`(?i)\bno space left\b|\bdisk (is )?full\b|\bfull disk\b|\b(whole|root|host'?s?) (file ?system|disk)\b` +
		`|\bhost disk\b|\bfree (up )?(the )?(disk|space on)\b`)
	// cantReachRe is the asker saying its sandbox can't carry the step out.
	cantReachRe = regexp.MustCompile(`(?i)\b(can'?t|cannot|can not|unable to|no way to)( \w+)? (see|reach|access|write|delete|remove|touch|get to|free|find)\b[^.\n]{0,60}\b(outside|sandbox|host|daemon|hidden|mounted|machine)` +
		`|\boutside (my|the|its|our|this|an agent'?s?) sandbox\b|\bnot (visible|mounted|reachable|writable) (in|from|inside) (my|the|its|this|an agent'?s?) sandbox\b` +
		`|\bhidden from (my|the|its|every|agents'?|an agent'?s?) sandbox(es)?\b|\bread-only (in|from|inside) (my|the|its|this) sandbox\b`)
	// pathRe finds an absolute path or a read-only $HIVE_ folder an ask names.
	pathRe = regexp.MustCompile(`(?:^|[\s'"(=\x60])(/[\w.@~+-]+(?:/[\w.@~+*-]*)*|\$\{?HIVE_(?:STATE|PROJECTS|KB|MODULES|RUN|SECRETS)\}?)`)
	// toolPathRe is a path an ask runs or reads from, not one it changes.
	toolPathRe = regexp.MustCompile(`^/(usr/(local/)?)?s?bin(/|$)|^/dev/(null|zero|stdin|stdout|stderr|fd|tty|u?random)\b|^/proc/self\b|^/(etc|usr)/?$`)
	// selfHosts are names that are this machine, or the forge.
	selfHosts = map[string]bool{"localhost": true, "127.0.0.1": true, "github.com": true}
)

// beyond says why r's sandbox can't carry out an ask: "host" for another
// machine (only the person reaches those), "path" for a path or a
// machine-wide step outside its reach (a warden's), "" when it can.
func (m *Module) beyond(r core.Run, texts ...string) (kind, what string) {
	t := strings.Join(texts, "\n")
	for _, s := range otherHostRe.FindAllStringSubmatch(t, -1) {
		// ssh WORD is a host when it looks like one: user@ it, a dot or a
		// digit in it ("ssh iiks1"), not "the ssh key" or "ssh config".
		if h := strings.ToLower(s[4]); h != "" && (selfHosts[h] || s[3] == "" && !strings.ContainsAny(h, ".0123456789")) {
			continue
		}
		return "host", strings.TrimSpace(s[0])
	}
	if s := machineRe.FindString(t); s != "" {
		return "path", s
	}
	if s := cantReachRe.FindString(t); s != "" {
		return "path", s
	}
	for _, s := range pathRe.FindAllStringSubmatch(t, -1) {
		if p := s[1]; !m.reaches(r, p) {
			return "path", p
		}
	}
	return "", ""
}

// reaches says whether r's sandbox can write p, as r names it from inside.
func (m *Module) reaches(r core.Run, p string) bool {
	if strings.HasPrefix(p, "$") {
		return false // the read-only $HIVE_ folders
	}
	if toolPathRe.MatchString(p) {
		return true
	}
	p = filepath.Clean(p)
	under := func(dir string) bool {
		return dir != "" && (p == dir || strings.HasPrefix(p, strings.TrimSuffix(dir, "/")+"/"))
	}
	ws := m.C.WS
	for _, d := range []string{r.Checkout, ws.RunHome(r.ID), ws.RunBox(r.ID)} {
		if under(d) {
			return true
		}
	}
	if root, err := filepath.Abs(ws.Root); err == nil && under(root) {
		return false // the daemon's: host clones, other runs' checkouts, cache, records
	}
	for _, d := range []string{"/work", "/hive/box", "/hive/home", "/tmp"} {
		if under(d) {
			return true
		}
	}
	home, err := os.UserHomeDir()
	return err == nil && home != "/" && !r.ReadOnly && !r.OneTurn && !r.Isolated && under(home)
}
