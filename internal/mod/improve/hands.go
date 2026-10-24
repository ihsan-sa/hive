package improve

// The goals' first milestone is a change "with nobody in between". This counts
// the outside hands each merge and each release took, from the journal alone:
// what the person (or autobox, posting as them) did that the Hive didn't do
// itself. A merge's count goes with its score.measured, a release's with a
// release.counted, and the step-back and sweep digests carry the trend.

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

// Hand is one outside action, linked to the journal event it is.
type Hand struct {
	Seq  int64     `json:"seq"`
	Kind string    `json:"kind"` // answer | step | chat | draft | ready | merge | close | release | update | login
	What string    `json:"what"`
	At   time.Time `json:"at"`
}

// Hands is what a merge or release took from outside; none is unassisted.
type Hands struct {
	Count      int    `json:"count"`
	Unassisted bool   `json:"unassisted"`
	Hands      []Hand `json:"hands,omitempty"`
}

func (h Hands) String() string {
	if h.Unassisted {
		return "unassisted"
	}
	var b []string
	for _, x := range h.Hands {
		b = append(b, fmt.Sprintf("%s@%d", x.Kind, x.Seq))
	}
	return fmt.Sprintf("%d outside (%s)", h.Count, strings.Join(b, ", "))
}

// ReleaseCount is a release's outside hands: one the Hive cut (hive.released)
// or one it didn't, which H0 updated to (ByHand).
type ReleaseCount struct {
	Project string    `json:"project"`
	Tag     string    `json:"tag"`
	SHA     string    `json:"sha,omitempty"`
	Seq     int64     `json:"seq"` // the release's own event
	ByHand  bool      `json:"by_hand,omitempty"`
	Outside Hands     `json:"outside"`
	At      time.Time `json:"at"` // when it was released
}

type taskHands struct {
	created  int64
	project  string
	runs     []string
	pr       int
	issue    int
	prURL    string
	draft    bool
	stage    string
	closedCP bool
	forge    []Hand
	mergeAt  map[string]Hand // merge sha -> the task.saved that first carried it
}

// merge is a task's merge at sha, first recorded at seq.
type merge struct {
	seq           int64
	project, task string
	sha           string
}

type cachedMilestone struct {
	gen int64
	m   Milestone
}

type answered struct {
	seq int64
	at  time.Time
	ask core.Ask
}

type post struct {
	seq        int64
	at         time.Time
	chat, text string
}

type released struct {
	seq               int64
	at                time.Time
	project, tag, sha string
}

type updated struct {
	seq    int64
	at     time.Time
	to, by string
}

// hands indexes the journal for the count; Module.mu guards it.
type hands struct {
	asks       map[string]core.Ask
	answers    []answered
	posts      []post
	roots      map[string]bool // thread roots the person posted
	logins     []Hand
	tasks      map[string]*taskHands
	hiveMerged map[string]bool // merge shas the Hive merged itself (effect.merged)
	handMerged map[string]Hand // merge shas whose PR was merged on the forge before the Hive tried
	merging    map[string]bool // project#pr@sha the Hive tried to merge (effect.merging)
	merges     []merge         // every merge, in journal order: what unlinked hands go to
	gen        int64           // bumped on what the milestone reads, so it is worked out again
	milestones map[string]cachedMilestone
	releases   []released
	updates    []updated
	rolled     map[string]bool // tags an update to rolled back
	counted    map[string]ReleaseCount
}

func newHands() hands {
	return hands{asks: map[string]core.Ask{}, roots: map[string]bool{}, tasks: map[string]*taskHands{}, hiveMerged: map[string]bool{}, handMerged: map[string]Hand{}, merging: map[string]bool{}, milestones: map[string]cachedMilestone{}, rolled: map[string]bool{}, counted: map[string]ReleaseCount{}}
}

// byPerson says an answer is the person's own. Answers recorded before they
// carried who gave them are the person's unless their note says the Hive's.
func byPerson(a core.Ask) bool {
	if a.By != "" {
		return a.By == "person"
	}
	for _, p := range []string{"the Hive", "the warden", "a tribunal", "renewed on its own", "renewed outside the Hive"} {
		if strings.HasPrefix(a.Note, p) {
			return false
		}
	}
	return true
}

// fromPerson is a chat post by the person; autobox posts as them.
func fromPerson(from string) bool { return from == "person" || from == "autobox" }

// applyHands indexes e; m.mu is held.
func (m *Module) applyHands(e core.Event) {
	h := &m.hands
	switch e.Kind {
	case "score.measured", "release.counted", "hive.updated", "hive.rolledback", "task.saved", "chat.posted":
		h.gen++
	}
	switch e.Kind {
	case "person.asked":
		var a core.Ask
		if e.Decode(&a) == nil {
			h.asks[a.ID] = a
		}
	case "person.answered":
		var x core.Ask
		if e.Decode(&x) != nil || !byPerson(x) {
			return
		}
		a, ok := h.asks[x.ID]
		if !ok || a.Kind == "login" || a.Kind == "told" { // a login renewed by hand counts as itself
			return
		}
		a.Answer, a.Note, a.By = x.Answer, x.Note, x.By
		h.answers = append(h.answers, answered{e.Seq, e.At, a})
	case "chat.posted":
		var x map[string]string
		if e.Decode(&x) != nil || !fromPerson(x["from"]) {
			return
		}
		if x["id"] != "" && x["thread"] == x["id"] {
			h.roots[x["id"]] = true
		}
		h.posts = append(h.posts, post{e.Seq, e.At, x["chat"], core.Clip(x["text"], 4000)})
	case "login.renewed":
		var x map[string]any
		if e.Decode(&x) == nil && x["by"] == "hand" {
			h.logins = append(h.logins, Hand{Seq: e.Seq, Kind: "login", What: fmt.Sprintf("the %v login renewed by hand", x["login"]), At: e.At})
		}
	case "effect.merging":
		var x struct {
			Project, SHA string
			PR           int
		}
		if e.Decode(&x) == nil {
			h.merging[fmt.Sprintf("%s#%d@%s", x.Project, x.PR, x.SHA)] = true
		}
	case "effect.merged":
		var x struct {
			Project  string `json:"project"`
			PR       int    `json:"pr"`
			SHA      string `json:"sha"`
			MergeSHA string `json:"merge_sha"`
			Already  bool   `json:"already"`
		}
		if e.Decode(&x) != nil || x.MergeSHA == "" {
			return
		}
		// The PR was merged on the forge before the Hive's merge ran, and no
		// try of the Hive's own (cut short by a restart) got there first.
		if x.Already && !h.merging[fmt.Sprintf("%s#%d@%s", x.Project, x.PR, x.SHA)] {
			h.handMerged[x.MergeSHA] = Hand{Seq: e.Seq, Kind: "merge", What: fmt.Sprintf("PR #%d merged outside the Hive (at %s)", x.PR, core.Short(x.MergeSHA)), At: e.At}
			return
		}
		h.hiveMerged[x.MergeSHA] = true
	case "task.saved":
		var t work.Task
		if e.Decode(&t) != nil {
			return
		}
		th := h.tasks[t.ID]
		if th == nil {
			th = &taskHands{created: e.Seq, mergeAt: map[string]Hand{}}
			h.tasks[t.ID] = th
		}
		th.project = t.Project
		for _, r := range []string{t.Worker, t.Reviewer} {
			if r != "" && !slices.Contains(th.runs, r) {
				th.runs = append(th.runs, r)
			}
		}
		th.pr, th.issue, th.prURL = t.PR.Number, t.Issue.Number, t.PR.URL
		hand := func(kind, what string) {
			th.forge = append(th.forge, Hand{Seq: e.Seq, Kind: kind, What: fmt.Sprintf("PR #%d %s", t.PR.Number, what), At: e.At})
		}
		// The Hive never drafts a PR or marks one ready: a draft is left for
		// a read outside it, and the merge waits until someone readies it.
		draft := !t.DraftSince.IsZero()
		switch {
		case draft && !th.draft:
			hand("draft", "made a draft outside the Hive")
		case !draft && th.draft:
			hand("ready", "marked ready outside the Hive")
		}
		if t.Stage == work.StageStopped && th.stage != work.StageStopped && t.ByPerson && strings.HasPrefix(t.Reason, "closed on the forge") {
			hand("close", "closed outside the Hive")
		}
		if cp := strings.HasPrefix(t.NoBank, "the person closed"); cp && !th.closedCP {
			hand("close", "closed outside the Hive (a checkpoint's)")
			th.closedCP = true
		}
		for _, sha := range append([]string{t.MergeSHA}, bankSHAs(t)...) {
			if _, ok := th.mergeAt[sha]; sha != "" && !ok {
				h.merges = append(h.merges, merge{e.Seq, t.Project, t.ID, sha})
				th.mergeAt[sha] = Hand{Seq: e.Seq, Kind: "merge", What: fmt.Sprintf("PR #%d merged outside the Hive (at %s)", t.PR.Number, core.Short(sha)), At: e.At}
			}
		}
		th.draft, th.stage = draft, t.Stage
	case "hive.released":
		var x struct{ Project, Tag, SHA string }
		if e.Decode(&x) == nil && x.Tag != "" {
			h.releases = append(h.releases, released{e.Seq, e.At, x.Project, x.Tag, x.SHA})
		}
	case "hive.updated":
		var x struct{ To, By string }
		if e.Decode(&x) == nil && x.To != "" {
			h.updates = append(h.updates, updated{e.Seq, e.At, x.To, x.By})
		}
	case "hive.rolledback":
		var x struct{ From string }
		if e.Decode(&x) == nil {
			h.rolled[x.From] = true
		}
	case "release.counted":
		var rc ReleaseCount
		if e.Decode(&rc) == nil {
			h.counted[rc.Project+"@"+rc.Tag] = rc
		}
	}
}

func bankSHAs(t work.Task) []string {
	var out []string
	for _, b := range t.Banks {
		out = append(out, b.MergeSHA)
	}
	return out
}

// linkOf is what an answered ask is about: a task's id, "release", or ""
// when it names neither. m.mu is held.
func (m *Module) linkOf(a core.Ask) string {
	if a.Kind == "release" || a.Kind == "upstream" {
		return "release"
	}
	for id, th := range m.hands.tasks {
		if a.Ref != "" && strings.Contains(a.Ref, id) || a.From != "" && slices.Contains(th.runs, a.From) {
			return id
		}
	}
	return ""
}

func answerHand(x answered) Hand {
	kind, what := "answer", fmt.Sprintf("the person answered %q to a %s ask: %s", x.ask.Answer, x.ask.Kind, core.Clip(x.ask.Title, 100))
	if x.ask.Kind == "request" { // a warden's or tribunal's step put to the person
		kind = "step"
		what = fmt.Sprintf("the person answered %q to a step: %s", x.ask.Answer, core.Clip(x.ask.Title, 100))
	}
	return Hand{Seq: x.seq, Kind: kind, What: what, At: x.at}
}

func postHand(p post) Hand {
	return Hand{Seq: p.seq, Kind: "chat", What: "the person in " + p.chat + ": " + core.Clip(strings.Join(strings.Fields(p.text), " "), 100), At: p.at}
}

// numbered matches a mention of PR or issue n: #n, /pull/n, /issues/n,
// "PR n" or "issue n", with no digit after it.
func numbered(text string, n int) bool {
	if n == 0 {
		return false
	}
	num := strconv.Itoa(n)
	for _, pre := range []string{"#", "/pull/", "/issues/", "PR ", "issue "} {
		for rest := text; ; {
			i := strings.Index(rest, pre+num)
			if i < 0 {
				break
			}
			end := i + len(pre) + len(num)
			if end == len(rest) || rest[end] < '0' || rest[end] > '9' {
				return true
			}
			rest = rest[end:]
		}
	}
	return false
}

// names says whether a post names the task: its id, its PR or its issue. A
// number counts only outside another project's chat.
func (th *taskHands) names(id string, p post) bool {
	if strings.Contains(p.text, id) || th.prURL != "" && strings.Contains(p.text, th.prURL) {
		return true
	}
	if other, ok := strings.CutPrefix(p.chat, "project:"); ok && other != th.project {
		return false
	}
	return numbered(p.text, th.pr) || numbered(p.text, th.issue)
}

func finish(hs []Hand) Hands {
	sort.Slice(hs, func(i, j int) bool { return hs[i].Seq < hs[j].Seq })
	hs = slices.CompactFunc(hs, func(a, b Hand) bool { return a.Seq == b.Seq && a.Kind == b.Kind })
	return Hands{Count: len(hs), Unassisted: len(hs) == 0, Hands: hs}
}

// HandsFor counts what task's merge at sha took from outside, up to seq upto:
// from the task's first record on, its PR's draft, ready, close or merge the
// Hive didn't make, the person's answers about it and posts naming it; and
// anything outside that names no task or release (an answer, a login renewed
// by hand) when this is the next merge after it, so each counts once.
func (m *Module) HandsFor(task, sha string, upto int64) Hands {
	m.mu.Lock()
	defer m.mu.Unlock()
	h := &m.hands
	th := h.tasks[task]
	if th == nil {
		return finish(nil)
	}
	in := func(seq int64) bool { return seq >= th.created && seq <= upto }
	var hs []Hand
	for _, x := range th.forge {
		if in(x.Seq) {
			hs = append(hs, x)
		}
	}
	if x, ok := h.handMerged[sha]; ok && x.Seq <= upto {
		hs = append(hs, x) // merged on the forge, then recorded by the Hive's merge
	} else if x, ok := th.mergeAt[sha]; ok && in(x.Seq) && !h.hiveMerged[sha] {
		hs = append(hs, x) // ended as landed, with no merge of the Hive's
	}
	// next says this merge is the first after seq in project ("" is any).
	next := func(seq int64, project string) bool {
		for _, x := range h.merges {
			if x.seq > seq && (project == "" || x.project == project) {
				return x.task == task && x.sha == sha && x.seq <= upto
			}
		}
		return false
	}
	for _, x := range h.answers {
		if x.seq > upto {
			continue
		}
		link := m.linkOf(x.ask)
		if link == task && in(x.seq) || link == "" && (x.ask.Project == "" || x.ask.Project == th.project) && next(x.seq, x.ask.Project) {
			hs = append(hs, answerHand(x))
		}
	}
	for _, p := range h.posts {
		if in(p.seq) && th.names(task, p) {
			hs = append(hs, postHand(p))
		}
	}
	for _, l := range h.logins {
		if next(l.Seq, "") {
			hs = append(hs, l)
		}
	}
	return finish(hs)
}

// releaseHands counts a release's outside hands between the project's
// previous release (from) and its own event (upto): the person's answers to
// release asks, or to asks naming no task, posts naming its tag or commit,
// and logins renewed by hand.
func (m *Module) releaseHands(project, tag, sha string, from, upto int64) []Hand {
	h := &m.hands
	in := func(seq int64) bool { return seq > from && seq <= upto }
	var hs []Hand
	for _, x := range h.answers {
		if in(x.seq) && (x.ask.Project == "" || x.ask.Project == project) {
			if link := m.linkOf(x.ask); link == "release" || link == "" {
				hs = append(hs, answerHand(x))
			}
		}
	}
	for _, p := range h.posts {
		if in(p.seq) && (strings.Contains(p.text, tag) || len(sha) >= 7 && strings.Contains(p.text, core.Short(sha))) {
			hs = append(hs, postHand(p))
		}
	}
	for _, l := range h.logins {
		if in(l.Seq) {
			hs = append(hs, l)
		}
	}
	return hs
}

// CountReleases records release.counted for each release not yet counted:
// each the Hive cut, and each H0 updated to that it didn't cut, which is a
// release by hand.
func (m *Module) CountReleases() {
	self := ""
	for _, p := range m.C.Projects.List() {
		if p.Self {
			self = p.Name
			break
		}
	}
	m.mu.Lock()
	h := &m.hands
	type rel struct {
		released
		byHand bool
	}
	var all []rel
	cut := map[string]bool{}
	for _, r := range h.releases {
		all = append(all, rel{r, false})
		cut[r.tag] = true
	}
	for _, u := range h.updates {
		if !cut[u.to] {
			cut[u.to] = true
			all = append(all, rel{released{u.seq, u.at, self, u.to, ""}, true})
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].seq < all[j].seq })
	var todo []ReleaseCount
	prev := map[string]int64{}
	for _, r := range all {
		from := prev[r.project]
		prev[r.project] = r.seq
		if _, ok := h.counted[r.project+"@"+r.tag]; ok {
			continue
		}
		hs := m.releaseHands(r.project, r.tag, r.sha, from, r.seq)
		if r.byHand {
			hs = append(hs, Hand{Seq: r.seq, Kind: "release", What: r.tag + " was released outside the Hive (H0 updated to it)", At: r.at})
		}
		todo = append(todo, ReleaseCount{Project: r.project, Tag: r.tag, SHA: r.sha, Seq: r.seq, ByHand: r.byHand, Outside: finish(hs), At: r.at})
	}
	m.mu.Unlock()
	for _, rc := range todo {
		m.C.J.MustAppend("release.counted", rc)
	}
}

// Releases are a project's counted releases, oldest first.
func (m *Module) Releases(project string) []ReleaseCount {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ReleaseCount
	for _, rc := range m.hands.counted {
		if rc.Project == project {
			out = append(out, rc)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out
}

// Milestone is the goals' first milestone read from the record: a change the
// Hive proposed, kept by the keep-or-revert loop, merged and released with no
// outside hand, and H0 updated to that release by itself.
type Milestone struct {
	Task              string `json:"task,omitempty"`
	SHA               string `json:"sha,omitempty"`
	Proposed          bool   `json:"proposed"`
	Kept              bool   `json:"kept"`
	Unassisted        bool   `json:"unassisted"`
	Release           string `json:"release,omitempty"`
	ReleaseUnassisted bool   `json:"release_unassisted"`
	Updated           bool   `json:"updated"`
	Steps             int    `json:"steps"` // how many of the five hold, in order
	Line              string `json:"line"`
}

// Met says all five steps hold.
func (x Milestone) Met() bool { return x.Steps == 5 }

// proposed says the Hive itself proposed t: not the person's ask, nor a brief
// the lead filed in a thread the person started. m.mu is held.
func (m *Module) proposed(t work.Task) bool {
	if t.From == "" || t.From == "person" {
		return false
	}
	if t.Kind == "revert" || t.FollowUp != "" {
		return true
	}
	return t.Thread == "" || !m.hands.roots[t.Thread]
}

// Milestone is how far the project's merges got towards the milestone: the
// merge that got furthest, the newest of those.
func (m *Module) Milestone(project string) Milestone {
	m.mu.Lock()
	gen := m.hands.gen
	if c, ok := m.hands.milestones[project]; ok && c.gen == gen {
		m.mu.Unlock()
		return c.m
	}
	m.mu.Unlock()
	scores, rels := m.Scores(project), m.Releases(project)
	m.mu.Lock()
	updated := map[string]bool{}
	for _, u := range m.hands.updates {
		if u.by != "hand" && !m.hands.rolled[u.to] {
			updated[u.to] = true
		}
	}
	m.mu.Unlock()
	best := Milestone{}
	for _, ms := range scores {
		if ms.Task == "" || ms.NoResult {
			continue
		}
		t, ok := m.Work.Get(ms.Task)
		if !ok {
			continue
		}
		m.mu.Lock()
		x := Milestone{Task: ms.Task, SHA: ms.SHA, Proposed: m.proposed(t), Kept: ms.Kept, Unassisted: ms.Outside != nil && ms.Outside.Unassisted}
		m.mu.Unlock()
		for _, rc := range rels { // the first release after it holds it
			if rc.At.After(ms.At) {
				x.Release, x.ReleaseUnassisted, x.Updated = rc.Tag, rc.Outside.Unassisted && !rc.ByHand, updated[rc.Tag]
				break
			}
		}
		for _, ok := range []bool{x.Proposed, x.Kept, x.Unassisted, x.ReleaseUnassisted, x.Updated} {
			if !ok {
				break
			}
			x.Steps++
		}
		if x.Steps >= best.Steps {
			best = x
		}
	}
	best.Line = best.line()
	m.mu.Lock()
	m.hands.milestones[project] = cachedMilestone{gen, best}
	m.mu.Unlock()
	return best
}

func (x Milestone) line() string {
	if x.Task == "" {
		return "milestone 0/5: no merge measured yet"
	}
	mark := func(ok bool) string {
		if ok {
			return "yes"
		}
		return "no"
	}
	rel := "no release yet"
	if x.Release != "" {
		rel = x.Release
	}
	return fmt.Sprintf("milestone %d/5: proposed by the Hive %s, kept %s, unassisted merge %s, unassisted release %s, H0 updated %s (task %s at %s, %s)",
		x.Steps, mark(x.Proposed), mark(x.Kept), mark(x.Unassisted), mark(x.ReleaseUnassisted), mark(x.Updated), x.Task, core.Short(x.SHA), rel)
}

// trendWindow is the width of one window of the digest's outside-hands trend.
const trendWindow = 6 * time.Hour

// handsTrend is the digest's section on outside hands: per window, newest
// first, merges and releases with how many outside actions they took.
func (m *Module) handsTrend(project string) string {
	type win struct{ merges, hands, free, rels, relFree int }
	wins := map[time.Time]*win{}
	at := func(t time.Time) *win {
		k := t.UTC().Truncate(trendWindow)
		if wins[k] == nil {
			wins[k] = &win{}
		}
		return wins[k]
	}
	since := time.Now().Add(-48 * time.Hour)
	for _, ms := range m.Scores(project) {
		if ms.Outside == nil || ms.At.Before(since) {
			continue
		}
		w := at(ms.At)
		w.merges++
		w.hands += ms.Outside.Count
		if ms.Outside.Unassisted {
			w.free++
		}
	}
	for _, rc := range m.Releases(project) {
		if rc.At.Before(since) {
			continue
		}
		w := at(rc.At)
		w.rels++
		if rc.Outside.Unassisted && !rc.ByHand {
			w.relFree++
		}
	}
	var keys []time.Time
	for k := range wins {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].After(keys[j]) })
	var b strings.Builder
	b.WriteString("Outside hands ('nobody in between'), per 6-hour window over two days, newest first:\n")
	if len(keys) == 0 {
		b.WriteString("- no merge or release counted yet\n")
	}
	for _, k := range keys {
		w := wins[k]
		per := "-"
		if w.merges > 0 {
			per = strconv.FormatFloat(float64(w.hands)/float64(w.merges), 'f', 1, 64)
		}
		fmt.Fprintf(&b, "- %s-%s: %d merges, %d outside actions (%s per merge), %d unassisted; %d releases, %d unassisted\n",
			k.Format("01-02 15:04"), k.Add(trendWindow).Format("15:04"), w.merges, w.hands, per, w.free, w.rels, w.relFree)
	}
	b.WriteString(m.Milestone(project).Line + "\n")
	return b.String()
}
