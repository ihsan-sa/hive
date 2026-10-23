package core

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
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
func (w Workspace) DaemonFile() string         { return filepath.Join(w.Hive(), "daemon.json") }
func (w Workspace) Bin() string                { return filepath.Join(w.Hive(), "bin") }
func (w Workspace) Gates() string              { return filepath.Join(w.Hive(), "gates") }

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
	ReviewerModel  string         `json:"reviewer_model"`
	TribunalModels []TribunalSeat `json:"tribunal_models"`
	// Egress holds runs to the hosts listed; with no list a run reaches every
	// public host.
	Egress Egress `json:"egress"`
	// Floor is the person's list of names, paths and secret globs: a step whose
	// text touches one goes to the person, whatever a warden or panel says.
	Floor []string `json:"floor,omitempty"`
	// Sandbox: "auto" uses the platform sandbox and refuses agents without one;
	// "bare" is the documented, opted-in state where agents run unsandboxed.
	Sandbox      string                     `json:"sandbox"`
	BareAccepted string                     `json:"bare_accepted,omitempty"` // timestamp the warning was read
	WSLDistro    string                     `json:"wsl_distro,omitempty"`
	ReleaseRepo  string                     `json:"release_repo"`
	AutoUpdate   bool                       `json:"auto_update"`
	APIKeyOptIn  bool                       `json:"api_key_opt_in"`
	ReviewRounds int                        `json:"review_rounds"`
	Projects     map[string]ProjectSettings `json:"projects,omitempty"`
	Schedules    map[string]string          `json:"schedules,omitempty"` // name -> "every 12h" | "daily 03:00" | "off"

	// MaxOutputTokens caps one reply (a budget is checked only between turns); 0 leaves the harness default.
	MaxOutputTokens int `json:"max_output_tokens"`
	// MonthlyUSD: once this month's runs cost more, the person is told once. Work goes on; 0 is off.
	MonthlyUSD float64 `json:"monthly_usd,omitempty"`
	// A turn waits while available memory is under MemFloorMB, or memory or
	// CPU pressure (Linux PSI "some avg10", percent) is over its limit; 0 turns a check off.
	MemFloorMB     int     `json:"mem_floor_mb"`
	MemPressureMax float64 `json:"mem_pressure_max"`
	CPUPressureMax float64 `json:"cpu_pressure_max"`
	// PlateauMerges: a project whose best score hasn't moved in this many
	// merges gets a step-back (besides the improve.stepback schedule); 0 is off.
	PlateauMerges int `json:"plateau_merges"`
}

// ProjectSettings override workspace defaults per project.
type ProjectSettings struct {
	Harness string `json:"harness,omitempty"`
	Model   string `json:"model,omitempty"`
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
// list in place of it. A run's harness API hosts are added to any list.
type Egress struct {
	Default []string            `json:"default,omitempty"`
	Roles   map[string][]string `json:"roles,omitempty"` // warden, tribunal, worker, lead, ...
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
	for _, f := range c.Floor {
		if _, err := path.Match(f, ""); strings.ContainsAny(f, "*?[") && err != nil {
			return fmt.Errorf("floor entry %q isn't a glob the Hive can match: %v", f, err)
		}
	}
	for _, t := range c.TribunalModels {
		if t.bad != "" {
			return fmt.Errorf("tribunal seat %s isn't a model name or {\"harness\": ..., \"model\": ...}", t.bad)
		}
		if t.Harness == "" {
			continue
		}
		if _, err := harness.Get(t.Harness); err != nil {
			return fmt.Errorf("tribunal seat %s on harness %s can't sit: %v", t.Model, t.Harness, err)
		}
	}
	return nil
}

// DefaultConfig is a fresh install's settings.
func DefaultConfig() Config {
	return Config{
		Autonomy:       2,
		Harness:        "claude",
		Model:          "sonnet",
		ReviewerModel:  "opus",
		TribunalModels: []TribunalSeat{{Model: "opus"}, {Model: "sonnet"}, {Model: "fable"}},
		Sandbox:        "auto",
		ReleaseRepo:    DefaultReleaseRepo,
		AutoUpdate:     true,
		ReviewRounds:   3,

		MaxOutputTokens: 16000,
		MemFloorMB:      1024,
		MemPressureMax:  10,
		CPUPressureMax:  40,
		PlateauMerges:   3,
		Schedules: map[string]string{
			"improve.sweep":    "every 12h",
			"improve.stepback": "every 72h",
			"kb.file":          "daily 04:00",
			"modules.update":   "every 24h",
			"hive.update":      "every 6h",
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
	cfgMu.Lock()
	defer cfgMu.Unlock()
	c := DefaultConfig()
	raw, err := os.ReadFile(w.ConfigPath())
	if err == nil {
		err = json.Unmarshal(raw, &c)
	} else if os.IsNotExist(err) {
		err = nil
	}
	if c.Schedules == nil {
		c.Schedules = DefaultConfig().Schedules
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
	tmp := path + ".tmp-" + NewID()[:6]
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
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

// RememberWorkspace writes the pointer file.
func RememberWorkspace(root string) error {
	return WriteFileAtomic(pointerFile(), []byte(root+"\n"), 0o644)
}

// ForgetWorkspace removes the pointer file (uninstall leaves the folder alone).
func ForgetWorkspace() { os.Remove(pointerFile()) }

// Init creates the folder layout.
func (w Workspace) Init() error {
	for _, d := range []string{w.Hive(), w.Modules(), w.Projects(), w.Runs(), w.Chats(), w.KB(), w.Bin(), w.Gates()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
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
