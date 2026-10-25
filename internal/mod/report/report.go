// Package report sends an install's failure and usage reports to the Hive's
// developer, only when the person has said yes. It reads the journal, scrubs
// secrets on this machine, sends one usage report and one report per failure
// to the configured address and keeps a copy of every report sent. The
// contract and the client's behaviour are docs/report.md.
package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// DefaultURL receives reports unless the config names another. It is the
// developer's live receiver; hive.ihsan.cc is the website and takes no reports.
// The /v1/ in its path is the version of the report schema (docs/report.md).
const DefaultURL = "https://hive-api.ihsan.cc/v1/report"

// Caps, as docs/report.md states them.
const (
	maxPerRun  = 25 // the endpoint takes 30 an hour from one install; the job runs every 6h
	maxText    = 2000
	maxExcerpt = 4000
)

// Report is one request body, a usage report or one failure.
type Report struct {
	Install string             `json:"install"`
	Version string             `json:"version"`
	Type    string             `json:"type"` // usage | failure
	Kind    string             `json:"kind,omitempty"`
	OS      string             `json:"os,omitempty"`
	At      string             `json:"at,omitempty"`
	Message string             `json:"message,omitempty"`
	Log     string             `json:"log,omitempty"`
	Usage   map[string]float64 `json:"usage,omitempty"`
}

// Failure is one thing that went wrong, before it becomes a report.
type Failure struct {
	Kind    string    `json:"kind"` // task-blocked | task-stopped | run-error | alert | crash | doctor
	At      time.Time `json:"at"`
	Role    string    `json:"role,omitempty"`
	Text    string    `json:"text"`
	Excerpt string    `json:"excerpt,omitempty"`
}

// Module sends reports.
type Module struct{ C *core.Core }

// Dir holds the copies of what was sent, the reports waiting to go and
// doctor failures still to build.
func Dir(ws core.Workspace) string { return filepath.Join(ws.Hive(), "reports") }

func outbox(ws core.Workspace) string  { return filepath.Join(Dir(ws), "outbox.jsonl") }
func pending(ws core.Workspace) string { return filepath.Join(Dir(ws), "pending.jsonl") }

// Note keeps a failure seen outside the daemon (the doctor) for the next run.
func Note(ws core.Workspace, f Failure) {
	cfg := ws.LoadConfig()
	if _, yes := Contributing(cfg); yes || Level(cfg) != Off {
		f.At = time.Now().UTC()
		core.AppendJSONL(outbox(ws), f)
	}
}

// URL is where reports go.
func URL(cfg core.Config) string {
	if cfg.ShareURL != "" {
		return cfg.ShareURL
	}
	return DefaultURL
}

// Send is the report.send job: what the person chose to share goes (the
// reports, then at everything one batch of the records), and what they chose
// to contribute is filed upstream. It never fails the Hive: an unreachable
// address or a 429 leaves the rest for next time.
func (m *Module) Send() (string, error) {
	cfg := m.C.WS.LoadConfig()
	_, contributing := Contributing(cfg)
	if Level(cfg) == Off && !contributing {
		return "sharing is off", nil
	}
	if cfg.InstallID == "" {
		return "sharing has no install id; `" + core.Cmd + " report failures` makes one", nil
	}
	out := "sharing is off"
	if Level(cfg) != Off {
		var err error
		if out, err = m.sendReports(cfg); err != nil {
			return "", err
		}
	}
	if Level(cfg) == Everything {
		out += "; " + m.sendBatch(cfg)
	}
	if contributing {
		out += "; " + m.contribute(cfg)
	}
	return out, nil
}

// sendReports queues what the journal holds since the last run, then sends
// the queue one report at a time.
func (m *Module) sendReports(cfg core.Config) (string, error) {
	ws := m.C.WS
	fails, use, seq, read := m.Build(cfg)
	queue := Waiting(ws)
	if len(fails) > 0 || nonzero(use.Usage) {
		// A run that stopped between keeping the queue and marking the
		// journal left these reports queued, and they were just built again.
		queue = slices.DeleteFunc(queue, func(r Report) bool { return builtAgain(r, fails, use) })
		// The newest reports fit the cap, with room for the usage report,
		// which counts the failures that didn't.
		queue = append(queue, fails...)
		if over := len(queue) + 1 - maxPerRun; over > 0 {
			for _, r := range queue[:over] {
				if r.Type == "failure" {
					use.Usage["failures_dropped"]++
				}
			}
			queue = queue[over:]
		}
		if nonzero(use.Usage) {
			queue = append(queue, use)
		}
		// The queue is kept before the journal is marked, so a report is
		// built once.
		if err := save(ws, queue); err != nil {
			return "", fmt.Errorf("keeping the reports: %v", err)
		}
		beforeMark()
		// The outbox is never rewritten, only marked read up to an offset,
		// so a doctor appending to it can't lose a line to this run.
		m.C.J.MustAppend("report.queued", map[string]any{"seq": seq, "outbox": read, "reports": len(queue)})
	}
	if len(queue) == 0 {
		return "nothing new", nil
	}
	run, sent, refused := time.Now().UTC().Format("060102-150405.000000000"), 0, 0 // names sort in send order
	for i, r := range queue {
		raw, _ := json.Marshal(r)
		req, err := http.NewRequest("POST", URL(cfg), bytes.NewReader(raw))
		if err != nil {
			return "", fmt.Errorf("share_url: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "hive/"+core.Version)
		// The copy is kept before the report leaves, so nothing goes out
		// unrecorded.
		name := filepath.Join(Dir(ws), "sent", fmt.Sprintf("%s-%02d.json", run, i))
		if err := core.WriteFileAtomic(name, raw, 0o600); err != nil {
			save(ws, queue[i:])
			return "", fmt.Errorf("keeping a copy of the report: %v", err)
		}
		why := "couldn't reach " + URL(cfg)
		resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
		if err == nil {
			resp.Body.Close()
			why = fmt.Sprintf("%s answered %d", URL(cfg), resp.StatusCode)
		}
		switch {
		case err == nil && resp.StatusCode/100 == 2:
			sent++
		case err == nil && dropped(resp.StatusCode):
			refused++ // the endpoint will never take it; the copy shows what it was
		default:
			os.Remove(name)
			save(ws, queue[i:])
			return fmt.Sprintf("sent %d, refused %d; %s; %d wait for the next run", sent, refused, why, len(queue)-i), nil
		}
	}
	save(ws, nil)
	return fmt.Sprintf("sent %d, refused %d", sent, refused), nil
}

// builtAgain says a queued report is one of fails, or a usage report use
// covers: one whose events start where use's do.
func builtAgain(r Report, fails []Report, use Report) bool {
	if r.Type == "usage" {
		from, _, _ := strings.Cut(r.Message, " to ")
		start, _, _ := strings.Cut(use.Message, " to ")
		return r.Message != "" && from == start
	}
	return slices.ContainsFunc(fails, func(f Report) bool {
		return f.Kind == r.Kind && f.At == r.At && f.Message == r.Message
	})
}

// dropped says the endpoint refused a report for good: not the schema, no
// length, too large, not JSON.
func dropped(code int) bool { return code == 400 || code == 411 || code == 413 || code == 415 }

func nonzero(u map[string]float64) bool {
	for _, v := range u {
		if v != 0 {
			return true
		}
	}
	return false
}

// Waiting lists the reports queued for the next send.
func Waiting(ws core.Workspace) []Report {
	var q []Report
	core.ReadJSONL(pending(ws), func(line []byte) {
		var r Report
		if json.Unmarshal(line, &r) == nil {
			q = append(q, r)
		}
	})
	return q
}

// save replaces the queue with q.
func save(ws core.Workspace, q []Report) error {
	if len(q) == 0 {
		os.Remove(pending(ws))
		return nil
	}
	var b bytes.Buffer
	for _, r := range q {
		raw, _ := json.Marshal(r)
		b.Write(append(raw, '\n'))
	}
	return core.WriteFileAtomic(pending(ws), b.Bytes(), 0o600)
}

// beforeMark runs between queueing and marking the journal; tests note a
// failure there.
var beforeMark = func() {}

// unread returns the outbox past offset, up to its last whole line, and the
// offset that ends it. A file shorter than offset was replaced and is read
// from the start.
func unread(path string, offset int64) ([]byte, int64) {
	f, err := os.Open(path)
	if err != nil {
		return nil, offset
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || st.Size() < offset {
		offset = 0
	}
	raw, _ := io.ReadAll(io.NewSectionReader(f, offset, 1<<40))
	raw = raw[:bytes.LastIndexByte(raw, '\n')+1] // a line still being written waits
	return raw, offset + int64(len(raw))
}

var (
	notVersion = regexp.MustCompile(`[^A-Za-z0-9._+-]+`)
	notKind    = regexp.MustCompile(`[^a-z0-9_.:-]+`)
)

// clean replaces what the pattern refuses with '-' and clips to n bytes (the
// result is ASCII); empty becomes or.
func clean(s string, not *regexp.Regexp, n int, or string) string {
	s = not.ReplaceAllString(s, "-")
	if len(s) > n {
		s = s[:n]
	}
	if s == "" {
		return or
	}
	return s
}

// Version is core.Version as the contract allows it.
func Version() string { return clean(core.Version, notVersion, 32, "0") }

// failure turns f into a report; the role, if any, follows the kind after ':'.
func failure(cfg core.Config, f Failure, root string) Report {
	kind := f.Kind
	if f.Role != "" {
		kind += ":" + f.Role
	}
	return Report{Install: cfg.InstallID, Version: Version(), Type: "failure", Kind: clean(strings.ToLower(kind), notKind, 64, "unknown"),
		OS: runtime.GOOS + "/" + runtime.GOARCH, At: f.At.UTC().Format(time.RFC3339),
		Message: core.Clip(Scrub(f.Text, root), maxText), Log: clipLines(Scrub(f.Excerpt, root), maxExcerpt)}
}

// clipLines clips a log to n characters as core.Clip does, keeping its line
// breaks so the report reads as the log did: each line's spaces are squashed
// and blank lines dropped.
func clipLines(s string, n int) string {
	var lines []string
	for l := range strings.Lines(s) {
		if l = strings.Join(strings.Fields(l), " "); l != "" {
			lines = append(lines, l)
		}
	}
	s = strings.Join(lines, "\n")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// Build reads the journal since the last run that queued reports, from when
// sharing was turned on, and returns one report per failure (oldest first),
// the usage report, the last event seq it covers and the outbox offset it
// read up to.
func (m *Module) Build(cfg core.Config) ([]Report, Report, int64, int64) {
	since, _ := cfg.Sharing()
	return m.build(cfg, since, "report.queued")
}

// build is Build from since, after the last event of kind mark: the reports'
// queue and the contribute path each keep their own place.
func (m *Module) build(cfg core.Config, since time.Time, mark string) ([]Report, Report, int64, int64) {
	use := map[string]float64{"runs": 0, "turns": 0, "merges": 0, "reverts": 0, "cost_usd": 0}
	var fails []Failure
	var from, to time.Time
	var last, seq, boxAt int64
	roles := map[string]string{}
	sessions, spends := map[string]string{}, map[string]*core.SessionSpend{}
	stages := map[string]string{}
	running := false
	var events []core.Event
	m.C.J.Replay(func(e core.Event) {
		if e.Kind == mark {
			var s struct{ Seq, Outbox int64 }
			e.Decode(&s)
			last, boxAt = s.Seq, s.Outbox
		}
		events = append(events, e)
	})
	add := func(at time.Time, kind, role, text, excerpt string) {
		fails = append(fails, Failure{Kind: kind, At: at, Role: role, Text: text, Excerpt: excerpt})
	}
	for _, e := range events {
		seq = e.Seq
		fresh := e.Seq > last && !e.At.Before(since)
		if fresh && from.IsZero() {
			from = e.At
		}
		if fresh {
			to = e.At
		}
		switch e.Kind {
		case "run.created":
			var r core.RunSpec
			e.Decode(&r)
			roles[r.ID] = r.Role
			if fresh {
				use["runs"]++
			}
		case "run.turn_started":
			var t struct{ ID, Session string }
			e.Decode(&t)
			sessions[t.ID] = t.Session
		case "run.turn_ended":
			var t struct {
				ID, Status, Reason, Session string
				Result                      *struct {
					Text    string
					IsError bool    `json:"is_error"`
					Limited bool    `json:"limited"`
					Cost    float64 `json:"cost_usd"`
				}
			}
			e.Decode(&t)
			if t.Session != "" {
				sessions[t.ID] = t.Session
			}
			spent := 0.0
			if t.Result != nil {
				if spends[t.ID] == nil {
					spends[t.ID] = &core.SessionSpend{}
				}
				spent = spends[t.ID].Turn(sessions[t.ID], t.Result.Cost)
			}
			if !fresh {
				continue
			}
			use["turns"]++
			use["cost_usd"] += spent
			if t.Status == core.StatusParked && (t.Result == nil || t.Result.IsError && !t.Result.Limited) {
				text := ""
				if t.Result != nil {
					text = t.Result.Text
				}
				add(e.At, "run-error", roles[t.ID], t.Reason, text)
			}
		case "task.saved":
			var t struct {
				ID, Kind, Stage, Reason string
				Log                     []struct{ Text string }
			}
			e.Decode(&t)
			prev := stages[t.ID]
			stages[t.ID] = t.Stage
			if !fresh || prev == t.Stage {
				continue
			}
			switch t.Stage {
			case "merged":
				use["merges"]++
				if t.Kind == "revert" {
					use["reverts"]++
				}
			case "blocked", "stopped":
				var log []string
				for _, l := range t.Log[max(0, len(t.Log)-8):] {
					log = append(log, l.Text)
				}
				add(e.At, "task-"+t.Stage, "", t.Reason, strings.Join(log, "\n"))
			}
		case "alert.raised":
			var a map[string]string
			e.Decode(&a)
			if fresh {
				add(e.At, "alert", "", a["kind"]+": "+a["text"], "")
			}
		case "hive.started":
			if running && fresh {
				add(e.At, "crash", "", "the Hive stopped without shutting down", "")
			}
			running = true
		case "hive.stopped":
			running = false
		}
	}
	box, boxEnd := unread(outbox(m.C.WS), boxAt)
	for _, line := range bytes.Split(box, []byte("\n")) {
		var f Failure
		if json.Unmarshal(line, &f) == nil {
			add(f.At, f.Kind, "", f.Text, f.Excerpt)
		}
	}
	sort.SliceStable(fails, func(i, j int) bool { return fails[i].At.Before(fails[j].At) })
	reports := []Report{}
	for _, f := range fails {
		reports = append(reports, failure(cfg, f, m.C.WS.Root))
	}
	u := Report{Install: cfg.InstallID, Version: Version(), Type: "usage", OS: runtime.GOOS + "/" + runtime.GOARCH, Usage: use}
	if !to.IsZero() {
		u.At = to.UTC().Format(time.RFC3339)
		u.Message = fmt.Sprintf("harness %s; events from %s to %s", cfg.Harness, from.UTC().Format(time.RFC3339), u.At)
	}
	return reports, u, seq, boxEnd
}

// Sent lists the copies of sent reports, oldest first.
func Sent(ws core.Workspace) []string {
	files, _ := filepath.Glob(filepath.Join(Dir(ws), "sent", "*.json"))
	sort.Strings(files)
	return files
}
