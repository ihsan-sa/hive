// Package projects creates projects (a repository the Hive works on, with its
// own goals, chat and score) and answers agents' status questions.
package projects

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/demo"
	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/chat"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/improve"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

// Module is the projects module.
type Module struct {
	C       *core.Core
	Chat    *chat.Module
	Work    *work.Module
	Improve *improve.Module

	mu      sync.Mutex // one project setup at a time
	retry   map[string]time.Time
	lastErr map[string]string
	claimed map[string]bool // self projects whose steward record was looked at this run
}

// New registers ops.
func New(c *core.Core, ch *chat.Module, w *work.Module, im *improve.Module) *Module {
	m := &Module{C: c, Chat: ch, Work: w, Improve: im, retry: map[string]time.Time{}, lastErr: map[string]string{}, claimed: map[string]bool{}}
	ops := c.Runs.Ops()
	ops.Register(core.Op{Name: "create_project", Roles: []string{"lead"}, Help: `{"name","repo","description","goals","sharing","visibility","self"} (visibility: private (default) or public: public in public_owners (Hive-RSI-Lab) is made and the person told, public anywhere else waits for their yes; an existing repository keeps its visibility, changing it is the person's; sharing: shared (default), private (its agents read the shared Hive, no one else sees it) or isolated (walled both ways); self: the Hive's own code, in a fork of it, or in the release repository itself only when this Hive is its steward)`, Fn: m.opCreate})
	c.Person.Handle("public_repo", m.onPublic)
	ops.Register(core.Op{Name: "status", Help: `{} -> projects, work in flight, scores, what waits on the person, the live settings (models, autonomy, schedules)`, Fn: func(r core.Run, _ json.RawMessage) (any, error) { return m.Status(r), nil }})
	return m
}

// Spec describes a project to create.
type Spec struct {
	Name        string `json:"name"`
	Repo        string `json:"repo"`
	Description string `json:"description"`
	Goals       string `json:"goals"`
	Sharing     string `json:"sharing"`    // shared, private or isolated (docs/sharing.md)
	Visibility  string `json:"visibility"` // the repository's, if the Hive makes it: private ("") or public
	Isolated    bool   `json:"isolated"`   // the older form of "sharing": "isolated"
	Demo        bool   `json:"demo"`
	Self        bool   `json:"self"`
	// Steward is the person's word that this Hive becomes the steward of
	// release_repo when no Hive is recorded there yet (core.StewardFile).
	Steward bool `json:"steward"`
}

func (m *Module) opCreate(r core.Run, args json.RawMessage) (any, error) {
	var s Spec
	if err := core.Bind(args, &s); err != nil {
		return nil, err
	}
	// A new project's name, description and goals land in the shared record
	// and on the forge, so a hidden project's lead could carry its text out
	// through them. Only the person, or a lead outside every wall, makes one.
	// A project the Hive doesn't know (removed, or not yet saved) can't show
	// it has no wall, so its agents can't either.
	if r.Project != "" {
		p, ok := m.C.Projects.Get(r.Project)
		if !ok {
			return nil, fmt.Errorf("%s isn't a project the Hive knows, so its agents can't create projects; ask the person", r.Project)
		}
		if p.Hidden() {
			return nil, fmt.Errorf("%s is walled off, so its agents can't create projects; ask the person", r.Project)
		}
	}
	if s.Self {
		s.Repo = "" // an agent never picks where the Hive's own code lives
	}
	s.Steward = false // nor which Hive is the steward of the Hive's own repository
	if err := checkVisibility(s.Visibility); err != nil {
		return nil, err
	}
	// A public repository is outward in the person's name: only where they
	// said public repositories go is one made without their yes.
	if s.Visibility == "public" && !m.publicAllowed(s.Repo) {
		ref, _ := json.Marshal(s)
		a := m.C.Person.Ask(core.Ask{Kind: "public_repo", Ref: string(ref), From: r.ID, Project: r.Project,
			Title:  "Make a public repository for " + orDefault(s.Repo, s.Name) + "?",
			Detail: "An agent asked create_project for a public repository outside " + strings.Join(m.C.WS.LoadConfig().PublicOwners, ", ") + ". Yes creates the project with it public; no leaves it uncreated. Description: " + core.Clip(s.Description, 300)})
		return map[string]any{"waiting_on_person": a.ID, "text": "a public repository outside public_owners is the person's call: asked them; the project is made if they say yes, and you'll get a message"}, nil
	}
	p, created, err := m.Create(s)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"project": p.Name, "repo": p.Repo, "new_repository": created, "visibility": orDefault(p.Visibility, "as it was (adopted)"), "chat": "project:" + p.Name}
	if s.Visibility == "public" && created {
		m.C.Person.Tell(core.Ask{Title: "Made a public repository: " + p.Repo, Ref: p.Repo, From: r.ID, Project: p.Name,
			Detail: "create_project made " + p.Repo + " public, in an owner listed in public_owners. " + core.Clip(p.Description, 300)})
	} else if s.Visibility == "public" && !created {
		out["note"] = "the repository already existed, so it keeps its visibility: changing an existing repository's visibility is the person's"
	}
	return out, nil
}

func checkVisibility(v string) error {
	if v != "" && v != "private" && v != "public" {
		return fmt.Errorf("visibility is private or public, not %q", v)
	}
	return nil
}

// publicAllowed says whether repo's owner is one the person said public
// repositories may go in. A bare name lives in the person's own account,
// which isn't one unless they list it.
func (m *Module) publicAllowed(repo string) bool {
	owner, _, ok := strings.Cut(repo, "/")
	if !ok {
		return false
	}
	return slices.ContainsFunc(m.C.WS.LoadConfig().PublicOwners, func(o string) bool { return strings.EqualFold(o, owner) })
}

// onPublic makes the project a public_repo ask was about, once the person says yes.
func (m *Module) onPublic(a core.Ask) {
	var s Spec
	if json.Unmarshal([]byte(a.Ref), &s) != nil {
		return
	}
	if a.Answer != "yes" {
		if a.From != "" {
			m.C.Runs.Send(a.From, "The person said no to a public repository for "+orDefault(s.Repo, s.Name)+"; nothing was made.")
		}
		return
	}
	p, created, err := m.Create(s)
	if a.From == "" {
		return
	}
	switch {
	case err != nil:
		m.C.Runs.Send(a.From, "The person said yes to a public repository for "+orDefault(s.Repo, s.Name)+", but making the project failed: "+err.Error())
	case !created:
		m.C.Runs.Send(a.From, "The person said yes; "+p.Repo+" already existed, so it keeps its visibility. Project "+p.Name+" is set up.")
	default:
		m.C.Runs.Send(a.From, "The person said yes: "+p.Repo+" is made public and project "+p.Name+" is set up (chat project:"+p.Name+").")
	}
}

// Create makes (or adopts) the repository, clones it, sets it up, and opens
// its chat. An empty repository gets a first commit with the Hive's files; an
// existing one gets a setup task whose pull request the person merges. Setup
// that fails part way is finished later by Tick, never started over.
func (m *Module) Create(s Spec) (core.Project, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var level core.Project
	if s.Isolated && s.Sharing == "" {
		s.Sharing = core.SharingIsolated
	}
	if err := level.SetSharing(s.Sharing); err != nil {
		return core.Project{}, false, err
	}
	if err := checkVisibility(s.Visibility); err != nil {
		return core.Project{}, false, err
	}
	public := s.Visibility == "public"
	f := m.C.Effects.Forge
	if s.Demo && s.Name == "" {
		s.Name = "textkit"
	}
	// The Hive's own project works in a fork, made on first need: the
	// person's own, or the one in the owner they named as self_fork. It works
	// in the Hive's repository itself only when that is theirs and this Hive
	// is its steward, so two Hives of one person never merge and release there.
	cfg := m.C.WS.LoadConfig()
	if up := cfg.ReleaseRepo; s.Self && (s.Repo == "" || s.Repo == up) {
		user, err := f.User()
		if err != nil {
			return core.Project{}, false, fmt.Errorf("who are you on %s? %v", f.Name(), err)
		}
		owner := ""
		switch {
		case slices.ContainsFunc(m.C.Projects.List(), func(p core.Project) bool { return p.Self && p.Repo == up }):
			s.Repo = up // already working there, as H0 did before the record; merges and releases still check it
		case s.Repo == "" && cfg.SelfFork != "":
			owner = cfg.SelfFork
		case s.Repo == "" && !strings.HasPrefix(up, user+"/"):
		default:
			if err := m.mayWorkIn(up, s.Steward); err != nil {
				return core.Project{}, false, err
			}
			s.Repo = up
		}
		if s.Repo == "" {
			if s.Repo, err = f.Fork(up, owner); err != nil {
				return core.Project{}, false, fmt.Errorf("forking %s: %v", up, err)
			}
		}
		s.Name = orDefault(s.Name, "hive")
	}
	if s.Repo == "" && s.Name == "" {
		return core.Project{}, false, errors.New("give a name or a repository")
	}
	if s.Repo == "" || !strings.Contains(s.Repo, "/") {
		user, err := f.User()
		if err != nil {
			return core.Project{}, false, fmt.Errorf("who are you on %s? %v", f.Name(), err)
		}
		if s.Repo == "" {
			s.Repo = user + "/" + s.Name
		} else {
			s.Repo = user + "/" + s.Repo
		}
	}
	for _, p := range m.C.Projects.List() {
		if p.Repo == s.Repo || (s.Demo && p.Demo) {
			p, err := m.finish(p)
			return p, false, err
		}
	}
	created, err := f.EnsureRepo(s.Repo, core.Clip(s.Description, 300), public)
	// The demo never adopts a repository with history: take the next free name.
	for i := 2; s.Demo && err == nil && !created && i < 10; i++ {
		if empty, _ := f.IsEmpty(s.Repo); empty {
			break
		}
		owner := strings.SplitN(s.Repo, "/", 2)[0]
		s.Name = fmt.Sprintf("textkit-%d", i)
		s.Repo = owner + "/" + s.Name
		created, err = f.EnsureRepo(s.Repo, core.Clip(s.Description, 300), public)
	}
	if err != nil {
		return core.Project{}, false, err
	}
	p := core.Project{Name: m.C.Projects.UniqueName(s.Name, s.Repo), Repo: s.Repo, Branch: f.DefaultBranch(s.Repo),
		Description: s.Description, Isolated: level.Isolated, Private: level.Private, Self: s.Self, Demo: s.Demo, Goals: s.Goals}
	if created {
		p.Branch, p.Visibility = "main", orDefault(s.Visibility, "private")
	}
	if p, err = m.C.Projects.Add(p); err != nil {
		return p, created, err
	}
	p, err = m.finish(p)
	return p, created, err
}

// mayWorkIn says why this Hive may not work directly in up, the release
// repository: its record names another steward, or names none and the
// person hasn't made this Hive the steward.
func (m *Module) mayWorkIn(up string, word bool) error {
	raw, err := m.C.Effects.Forge.File(up, core.StewardFile)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return fmt.Errorf("can't read %s's steward record (%s), so this Hive won't work in it directly: %v", up, core.StewardFile, err)
	}
	steward, id := core.ParseSteward(raw), m.C.WS.HiveID()
	if why := core.StewardRefusal(up, steward, id); why != "" {
		return errors.New(why)
	}
	if steward == "" && !word {
		return fmt.Errorf("%s records no steward yet (%s), and another of your Hives may already work in it, so this one won't: "+
			"it works in a fork once you name its owner as self_fork in the settings (e.g. Hive-RSI-Lab), or becomes the steward by your word "+
			"(%s naming %s in %s)", up, core.StewardFile, core.StewardFile, id, up)
	}
	return nil
}

// claim files the change that records this Hive as the steward of the
// release repository its own project works in, once, when no Hive is
// recorded there yet. The file is protected, so the person merges it.
func (m *Module) claim(p core.Project) {
	up := m.C.WS.LoadConfig().ReleaseRepo
	if !p.Self || !p.Ready || p.Repo != up || m.claimed[p.Name] {
		return
	}
	m.claimed[p.Name] = true
	if core.ParseSteward(m.C.Effects.ReadFile(p, "refs/remotes/origin/"+p.Branch, core.StewardFile)) != "" {
		return
	}
	id := m.C.WS.HiveID()
	title := "Record this Hive (" + id + ") as the steward of " + up
	if len(m.Work.List(func(t work.Task) bool { return t.Project == p.Name && t.Title == title })) > 0 {
		return
	}
	m.Work.File(work.Brief{Project: p.Name, From: "hive", Title: title,
		Done: "`" + core.StewardFile + "` on main reads exactly:\n\n```\n" + core.StewardRecord(id) + "```",
		Known: "Only one Hive merges and releases in " + up + ": the steward this file names. Until it exists, another Hive of the person's could work there too. " +
			"The file is protected, so the person merges the change: that is their word that this Hive is the steward."})
}

// finish clones and sets up a registered project; safe to call again.
func (m *Module) finish(p core.Project) (core.Project, error) {
	if p.Ready {
		return p, nil
	}
	main, err := m.C.Effects.Sync(p)
	if err != nil {
		return p, err
	}
	// Records from before the Demo flag existed: the demo said so in its description.
	demoProject := p.Demo || (strings.HasPrefix(p.Name, "textkit") && strings.Contains(p.Description, "the demo"))
	goals := strings.TrimSpace(p.Goals)
	if main == "" {
		files := map[string]string{}
		if demoProject {
			files = demo.Files()
		} else {
			files["README.md"] = "# " + p.Name + "\n\n" + p.Description + "\n"
			files[".hive/goals.md"] = "# Goals\n\n" + orDefault(goals, "(Not set yet. Tell the lead what this project is for.)") + "\n"
			files[".hive/lessons.md"] = "# Lessons\n\nWhat was tried and why it didn't stay. Read before working here.\n\n"
			files[".hive/project.json"] = "{\n  \"test\": \"\"\n}\n"
		}
		sha, err := m.C.Effects.Setup(p, "", files, "Set up the project for the Hive")
		if err != nil {
			return p, err
		}
		if err := m.C.Effects.PushInitial(p, sha); err != nil {
			return p, err
		}
		if _, err := m.C.Effects.Sync(p); err != nil {
			return p, err
		}
	} else if m.C.Effects.ReadFile(p, main, ".hive/goals.md") == "" {
		title := "Set up the Hive files for " + p.Name
		if len(m.Work.List(func(t work.Task) bool { return t.Project == p.Name && t.Title == title })) == 0 {
			m.Work.File(work.Brief{Project: p.Name, From: "hive", Title: title,
				Done:  "`.hive/goals.md` holds the person's goals in their own words; `.hive/project.json` names the test command and, if one fits, a score (command, direction, tolerance, and in `paths` every file the score reads: scripts it calls, tests, data; the command prints its number as the last word of its output or alone on exactly one line); `.hive/lessons.md` exists.",
				Known: "The person's words about this project: " + orDefault(goals, p.Description)})
		}
	}
	p.Ready, p.Demo = true, demoProject
	m.C.Projects.Update(p)
	m.Chat.EnsureProject(p.Name)
	m.C.J.MustAppend("project.ready", map[string]string{"project": p.Name, "repo": p.Repo})
	return p, nil
}

// Tick finishes the setup of projects that didn't complete (a failed clone,
// the network down), retrying every minute and raising one alert per error.
func (m *Module) Tick() {
	for _, p := range m.C.Projects.List() {
		m.claim(p)
		if p.Ready || time.Now().Before(m.retry[p.Name]) || !m.mu.TryLock() {
			continue
		}
		m.retry[p.Name] = time.Now().Add(time.Minute)
		go func(p core.Project) {
			defer m.mu.Unlock()
			if _, err := m.finish(p); err != nil && err.Error() != m.lastErr[p.Name] {
				m.lastErr[p.Name] = err.Error()
				m.C.J.MustAppend("alert.raised", map[string]string{"kind": "project", "project": p.Name, "text": "setting up " + p.Name + " (retrying every minute): " + core.Clip(err.Error(), 300)})
			}
		}(p)
		return
	}
}

func orDefault(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}

// ProjectStatus is one project's summary.
type ProjectStatus struct {
	Name       string   `json:"name"`
	Repo       string   `json:"repo"`
	Visibility string   `json:"visibility,omitempty"` // public or private, when the Hive made the repository
	Best       *float64 `json:"best_score,omitempty"`
	Hold       string   `json:"merge_hold,omitempty"` // the person's hold on its merges
	InFlight   []string `json:"in_flight"`
	Recent     []string `json:"recent"`
}

// Status summarizes the Hive for agent r, leaving out the projects it may
// not reach (an isolated one unless r is its own, and every other when r's
// is isolated: seeing only its own project is what isolation means).
func (m *Module) Status(r core.Run) map[string]any {
	var ps []ProjectStatus
	for _, p := range m.C.Projects.List() {
		if m.C.Runs.Walled(r, p.Name) != nil {
			continue
		}
		st := ProjectStatus{Name: p.Name, Repo: p.Repo, Visibility: p.Visibility}
		if b, ok := m.Improve.Best(p.Name); ok {
			st.Best = &b
		}
		if h, ok := m.Work.Hold(p.Name); ok {
			st.Hold = h.Text()
		}
		for i, t := range m.Work.List(func(t work.Task) bool { return t.Project == p.Name }) {
			line := fmt.Sprintf("[%s] %s (%s)", t.Stage, t.Label(), t.ID)
			if t.Fix() {
				line += " · a fix"
			} else {
				line += " · taken as a gain"
			}
			if t.Queued() && t.Reason != "" {
				line += ": " + t.Reason
			} else if t.PR.URL != "" {
				line += " " + t.PR.URL
			} else if t.Stage == work.StageBlocked && t.Branch != "" {
				line += " branch " + t.Branch
			}
			if !t.Terminal() {
				st.InFlight = append(st.InFlight, line)
			} else if i < 8 || t.Stage != work.StageDone { // blocked and stopped work stays findable
				st.Recent = append(st.Recent, line)
			}
		}
		ps = append(ps, st)
	}
	var waiting []string
	for _, a := range m.C.Person.Open() {
		// A project-less ask (the spend notice) is the person's, and every run sees it.
		if a.Project != "" && m.C.Runs.Walled(r, a.Project) != nil {
			continue
		}
		waiting = append(waiting, fmt.Sprintf("%s (from %s; the person answers in Waiting, or %s approve|refuse %s)", a.Title, a.From, core.Cmd, a.ID))
	}
	cfg := m.C.WS.LoadConfig()
	running, waitingChecks := m.C.Effects.Slots.State()
	pace := m.Work.Pace()
	return map[string]any{"projects": ps, "waiting_on_person": waiting, "config": View(cfg),
		"queue":   m.Work.Queue(r), // briefs waiting for a worker, in the order they start; the queue op reorders or holds them
		"checks":  fmt.Sprintf("%d running, %d waiting (at once: %s; max_checks %d, 0 is no ceiling)", len(running), len(waitingChecks), pace["checks"], cfg.MaxChecks),
		"workers": fmt.Sprintf("at once: %s; max_workers %d, 0 is no ceiling", pace["workers"], cfg.MaxWorkers),
		"gates":   fmt.Sprintf("recursive gates at once: %s; max_gates %d, 0 is one per 6 cores", pace["gates"], cfg.MaxGates)}
}

// ConfigView is the part of the config every agent may read, so it can say
// how the Hive is set; everything else (the forge, the floor, egress, other
// projects' settings) stays hidden, including any field added later.
type ConfigView struct {
	Autonomy       int                 `json:"autonomy"`
	Harness        string              `json:"harness"`
	Model          string              `json:"model"`
	LeadModel      string              `json:"lead_model"`
	ReviewerModel  string              `json:"reviewer_model"`
	TribunalModels []core.TribunalSeat `json:"tribunal_models"`
	ModelChoices   []string            `json:"model_choices"`
	Schedules      map[string]string   `json:"schedules"`
	ReviewRounds   int                 `json:"review_rounds"`
	PlateauMerges  int                 `json:"plateau_merges"`
	MaxWorkers     int                 `json:"max_workers"`
	MaxChecks      int                 `json:"max_checks"`
	AutoUpdate     bool                `json:"auto_update"`
	Routes         map[string][]string `json:"routes,omitempty"` // each role's models in fallback order (the models op says more)
	OpenModels     []string            `json:"open_models,omitempty"`
}

// View is c as agents see it.
func View(c core.Config) ConfigView {
	return ConfigView{c.Autonomy, c.Harness, c.Model, c.LeadModel, c.ReviewerModel, c.TribunalModels,
		append(harness.Models(c.Harness), c.ModelChoices...), c.Schedules, c.ReviewRounds, c.PlateauMerges, c.MaxWorkers, c.MaxChecks, c.AutoUpdate, c.Routes, slices.Sorted(maps.Keys(c.Models))}
}
