package models

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

// A project whose .hive/project.json names a "ledger" has that file kept as
// its trial ledger: the named models and the project's counted trials, as the
// models op shows them. A score that reads it can't call the models op from
// its sandbox, so the Hive writes it: when what the ledger would say changes
// (a trial's outcome recorded, a model named), a record task lands it on main
// as a host-made commit, with no approval or read in between (work's Record).

// ledgerRetry is how long after a record task stopped (a red check, a
// conflict) the same ledger is filed again.
var ledgerRetry = 30 * time.Minute

// ledgerTrial is a trial as the ledger holds it, in the order the trials
// project's score has always saved it.
type ledgerTrial struct {
	Task    string  `json:"task"`
	Model   string  `json:"model"`
	Project string  `json:"project"`
	Title   string  `json:"title"`
	Outcome string  `json:"outcome"`
	Reason  string  `json:"reason,omitempty"`
	Cost    float64 `json:"cost"`
	Minutes int     `json:"minutes"`
	Turns   int     `json:"turns"`
	Rounds  int     `json:"review_rounds"`
}

// Ledger is project's trial ledger as the file holds it.
func (m *Module) Ledger(project string) string {
	l := struct {
		Models map[string]string `json:"models"`
		Trials []ledgerTrial     `json:"trials"`
	}{Models: named(m.C.WS.LoadConfig()), Trials: []ledgerTrial{}}
	m.mu.Lock()
	for _, t := range m.trials {
		if t.Project == project && t.Hive == "" {
			l.Trials = append(l.Trials, ledgerTrial{t.Task, t.Model, t.Project, t.Title, t.Outcome, t.Reason, t.Cost, t.Minutes, t.Turns, t.Rounds})
		}
	}
	m.mu.Unlock()
	slices.SortFunc(l.Trials, func(a, b ledgerTrial) int { return strings.Compare(a.Task, b.Task) })
	b, _ := json.MarshalIndent(l, "", "  ")
	return string(b) + "\n"
}

// tickLedgers files a record task for each project whose ledger on main is
// behind what it would say now. A ledger found up to date is remembered by its
// content, so a quiet tick touches no git; a project that keeps none is looked
// at again every ledgerLook, and one that failed after ledgerRetry.
func (m *Module) tickLedgers() {
	for _, p := range m.C.Projects.List() {
		content := m.Ledger(p.Name)
		sum := fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
		m.mu.Lock()
		was := m.ledgers[p.Name]
		m.mu.Unlock()
		if !was.at.IsZero() && (was.sum == sum && was.kept || time.Since(was.at) < was.again) {
			continue
		}
		now := ledgerSeen{sum: sum, at: time.Now()}
		kept, err := m.refreshLedger(p, content)
		switch {
		case err == errLedgerWait:
			continue
		case err != nil:
			now.again = ledgerRetry
			m.C.J.MustAppend("model.ledger_failed", map[string]string{"project": p.Name, "error": err.Error()})
		case kept:
			now.kept = true
		default:
			now.again = ledgerLook
		}
		m.mu.Lock()
		m.ledgers[p.Name] = now
		m.mu.Unlock()
	}
}

// ledgerSeen is how tickLedgers last left a project's ledger: up to date at
// sum (kept), or to be looked at again after again.
type ledgerSeen struct {
	sum   string
	at    time.Time
	kept  bool
	again time.Duration
}

// ledgerLook is how often a project that keeps no ledger is looked at again,
// so one whose project.json comes to name one is written without a new trial.
var ledgerLook = 10 * time.Minute

var errLedgerWait = errors.New("a record of the ledger is in flight")

// refreshLedger brings p's ledger on main to content: kept says main holds
// it, and errLedgerWait that a record task is on its way or stopped lately.
// Neither, and no error: p keeps no ledger.
func (m *Module) refreshLedger(p core.Project, content string) (kept bool, err error) {
	// Main as the host clone last fetched it says whether p keeps one at all;
	// a clone not made yet doesn't say.
	if raw := m.C.Effects.ReadFile(p, "refs/remotes/origin/"+p.Branch, ".hive/project.json"); raw != "" && core.ParseProjectConfig(raw).Ledger == "" {
		return false, nil
	}
	cut := time.Now().Add(-ledgerRetry)
	if len(m.Work.List(func(t work.Task) bool {
		return t.Project == p.Name && t.Kind == "record" && (!t.Terminal() || t.Stage == work.StageStopped && t.Updated.After(cut))
	})) > 0 {
		return false, errLedgerWait
	}
	main, err := m.C.Effects.Sync(p)
	if err != nil || main == "" {
		return false, err
	}
	path := m.C.Effects.Config(p, main).Ledger
	if path == "" {
		return false, nil
	}
	if m.C.Effects.ReadFile(p, main, path) == strings.TrimSpace(content) { // git's output comes trimmed
		return true, nil
	}
	_, err = m.Work.Record(p.Name, "Refresh "+path+" from the Hive's record of trials",
		"It writes "+path+", the project's trial ledger (its .hive/project.json names it), as `hive agent models` shows it now: the named models and this project's trials.",
		map[string]string{path: content})
	if err != nil {
		return false, err
	}
	return false, errLedgerWait
}
