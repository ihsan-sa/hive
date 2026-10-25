package report

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/scan"
)

// maxWrites caps what one run writes upstream (issues, comments and the
// pull request), so a bad day on one install is not a flood there.
const maxWrites = 10

// Marker prefixes on the upstream issues and comments, which H0 reads back.
const (
	issueMarker   = "hive-report:"
	installMarker = "hive-install:"
)

// Upstream is the open-source Hive contributions go to.
func Upstream(cfg core.Config) string {
	if cfg.ReleaseRepo != "" {
		return cfg.ReleaseRepo
	}
	return core.DefaultReleaseRepo
}

var (
	idLike  = regexp.MustCompile(`[0-9a-f]{7,}|\d+`) // a hex run only with a digit in it, see Fingerprint
	spacing = regexp.MustCompile(`\s+`)
)

// Fingerprint names a kind of failure across installs: its kind and its
// message with numbers, ids and spacing taken out, so the same failure
// seen at another time, in another run, is one issue.
func Fingerprint(r Report) string {
	msg := strings.ToLower(r.Message)
	msg = idLike.ReplaceAllStringFunc(msg, func(w string) string {
		if strings.ContainsAny(w, "0123456789") {
			return "#"
		}
		return w // a word such as "defaced"
	})
	msg = spacing.ReplaceAllString(msg, " ")
	sum := sha256.Sum256([]byte(r.Kind + "\n" + core.Clip(msg, 200)))
	return hex.EncodeToString(sum[:6])
}

// installTag stands for the install upstream: not its id, which the reports
// endpoint knows, so a public issue can't be tied to an install's reports.
func installTag(id string) string {
	sum := sha256.Sum256([]byte("hive-upstream:" + id))
	return hex.EncodeToString(sum[:5])
}

// filed is one kind of failure this install contributed.
type filed struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
	Issue   int    `json:"issue,omitempty"`
	Count   int    `json:"count"`
	Last    string `json:"last"`
	Version string `json:"version"`
	OS      string `json:"os"`
	Dirty   bool   `json:"dirty,omitempty"` // seen again since its comment was written
	Log     string `json:"-"`
}

func filedPath(ws core.Workspace) string { return filepath.Join(Dir(ws), "contributed.json") }

func loadFiled(ws core.Workspace) map[string]*filed {
	out := map[string]*filed{}
	if raw, err := os.ReadFile(filedPath(ws)); err == nil {
		json.Unmarshal(raw, &out)
	}
	return out
}

// contribute files each new kind of failure since the person said yes as an
// issue upstream, once, and keeps one comment there per install saying how
// often it saw it; then it offers the Hive's own fixes upstream.
func (m *Module) contribute(cfg core.Config) string {
	ws, forge := m.C.WS, m.C.Effects.Forge
	if forge == nil {
		return "nothing contributed: no forge"
	}
	// Everything here is published, so it passes the publish scan first,
	// as `hive share` does; without its lists nothing goes and the
	// failures wait, unread.
	lists, err := m.publishLists()
	if err != nil {
		return "nothing contributed: the publish scan can't run: " + core.Clip(err.Error(), 300)
	}
	since, _ := Contributing(cfg)
	fails, _, seq, read := m.build(cfg, since, "report.contributed")
	st := loadFiled(ws)
	for _, r := range fails {
		fp := Fingerprint(r)
		x := st[fp]
		if x == nil {
			x = &filed{Kind: r.Kind, Message: m.private(r.Message)}
			st[fp] = x
		}
		x.Count++
		x.Last, x.Version, x.OS, x.Log, x.Dirty = r.At, r.Version, r.OS, m.private(r.Log), true
	}
	up, tag := Upstream(cfg), installTag(cfg.InstallID)
	fps := make([]string, 0, len(st))
	for fp := range st {
		fps = append(fps, fp)
	}
	sort.Slice(fps, func(i, j int) bool { return st[fps[i]].Last < st[fps[j]].Last })
	writes, opened, failed := 0, 0, ""
	var stopped []string
	for _, fp := range fps {
		x := st[fp]
		if !x.Dirty || writes >= maxWrites {
			continue
		}
		title := "Install failure (" + x.Kind + "): " + core.Clip(x.Message, 80)
		body := fmt.Sprintf("An install that said yes to contributing hit this failure (docs/report.md).\n\n- kind: `%s`\n- version: `%s`\n- os: `%s`\n\n%s\n", x.Kind, x.Version, x.OS, x.Message)
		if x.Log != "" {
			body += "\n```\n" + core.Clip(strings.ReplaceAll(x.Log, "```", "'''"), 1500) + "\n```\n"
		}
		times := "once"
		if x.Count > 1 {
			times = fmt.Sprintf("%d times", x.Count)
		}
		comment := fmt.Sprintf("Seen on install %s %s, the last at %s on %s (%s).", tag, times, x.Last, x.Version, x.OS)
		if why := held("issue "+fp, lists, title, body, comment); why != "" {
			// Not filed, now or for this occurrence; a later one tries again.
			x.Dirty = false
			stopped = append(stopped, why)
			continue
		}
		if x.Issue == 0 {
			if forge.Name() == "github" {
				// CreateIssue's own marker check reads only the newest
				// issues; another install's may be older, so the
				// fingerprint is looked up first. A failed lookup waits
				// rather than risk a duplicate.
				n, err := findIssue(up, fp)
				if err != nil {
					failed = err.Error()
					continue
				}
				x.Issue = n
			}
		}
		if x.Issue == 0 {
			iss, err := forge.CreateIssue(up, title, body, "<!-- "+issueMarker+fp+" -->")
			writes++
			if err != nil {
				failed = err.Error()
				continue
			}
			x.Issue, opened = iss.Number, opened+1
		}
		err := forge.StatusComment(up, x.Issue, "<!-- "+installMarker+tag+" -->", comment)
		writes++
		if err != nil {
			failed = err.Error()
			continue
		}
		x.Dirty = false
	}
	raw, _ := json.Marshal(st)
	if err := core.WriteFileAtomic(filedPath(ws), raw, 0o600); err != nil {
		return "nothing contributed: " + err.Error()
	}
	if len(fails) > 0 {
		m.C.J.MustAppend("report.contributed", map[string]any{"seq": seq, "outbox": read, "failures": len(fails), "opened": opened})
	}
	out := fmt.Sprintf("contributed %d failures, %d new issues on %s", len(fails), opened, up)
	if failed != "" {
		out += "; " + core.Clip(failed, 200)
	}
	if len(stopped) > 0 {
		out += fmt.Sprintf("; the publish scan held %d: %s", len(stopped), strings.Join(stopped, "; "))
	}
	if pr := m.offerFixes(cfg, since, maxWrites-writes, lists); pr != "" {
		out += "; " + pr
	}
	return out
}

// offerFixes offers what this Hive merged into the Hive's own code upstream,
// as one pull request from the person's fork, the repo they chose for the
// Hive's own project. A Hive working in the upstream itself has nothing to
// offer; nothing merged before the yes, or already offered, is said again.
func (m *Module) offerFixes(cfg core.Config, since time.Time, room int, lists scan.Lists) string {
	forge, up := m.C.Effects.Forge, Upstream(cfg)
	var did []string
	for _, p := range m.C.Projects.List() {
		if !p.Self || strings.EqualFold(p.Repo, up) || room < 2 {
			continue
		}
		mark := "report.offered"
		var last, seq int64
		type merged struct {
			seq   int64
			title string
		}
		var all []merged
		m.C.J.Replay(func(e core.Event) {
			seq = e.Seq
			switch e.Kind {
			case mark:
				var o struct {
					Project string
					Seq     int64
				}
				if e.Decode(&o) == nil && o.Project == p.Name {
					last = o.Seq
				}
			case "task.saved":
				var t struct{ ID, Project, Stage, Title string }
				if e.Decode(&t) == nil && t.Project == p.Name && t.Stage == "merged" && !e.At.Before(since) {
					all = append(all, merged{e.Seq, m.private(core.Clip(t.Title, 200)) + " (" + t.ID + ")"})
				}
			}
		})
		fresh := 0
		var titles []string
		for _, x := range all[max(0, len(all)-50):] {
			titles = append(titles, x.title)
		}
		for _, x := range all {
			if x.seq > last {
				fresh++
			}
		}
		if fresh == 0 {
			continue
		}
		owner, _, _ := strings.Cut(p.Repo, "/")
		branch := p.Branch
		if branch == "" {
			branch = forge.DefaultBranch(p.Repo)
		}
		body := "Changes a Hive made to the Hive's own code on its fork, offered by its install because the person said yes to contributing (docs/report.md). Each passed that Hive's own gate and review; judge them as any outside contribution.\n"
		title := "Changes from an install's fork"
		list := "Merged on the fork since its install said yes to contributing:\n"
		for _, t := range titles {
			list += "- " + t + "\n"
		}
		// The fork's owner is in the pull request's head, which a fork's
		// offer can't leave out; the text it carries passes the scan.
		if why := held("offer "+p.Name, lists, title, body, list); why != "" {
			did = append(did, "the publish scan held the offer from "+p.Name+": "+why)
			continue
		}
		pr, err := forge.CreatePR(up, owner+":"+branch, forge.DefaultBranch(up), title, body)
		room--
		if err != nil {
			did = append(did, "couldn't offer "+p.Name+": "+core.Clip(err.Error(), 200))
			continue
		}
		forge.StatusComment(up, pr.Number, "<!-- hive-offer:"+installTag(cfg.InstallID)+" -->", list)
		room--
		m.C.J.MustAppend(mark, map[string]any{"project": p.Name, "seq": seq, "pr": pr.Number, "changes": fresh})
		did = append(did, fmt.Sprintf("offered %d new changes from %s as %s#%d", fresh, p.Repo, up, pr.Number))
	}
	return strings.Join(did, "; ")
}

// private scrubs s as a report is and also takes out the person's own
// repositories and the accounts that own them, which a report to the
// developer may name but a public issue, or a batch, must not.
func (m *Module) private(s string) string {
	s = Scrub(s, m.C.WS.Root)
	upOwner, _, _ := strings.Cut(Upstream(m.C.WS.LoadConfig()), "/")
	for _, p := range m.C.Projects.List() {
		owner, _, ok := strings.Cut(p.Repo, "/")
		if !ok || strings.EqualFold(owner, upOwner) {
			continue
		}
		s = strings.ReplaceAll(s, p.Repo, "[repo]")
		if len(owner) >= 3 {
			s = regexp.MustCompile(`(?i)\b`+regexp.QuoteMeta(owner)+`\b`).ReplaceAllString(s, "[owner]")
		}
	}
	return s
}
