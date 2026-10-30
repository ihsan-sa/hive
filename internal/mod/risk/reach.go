package risk

import (
	"regexp"
	"strings"
)

// An agent's run reaches all the hive user can, so the only step past it is
// one on another machine: no agent and no warden reaches that, so it is the
// person's.

var (
	// otherHostRe is an ask that takes a step on another machine.
	otherHostRe = regexp.MustCompile(`(?i)\b(ssh|scp|rsync|sftp)\s+(-\S+\s+)*([\w.-]+@)?([a-z][\w-]*(\.[\w-]+)*)` +
		`|ssh://[\w.@-]+` +
		`|\b(another|other|a remote|remote|the person's own) (machine|host|box|computer|server)s?\b`)
	// selfHosts are names that are this machine, or the forge.
	selfHosts = map[string]bool{"localhost": true, "127.0.0.1": true, "github.com": true}
)

// elsewhere names the other machine an ask's step is on, "" when it is on
// this one.
func elsewhere(texts ...string) string {
	t := strings.Join(texts, "\n")
	for _, s := range otherHostRe.FindAllStringSubmatch(t, -1) {
		// ssh WORD is a host when it looks like one: user@ it, a dot or a
		// digit in it ("ssh host2"), not "the ssh key" or "ssh config".
		if h := strings.ToLower(s[4]); h != "" && (selfHosts[h] || s[3] == "" && !strings.ContainsAny(h, ".0123456789")) {
			continue
		}
		return strings.TrimSpace(s[0])
	}
	return ""
}
