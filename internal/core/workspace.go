package core

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
	"github.com/ihsan-sa/hive-standalone-build/internal/sandbox"
)

// Workspace is the folder on disk: .hive (core state), .modules, projects.
type Workspace struct {
	Root string
}

func (w Workspace) Hive() string               { return filepath.Join(w.Root, ".hive") }
func (w Workspace) Modules() string            { return filepath.Join(w.Root, ".modules") }
func (w Workspace) Projects() string           { return filepath.Join(w.Root, "projects") }
func (w Workspace) Project(name string) string { return filepath.Join(w.Projects(), name) }
func (w Workspace) Events() string             { return filepath.Join(w.Hive(), "events.jsonl") }
func (w Workspace) Runs() string               { return filepath.Join(w.Hive(), "runs") }
func (w Workspace) Run(id string) string       { return filepath.Join(w.Runs(), id) }
func (w Workspace) Chats() string              { return filepath.Join(w.Hive(), "chats") }
func (w Workspace) KB() string                 { return filepath.Join(w.Hive(), "kb") }
func (w Workspace) ConfigPath() string         { return filepath.Join(w.Hive(), "config.json") }
func (w Workspace) DaemonFile() string         { return filepath.Join(w.Private(), "daemon.json") }
func (w Workspace) Bin() string                { return filepath.Join(w.Hive(), "bin") }
func (w Workspace) Tools() string              { return filepath.Join(w.Hive(), "tools") }
func (w Workspace) Gates() string              { return filepath.Join(w.Hive(), "gates") }

// Private holds what grants access (the daemon's token, each run's home and
// CODEX_HOME). It is covered as a whole wherever agents read .hive, so a run
// made or a file renamed in it mid-turn is covered too.
func (w Workspace) Private() string { return filepath.Join(w.Hive(), "private") }

// Covered is the folders covered wherever agents read .hive: the login pool's
// stored logins, module programs' homes and logs, and Private. No host
// interpreter's install bound into a sandbox may hold one or sit inside one.
// The paths are absolute, so a relative root still guards them.
func (w Workspace) Covered() []string {
	hive := filepath.Join(w.abs(), ".hive")
	return []string{filepath.Join(hive, "logins"), filepath.Join(hive, "modules"), filepath.Join(hive, "private")}
}

// Guarded is what no host interpreter's install bound into a sandbox may
// hold: the workspace and the folders covered in it. The covered folders are
// listed as well as the root because any of them may be a symlink out of the
// workspace, and then holding the root says nothing about them.
func (w Workspace) Guarded() []string {
	return append([]string{w.abs()}, w.Covered()...)
}

func (w Workspace) abs() string {
	if a, err := filepath.Abs(w.Root); err == nil {
		return a
	}
	return w.Root
}

// Checkouts is where agents' own checkouts of a project live.
func (w Workspace) Checkouts(project string) string {
	return filepath.Join(w.Project(project), ".checkouts")
}

// Config is the workspace's settings, edited from the control center.
type Config struct {
	// Autonomy: 1 "ask me before anything outside the project", 2 default, 3 "tell me after".
	Autonomy       int            `json:"autonomy"`
	Harness        string         `json:"harness"`
	Model          string         `json:"model"`
	LeadModel      string         `json:"lead_model"`
	ReviewerModel  string         `json:"reviewer_model"`
	TribunalModels []TribunalSeat `json:"tribunal_models"`
	// ModelChoices adds models to the harness's known list in Settings' pick-lists.
	ModelChoices []string `json:"model_choices,omitempty"`
	// Models are named models on OpenAI-compatible endpoints, which the open
	// harness runs (route.go).
	Models map[string]OpenModel `json:"models,omitempty"`
	// Routes pick the models a role runs on, in the order a run moves along
	// them at a usage limit ("lead", "worker", "reviewer", "security",
	// "warden", "tribunal", "sweep"; "fallback" for every role that isn't a
	// check). Each entry is a named model, "harness:model", or a model on the
	// workspace harness. A role with no route takes the model settings above.
	Routes map[string][]string `json:"routes,omitempty"`
	// Egress holds runs to the hosts listed; with no list a run reaches every
	// public host.
	Egress Egress `json:"egress"`
	// Floor is the person's list of names, paths and secret globs: a step whose
	// text touches one goes to the person, whatever a warden or panel says.
	Floor []string `json:"floor,omitempty"`
	// Sandbox: "auto" uses the platform sandbox and refuses agents without one;
	// "bare" is the documented, opted-in state where agents run unsandboxed.
	Sandbox      string `json:"sandbox"`
	BareAccepted string `json:"bare_accepted,omitempty"` // timestamp the warning was read
	WSLDistro    string `json:"wsl_distro,omitempty"`
	ReleaseRepo  string `json:"release_repo"`
	// SelfFork is the owner (an organization) whose fork of release_repo this
	// Hive works on itself in, when the person owns release_repo but another
	// of their Hives is its steward (core.StewardFile).
	SelfFork string `json:"self_fork,omitempty"`
	// RepoOwner is the account or organization where create_project makes a
	// repository given only a name; empty is self_fork, else the forge login.
	RepoOwner string `json:"repo_owner,omitempty"`
	// PublicOwners are the accounts or organizations where create_project
	// may make a public repository and tell the person after; a public one
	// anywhere else waits for their yes.
	PublicOwners []string `json:"public_owners"`
	AutoUpdate   bool     `json:"auto_update"`
	ReleasePin   string   `json:"release_pin,omitempty"`        // the release `hive update --release` chose; auto-update stays on it
	PinnedAuto   bool     `json:"pinned_auto_update,omitempty"` // auto_update before the pin turned it off; clearing the pin restores it
	// Channel is the releases auto-update follows: "latest"; "pre-release",
	// each pre-release once its checks for this system pass; or "train", once
	// a day the newest of those after a canary child Hive on real work keeps
	// it. Empty is the default: the train on the Hive whose own project is
	// release_repo, the latest everywhere else.
	Channel      string                     `json:"channel,omitempty"`
	APIKeyOptIn  bool                       `json:"api_key_opt_in"`
	ReviewRounds int                        `json:"review_rounds"`
	Projects     map[string]ProjectSettings `json:"projects,omitempty"`
	Schedules    map[string]string          `json:"schedules,omitempty"` // name -> "every 12h" | "daily 03:00" | "off"
	ChatMaxMB    int                        `json:"chat_max_mb"`         // the most one chat message, files and all, may be, in MB; 0 means the default

	// UsageStops are where a plan login stops short of its limit, until the
	// window resets; none runs every login to its limit.
	UsageStops UsageStops `json:"usage_stops"`

	// MaxOutputTokens caps one reply (a budget is checked only between turns); 0 leaves the harness default.
	MaxOutputTokens int `json:"max_output_tokens"`
	// MonthlyUSD: once this month's runs cost more, the person is told once. Work goes on; 0 is off.
	MonthlyUSD float64 `json:"monthly_usd,omitempty"`
	// OpenRouterUSD is what paid OpenRouter models may cost in a month
	// (UTC): a paid one, priced so its spend is counted (PaidRefused), goes
	// after the rest of a route and takes turns only while the month's spend
	// on them is under it (internal/mod/models/paid.go). 0 keeps OpenRouter
	// free-only.
	OpenRouterUSD float64 `json:"openrouter_monthly_usd,omitempty"`
	// A turn waits while available memory is under MemFloorMB, or memory or
	// CPU pressure (Linux PSI "some avg10", percent) is over its limit; 0 turns a check off.
	MemFloorMB     int     `json:"mem_floor_mb"`
	MemPressureMax float64 `json:"mem_pressure_max"`
	CPUPressureMax float64 `json:"cpu_pressure_max"`
	// MaxWorkers is the most workers that may work at once, the person's
	// ceiling: below it the work module's pace sets the limit from what the
	// machine and the plan carry (internal/mod/work/pace.go). Briefs past the
	// limit wait in the queue, in order; 0 is no ceiling.
	MaxWorkers int `json:"max_workers"`
	// LoadMax holds new workers past the pace's start while the machine's
	// 1-minute load per core is over it (Linux /proc/loadavg); 0 is off.
	LoadMax float64 `json:"load_max"`
	// MaxChecks is the most checks (the gate's tests, scores and dry runs,
	// improve's measures) that run at once, a ceiling over the pace as
	// MaxWorkers is; the rest wait in order. 0 is no ceiling.
	MaxChecks int `json:"max_checks"`
	// Ceilings says max_workers and max_checks are the person's ceilings. A
	// config without it is from before the pace (internal/mod/work/pace.go),
	// when every install saved the then defaults (4, and the checks' start):
	// reading one clears those two values, so the pace can grow there.
	Ceilings bool `json:"ceilings"`
	// MaxGates is the most recursive gate runs (self projects' gains, each
	// two child Hives and their suites) at once, a ceiling over their pace;
	// 0 is one per 6 cores (internal/mod/work/selfgate.go).
	MaxGates int `json:"max_gates,omitempty"`
	// CheckCacheMaxMB caps one check's own home (its build cache and
	// scratch): past it the check is ended as can't-run; 0 is the default
	// (8 GB). See disk.go.
	CheckCacheMaxMB int `json:"check_cache_max_mb,omitempty"`
	// PlateauMerges: a project whose best score hasn't moved in this many
	// merges gets a step-back (besides the improve.stepback schedule); 0 is off.
	PlateauMerges int `json:"plateau_merges"`
	// StallFloorSeconds: nothing counts as stalled before this long without moving,
	// however fast its stage usually is here; 0 is the default (20 minutes).
	StallFloorSeconds int `json:"stall_floor_seconds,omitempty"`

	// Share is when the person said yes to sharing failures and usage with
	// the developer (docs/report.md), "off" once they said no, empty if
	// nobody has asked yet. Anything else counts as off.
	Share     string `json:"share,omitempty"`
	ShareURL  string `json:"share_url,omitempty"`
	InstallID string `json:"install_id,omitempty"`
	// ShareBundleURL is where `hive share` sends its bundle; empty is the developer's endpoint.
	ShareBundleURL string `json:"share_bundle_url,omitempty"`
}

// Sharing says whether sharing is on and since when.
func (c Config) Sharing() (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, c.Share)
	return t, err == nil
}

// SetShare records the person's answer; turning it on makes the install id.
// Turning it on when it is already on keeps the time it was turned on, so
// events since then that no run has reported yet still are, and still makes
// an id it lacks (report.Send asks for `report on` then).
func (c *Config) SetShare(on bool) {
	if _, already := c.Sharing(); !on || !already {
		c.Share = "off"
		if on {
			c.Share = time.Now().UTC().Format(time.RFC3339Nano)
		}
	}
	if on && c.InstallID == "" {
		c.InstallID = NewUUID()
	}
}

// ProjectSettings override workspace defaults per project.
type ProjectSettings struct {
	Harness string              `json:"harness,omitempty"`
	Model   string              `json:"model,omitempty"`
	Routes  map[string][]string `json:"routes,omitempty"` // the project's own, before the workspace's
}

// TribunalSeat is one tribunal seat's harness and model. A plain string in
// config.json is a model on the workspace harness.
type TribunalSeat struct {
	Harness string `json:"harness,omitempty"`
	Model   string `json:"model"`
	bad     string // what config.json held when it wasn't a seat; Check refuses it
}

// UnmarshalJSON never fails, since a failure would stop the rest of
// config.json being read; Check refuses a seat it couldn't read.
func (t *TribunalSeat) UnmarshalJSON(b []byte) error {
	*t = TribunalSeat{}
	if json.Unmarshal(b, &t.Model) == nil {
		return nil
	}
	type seat TribunalSeat
	if json.Unmarshal(b, (*seat)(t)) != nil {
		*t = TribunalSeat{bad: string(b)}
	}
	return nil
}

// MarshalJSON keeps a seat with no harness the plain string the person wrote,
// and a seat it couldn't read exactly as written, so a save never blanks it.
func (t TribunalSeat) MarshalJSON() ([]byte, error) {
	if t.bad != "" {
		return []byte(t.bad), nil
	}
	if t.Harness == "" {
		return json.Marshal(t.Model)
	}
	type seat TribunalSeat
	return json.Marshal(seat(t))
}

// Egress is the hosts runs may reach: Default for every run, and a role's own
// list in place of it. A run's harness API hosts are added to any list. Local
// is the services on this machine the person opened to every sandboxed run,
// one loopback host:port each ("127.0.0.1:11434"); nothing local is reachable
// otherwise. Projects is the hosts outside this machine the person opened to
// one project's runs and checks, past any list: host:port each, a public name
// ("broker.example.com:8883") or a LAN device's address ("192.168.1.20:80").
type Egress struct {
	Default  []string            `json:"default,omitempty"`
	Roles    map[string][]string `json:"roles,omitempty"` // warden, tribunal, worker, lead, ...
	Local    []string            `json:"local,omitempty"`
	Projects map[string][]string `json:"projects,omitempty"`
}

// OutsideFor is the host:port granted to project's runs, canonical, skipping
// any entry Check refuses.
func (e Egress) OutsideFor(project string) []string {
	var out []string
	for _, g := range e.Projects[project] {
		if a, err := sandbox.OutsideGrant(g); err == nil && !slices.Contains(out, a) {
			out = append(out, a)
		}
	}
	return out
}

// LocalAddrs is the loopback ip:port the Local grants open, skipping any
// entry Check refuses.
func (e Egress) LocalAddrs() []string {
	var out []string
	for _, l := range e.Local {
		a, err := sandbox.LocalGrant(l)
		if err != nil {
			continue
		}
		for _, x := range a {
			if !slices.Contains(out, x) {
				out = append(out, x)
			}
		}
	}
	return out
}

// For is a copy of role's list: the role's own when its key is set, even to
// [] (then only the harness API hosts), else Default. nil means every public host.
func (e Egress) For(role string) []string {
	if l, ok := e.Roles[role]; ok {
		return append([]string{}, l...)
	}
	return append([]string(nil), e.Default...)
}

// Check refuses settings the Hive can't act on: a tribunal seat it couldn't
// read or on a harness it doesn't have, and a floor glob that can't match.
func (c Config) Check() error {
	if c.Channel != "" && c.Channel != "latest" && c.Channel != "pre-release" && c.Channel != "train" {
		return fmt.Errorf("channel %q is none of \"latest\", \"pre-release\" and \"train\"", c.Channel)
	}
	if err := c.UsageStops.check(); err != nil {
		return err
	}
	for _, f := range c.Floor {
		if _, err := path.Match(f, ""); strings.ContainsAny(f, "*?[") && err != nil {
			return fmt.Errorf("floor entry %q isn't a glob the Hive can match: %v", f, err)
		}
	}
	for _, l := range c.Egress.Local {
		if _, err := sandbox.LocalGrant(l); err != nil {
			return err
		}
	}
	for _, p := range slices.Sorted(maps.Keys(c.Egress.Projects)) {
		for _, g := range c.Egress.Projects[p] {
			if _, err := sandbox.OutsideGrant(g); err != nil {
				return fmt.Errorf("egress for project %s: %v", p, err)
			}
		}
	}
	if err := c.checkRoutes(); err != nil {
		return err
	}
	for _, name := range slices.Sorted(maps.Keys(c.Models)) {
		if err := c.PaidRefused(name); err != nil {
			return fmt.Errorf("model %s: %v", name, err)
		}
	}
	if _, ok := c.Models[c.Model]; HarnessName(c.Harness) == "open" && !ok {
		return fmt.Errorf("the open harness runs a model named in models, and %q isn't one", c.Model)
	}
	for _, t := range c.TribunalModels {
		if t.bad != "" {
			return fmt.Errorf("tribunal seat %s isn't a model name or {\"harness\": ..., \"model\": ...}", t.bad)
		}
		if t.Model == "" {
			return errors.New("a tribunal seat needs a model")
		}
		if t.Harness == "" {
			continue
		}
		if _, err := harness.Get(t.Harness); err != nil {
			return fmt.Errorf("tribunal seat %s on harness %s can't sit: %v", t.Model, t.Harness, err)
		}
		if _, ok := c.Models[t.Model]; HarnessName(t.Harness) == "open" && !ok {
			return fmt.Errorf("tribunal seat %s on the open harness: no model of that name in models", t.Model)
		}
	}
	return nil
}

// DefaultConfig is a fresh install's settings.
func DefaultConfig() Config {
	return Config{
		Autonomy:       2,
		Harness:        "claude",
		Model:          "opus",
		LeadModel:      "opus",
		ReviewerModel:  "opus",
		TribunalModels: []TribunalSeat{{Model: "opus"}, {Model: "sonnet"}, {Model: "fable"}},
		Sandbox:        "auto",
		ReleaseRepo:    DefaultReleaseRepo,
		PublicOwners:   []string{"Hive-RSI-Lab"},
		AutoUpdate:     true,
		ReviewRounds:   3,
		ChatMaxMB:      300,

		MaxOutputTokens: 16000,
		MemFloorMB:      1024,
		MemPressureMax:  10,
		CPUPressureMax:  40,
		LoadMax:         1,
		Ceilings:        true,
		PlateauMerges:   3,
		Schedules: map[string]string{
			"improve.sweep":    "every 12h",
			"improve.stepback": "every 72h",
			"kb.file":          "daily 04:00",
			"modules.update":   "every 24h",
			"hive.update":      "every 6h",
			"report.send":      "every 6h",
			"hive.clean":       "every 1h",
		},
	}
}

// DefaultReleaseRepo is where installs look for new releases; set at build time.
var DefaultReleaseRepo = "ihsan-sa/hive"

var cfgMu sync.Mutex

// LoadConfig reads config.json, filling defaults for missing fields.
func (w Workspace) LoadConfig() Config {
	c, _ := w.ReadConfig()
	return c
}

// ReadConfig is LoadConfig with what's wrong in config.json: a value it
// couldn't read (every field that did read is kept) or a setting Check refuses.
func (w Workspace) ReadConfig() (Config, error) {
	harness.UseModules(w.Modules()) // the harnesses its modules declare are among those Check knows
	cfgMu.Lock()
	defer cfgMu.Unlock()
	c := DefaultConfig()
	raw, err := os.ReadFile(w.ConfigPath())
	if err == nil {
		err = json.Unmarshal(raw, &c)
		// A config from before lead_model: a model the person chose stays the
		// lead's; the old default ("sonnet") moves the lead to opus.
		var had struct {
			LeadModel  *string          `json:"lead_model"`
			Ceilings   *bool            `json:"ceilings"`
			UsageStops *json.RawMessage `json:"usage_stops"`
		}
		if json.Unmarshal(raw, &had) == nil && had.LeadModel == nil && c.Model != "sonnet" {
			c.LeadModel = c.Model
		}
		if had.Ceilings == nil {
			if c.MaxWorkers == 4 {
				c.MaxWorkers = 0
			}
			if c.MaxChecks == DefaultMaxChecks() {
				c.MaxChecks = 0
			}
			c.Ceilings = true
		}
		if had.UsageStops == nil {
			// A config from before usage_stops keeps the stops every
			// install had then (OldUsageStops); a new one has none.
			c.UsageStops = OldUsageStops
		}
	} else if os.IsNotExist(err) {
		err = nil
	}
	if c.Schedules == nil {
		c.Schedules = DefaultConfig().Schedules
	}
	if c.ChatMaxMB <= 0 {
		c.ChatMaxMB = DefaultConfig().ChatMaxMB
	}
	if err = errors.Join(err, c.Check()); err != nil {
		err = fmt.Errorf("%s: %w", w.ConfigPath(), err)
	}
	return c, err
}

// SaveConfig writes config.json atomically.
func (w Workspace) SaveConfig(c Config) error {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	raw, _ := json.MarshalIndent(c, "", "  ")
	return WriteFileAtomic(w.ConfigPath(), raw, 0o600)
}

// WriteFileAtomic writes via a temp file and rename.
func WriteFileAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp-" + NewID()[14:] // the random part; [:6] is the date, shared by every write that day
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := RenameOver(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// RenameOver renames tmp onto path. Windows refuses to replace a file, or
// move a folder, while another handle has it open (a reader in this daemon, a
// scanner), so there it tries again for a few seconds before giving up.
func RenameOver(tmp, path string) error {
	err := os.Rename(tmp, path)
	for i := 0; err != nil && runtime.GOOS == "windows" && i < 40; i++ {
		time.Sleep(50 * time.Millisecond)
		err = os.Rename(tmp, path)
	}
	return err
}

// NewID returns a short random id, sortable by time prefix.
func NewID() string {
	var b [5]byte
	_, _ = rand.Read(b[:])
	return time.Now().UTC().Format("060102-150405") + "-" + hex.EncodeToString(b[:])
}

// NewUUID returns a random RFC 4122 v4 UUID (harness session ids need one).
func NewUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// pointerFile records where the workspace lives so `hive` works from any directory.
func pointerFile() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = filepath.Join(HomeDir(), ".config")
	}
	return filepath.Join(dir, "hive", "workspace")
}

// HomeDir returns the user's home directory.
func HomeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

// DefaultRoot is ~/hive.
func DefaultRoot() string { return filepath.Join(HomeDir(), "hive") }

// FindWorkspace returns the configured workspace: $HIVE_HOME, the pointer file, or ~/hive.
func FindWorkspace() (Workspace, bool) {
	if v := os.Getenv("HIVE_HOME"); v != "" {
		return Workspace{Root: v}, exists(filepath.Join(v, ".hive"))
	}
	if raw, err := os.ReadFile(pointerFile()); err == nil {
		root := strings.TrimSpace(string(raw))
		if root != "" {
			return Workspace{Root: root}, exists(filepath.Join(root, ".hive"))
		}
	}
	root := DefaultRoot()
	return Workspace{Root: root}, exists(filepath.Join(root, ".hive"))
}

// ShareAnswer holds the installer's answer to sharing until first run makes
// the workspace and moves it into the config.
func ShareAnswer() string { return filepath.Join(filepath.Dir(pointerFile()), "share") }

// RememberWorkspace writes the pointer file.
func RememberWorkspace(root string) error {
	return WriteFileAtomic(pointerFile(), []byte(root+"\n"), 0o644)
}

// ForgetWorkspace removes the pointer file (uninstall leaves the folder alone).
func ForgetWorkspace() { os.Remove(pointerFile()) }

// Init creates the folder layout.
func (w Workspace) Init() error {
	for _, d := range []string{w.Hive(), w.Modules(), w.Projects(), w.Runs(), w.Chats(), w.KB(), w.Bin(), w.Gates(), w.Tools()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(w.Private(), 0o700); err != nil {
		return err
	}
	if !exists(w.ConfigPath()) {
		return w.SaveConfig(DefaultConfig())
	}
	return nil
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// Exists reports whether a path exists.
func Exists(p string) bool { return exists(p) }

// ErrNotFound is returned for missing records.
var ErrNotFound = errors.New("not found")

// IsolatedKB makes and returns an isolated project's notes folder, refusing
// one that isn't a real folder, such as a link a filer planted.
func (w Workspace) IsolatedKB(project string) (string, error) {
	dir := w.KB()
	for _, d := range []string{"isolated", project} {
		dir = filepath.Join(dir, d)
		os.Mkdir(dir, 0o755)
		if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() {
			return "", fmt.Errorf("%s is not a real folder", dir)
		}
	}
	return dir, nil
}
