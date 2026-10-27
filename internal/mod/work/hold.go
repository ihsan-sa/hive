package work

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// A merge hold is the person's stop on a project's merges: while it is on,
// the merge step lands nothing outside it (the Hive's own reverts
// and checkpoints included). Held work still goes through its tests,
// reads and checks, and waits as a ready pull request at the last step. The
// person sets it (hive hold), or any agent on the person's word; it ends when
// one of them lifts it or its condition is met. core.Effects.Merge refuses a
// held merge too, so nothing in the Hive merges past it.

// UntilRelease lifts a hold once a release newer than the one out when it was
// set is on the forge (pre-releases count: that is when a release is cut).
const UntilRelease = "release"

// MergeHold is one project's hold on its merges.
type MergeHold struct {
	Project string    `json:"project"`
	Why     string    `json:"why"`             // the person's words
	Only    []int     `json:"only,omitempty"`  // pull requests that may still merge
	Until   string    `json:"until,omitempty"` // "" until lifted, or UntilRelease
	Since   []string  `json:"since,omitempty"` // repo@tag: the newest release on each watched repo when set
	By      string    `json:"by"`              // person, or the run that set it on the person's word
	At      time.Time `json:"at"`
}

// Text is the hold in one line, for status, the control center and a PR.
func (h MergeHold) Text() string {
	s := "merges held"
	if len(h.Only) > 0 {
		var n []string
		for _, pr := range h.Only {
			n = append(n, "#"+strconv.Itoa(pr))
		}
		s += " except " + strings.Join(n, ", ")
	}
	if h.Until == UntilRelease {
		s += " until the next release is out"
	} else {
		s += " until lifted"
	}
	if h.Why != "" {
		s += ": " + core.Clip(h.Why, 160)
	}
	return s + " (" + h.By + ", " + h.At.Format("2006-01-02 15:04Z") + ")"
}

// Hold returns project's hold on its merges, if one is on.
func (m *Module) Hold(project string) (MergeHold, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.holds[project]
	if !ok {
		return MergeHold{}, false
	}
	return *h, true
}

// applyHold keeps the holds as the journal says.
func (m *Module) applyHold(e core.Event) {
	switch e.Kind {
	case "merge.held":
		var h MergeHold
		if e.Decode(&h) == nil && h.Project != "" {
			m.mu.Lock()
			m.holds[h.Project] = &h
			m.mu.Unlock()
		}
	case "merge.lifted":
		var x struct{ Project string }
		if e.Decode(&x) == nil {
			m.mu.Lock()
			delete(m.holds, x.Project)
			m.mu.Unlock()
		}
	}
}

// SetHold puts a hold on h.Project's merges, replacing one that is on.
func (m *Module) SetHold(h MergeHold) (MergeHold, error) {
	p, ok := m.C.Projects.Get(h.Project)
	if !ok {
		return h, fmt.Errorf("no project %q", h.Project)
	}
	h.Why = strings.TrimSpace(h.Why)
	if h.By == "" {
		return h, errors.New("a hold names who set it")
	}
	h.Since = nil
	switch h.Until {
	case "":
	case UntilRelease:
		for _, repo := range m.releaseRepos(p) {
			r, err := m.C.Effects.Forge.LatestRelease(repo)
			if err != nil && !errors.Is(err, core.ErrNotFound) {
				return h, fmt.Errorf("reading the newest release of %s, to know when the next is out: %w", repo, err)
			}
			h.Since = append(h.Since, repo+"@"+r.Tag)
		}
	default:
		return h, fmt.Errorf("until is %q or empty (until lifted), not %q", UntilRelease, h.Until)
	}
	h.At = time.Now().UTC()
	m.holding.Lock()
	m.C.J.MustAppend("merge.held", h)
	m.holding.Unlock()
	m.lookAgain(h.Project) // a changed hold may let one through
	return h, nil
}

// LiftHold ends project's hold on its merges; why says who and why.
func (m *Module) LiftHold(project, by, why string) error {
	if _, ok := m.Hold(project); !ok {
		return fmt.Errorf("%s has no hold on its merges", project)
	}
	m.lift(project, by, why)
	return nil
}

func (m *Module) lift(project, by, why string) {
	m.C.J.MustAppend("merge.lifted", map[string]string{"project": project, "by": by, "why": why})
	m.lookAgain(project)
}

// lookAgain has project's held tasks try their merge on the next tick.
func (m *Module) lookAgain(project string) {
	m.mu.Lock()
	for _, t := range m.tasks {
		if t.Project == project {
			delete(m.later, t.ID)
		}
	}
	m.mu.Unlock()
}

// releaseRepos are the repositories whose next release lifts p's hold: its
// own, and for the Hive's own source the repository releases come from.
func (m *Module) releaseRepos(p core.Project) []string {
	repos := []string{p.Repo}
	if up := m.C.WS.LoadConfig().ReleaseRepo; p.Self && up != "" && up != p.Repo {
		repos = append(repos, up)
	}
	return repos
}

// MergeHeld is why pr on project may not merge now, or "" if it may. A hold
// whose release is out is lifted here; the forge is asked at most once a
// holdGap per project.
func (m *Module) MergeHeld(project string, pr int) string {
	h, ok := m.Hold(project)
	if !ok {
		return ""
	}
	if h.Until == UntilRelease && m.releasedSince(h) {
		return ""
	}
	if slices.Contains(h.Only, pr) {
		return ""
	}
	return h.Text()
}

// releasedSince reports whether a release newer than h's is out, and lifts
// h if so.
func (m *Module) releasedSince(h MergeHold) bool {
	m.mu.Lock()
	if time.Now().Before(m.holdPolled[h.Project]) {
		m.mu.Unlock()
		return false
	}
	m.holdPolled[h.Project] = time.Now().Add(holdGap)
	m.mu.Unlock()
	for _, s := range h.Since {
		i := strings.LastIndex(s, "@")
		if i < 0 {
			continue
		}
		repo, tag := s[:i], s[i+1:]
		r, err := m.C.Effects.Forge.LatestRelease(repo)
		if err != nil || r.Tag == "" || r.Tag == tag {
			continue
		}
		// The forge was asked about h; a hold set since waits for its own release.
		m.holding.Lock()
		now, ok := m.Hold(h.Project)
		if ok && now.At.Equal(h.At) {
			m.lift(h.Project, "the Hive", fmt.Sprintf("release %s is out on %s, which the hold waited for", r.Tag, repo))
		}
		m.holding.Unlock()
		return ok && now.At.Equal(h.At)
	}
	return false
}

// prs are PR numbers as a list, one number, or one string such as the CLI's
// --only "12, #15".
type prs []int

func (l *prs) UnmarshalJSON(b []byte) error {
	var n int
	if json.Unmarshal(b, &n) == nil {
		*l = prs{n}
		return nil
	}
	var one string
	if json.Unmarshal(b, &one) != nil {
		return json.Unmarshal(b, (*[]int)(l))
	}
	*l = nil
	for _, f := range strings.FieldsFunc(one, func(r rune) bool { return r == ',' || r == ' ' }) {
		n, err := strconv.Atoi(strings.TrimPrefix(f, "#"))
		if err != nil {
			return fmt.Errorf("only takes PR numbers, not %q", f)
		}
		*l = append(*l, n)
	}
	return nil
}

// opHold is an agent setting or lifting a hold on the person's word.
func (m *Module) opHold(r core.Run, args json.RawMessage) (any, error) {
	var a struct {
		Project string `json:"project"`
		Why     string `json:"why"`
		Only    prs    `json:"only"`
		Until   string `json:"until"`
		Lift    any    `json:"lift"` // true, or "true" from the CLI's --lift true
	}
	if err := core.Bind(args, &a); err != nil {
		return nil, err
	}
	lift := a.Lift == true || a.Lift == "true"
	if a.Project == "" {
		return nil, errors.New("say which project")
	}
	if err := m.reach(r, a.Project); err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.Why) == "" {
		return nil, errors.New("why: quote the person's words that ask for this")
	}
	if lift {
		if err := m.LiftHold(a.Project, r.ID, a.Why); err != nil {
			return nil, err
		}
		return map[string]string{"project": a.Project, "hold": "lifted"}, nil
	}
	h, err := m.SetHold(MergeHold{Project: a.Project, Why: a.Why, Only: a.Only, Until: a.Until, By: r.ID})
	if err != nil {
		return nil, err
	}
	return map[string]string{"project": a.Project, "hold": h.Text()}, nil
}
