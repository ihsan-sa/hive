package report

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/report/contracttest"
)

// bundles is a stand-in for the bundle endpoint, keeping each batch's files.
type bundles struct {
	mu   sync.Mutex
	got  []map[string]string
	size []int
	URL  string
}

func newBundles(t *testing.T) *bundles {
	b := &bundles{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
		if err != nil || r.Header.Get("Content-Type") != "application/zip" || r.Header.Get("X-Hive-Install") == "" {
			w.WriteHeader(400)
			return
		}
		files := map[string]string{}
		for _, f := range zr.File {
			rc, _ := f.Open()
			body, _ := io.ReadAll(rc)
			rc.Close()
			files[f.Name] = string(body)
		}
		b.mu.Lock()
		b.got, b.size = append(b.got, files), append(b.size, len(raw))
		b.mu.Unlock()
		w.WriteHeader(202)
	}))
	t.Cleanup(srv.Close)
	b.URL = srv.URL
	return b
}

func (b *bundles) batches() []map[string]string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]map[string]string(nil), b.got...)
}

func write(t *testing.T, ws core.Workspace, rel, text string) {
	t.Helper()
	path := filepath.Join(ws.Hive(), filepath.FromSlash(rel))
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendTo(t *testing.T, ws core.Workspace, rel, text string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(ws.Hive(), filepath.FromSlash(rel)), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(text)
	f.Close()
}

// The levels: off, failures (on), everything; what a level adds starts when
// it is chosen, and a lower level ends it.
func TestLevels(t *testing.T) {
	for in, want := range map[string]string{"off": Off, "no": Off, "on": Failures, "yes": Failures, "failures": Failures, "everything": Everything, "ALL": Everything} {
		if got, err := ParseLevel(in); err != nil || got != want {
			t.Errorf("ParseLevel(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ParseLevel("some"); err == nil {
		t.Error("an unknown level was taken")
	}
	var cfg core.Config
	if Level(cfg) != Off {
		t.Fatal("no answer isn't off")
	}
	SetLevel(&cfg, Everything)
	all := cfg.ShareAll
	if Level(cfg) != Everything || cfg.InstallID == "" || all == "" {
		t.Fatalf("everything: %+v", cfg)
	}
	SetLevel(&cfg, Everything) // again: the time it was chosen stands
	if cfg.ShareAll != all {
		t.Fatal("choosing everything again moved its start")
	}
	SetLevel(&cfg, Failures)
	if Level(cfg) != Failures || cfg.ShareAll != "" {
		t.Fatalf("failures: %+v", cfg)
	}
	SetLevel(&cfg, Off)
	if Level(cfg) != Off || cfg.Share != "off" {
		t.Fatalf("off: %+v", cfg)
	}
	// Today's configs: a time in share is failures.
	if Level(core.Config{Share: time.Now().UTC().Format(time.RFC3339Nano)}) != Failures {
		t.Fatal("an old yes isn't failures")
	}
	if !strings.Contains(WhatIsSent(Everything), saysFailures) || strings.Contains(WhatIsSent(Failures), saysEverything) || WhatIsSent(Off) != saysOff {
		t.Fatal("WhatIsSent says the wrong level")
	}
	for _, l := range Levels {
		if !strings.Contains(Question, l+" sends ") {
			t.Errorf("the question doesn't say what %s sends", l)
		}
	}
}

// The everything level sends, beside the reports, exactly what the question
// names (the brief, notes and turn logs of each run, the chats, the journal
// and the daemon log), scrubbed, from when it was chosen, in batches no
// larger than the question says; a lower level stops them.
func TestEverythingSaysWhatIsSent(t *testing.T) {
	if !strings.Contains(saysEverything, fmt.Sprintf("in batches of up to %d MB", batchCap>>20)) {
		t.Fatalf("the question doesn't give the batch cap of %d MB", batchCap>>20)
	}
	for _, s := range everythingSet {
		if !strings.Contains(saysEverything, s.says) {
			t.Errorf("the question doesn't name %s (%q)", s.glob, s.says)
		}
	}
	e, b := contracttest.New(t), newBundles(t)
	c := open(t, func(cfg *core.Config) { cfg.ShareURL, cfg.ShareBundleURL = e.URL, b.URL })
	ws := c.WS
	// Before everything is chosen: none of this goes.
	write(t, ws, "runs/old/ro/brief.md", "an old brief")
	write(t, ws, "chats/lead.jsonl", `{"text":"before"}`+"\n")
	write(t, ws, "daemon.log", "before\n")
	past := time.Now().Add(-time.Hour)
	for _, f := range []string{"runs/old/ro/brief.md", "chats/lead.jsonl", "daemon.log"} {
		os.Chtimes(filepath.Join(ws.Hive(), f), past, past)
	}
	if _, err := Save(ws, Everything, ""); err != nil {
		t.Fatal(err)
	}
	// After: every kind it names, and what it never sends.
	write(t, ws, "runs/w1/ro/brief.md", "the brief, token=abcdefghijklmnop")
	write(t, ws, "runs/w1/box/notes.md", "notes from me@example.com")
	write(t, ws, "runs/w1/host/turn-001.jsonl", `{"type":"result"}`+"\n")
	write(t, ws, "runs/w1/home/.claude/creds.json", "a login")
	write(t, ws, "runs/w1/ro/system.md", "the system prompt")
	write(t, ws, "logins/claude/token", "a login")
	write(t, ws, "private/daemon.json", "the token")
	write(t, ws, "kb/note.md", "a note")
	appendTo(t, ws, "chats/lead.jsonl", `{"text":"after"}`+"\n")
	appendTo(t, ws, "daemon.log", "after\nhalf a li")
	c.J.MustAppend("task.saved", map[string]any{"id": "t1", "stage": "blocked", "reason": "stuck"})

	res, err := (&Module{C: c}).Send()
	if err != nil || len(b.batches()) != 1 || requests(e) == 0 {
		t.Fatalf("send: %q %v, %d batches, %d reports", res, err, len(b.batches()), requests(e))
	}
	got := b.batches()[0]
	var man Batch
	if err := json.Unmarshal([]byte(got["batch.json"]), &man); err != nil || man.Type != "batch" || man.Install == "" {
		t.Fatalf("manifest %q: %v", got["batch.json"], err)
	}
	seen := map[string]bool{}
	for name, body := range got {
		if name == "batch.json" {
			continue
		}
		rel, _, _ := strings.Cut(strings.TrimPrefix(name, ".hive/"), "@")
		matched := false
		for _, s := range everythingSet {
			if ok, _ := filepath.Match(s.glob, rel); ok {
				matched, seen[s.glob] = true, true
			}
		}
		if !matched {
			t.Errorf("a batch sent %s, which the question doesn't name", name)
		}
		if strings.Contains(body, "abcdefghijklmnop") || strings.Contains(body, "me@example.com") || strings.Contains(body, "before") || strings.Contains(body, "half a li") {
			t.Errorf("%s sent %q: unscrubbed, from before the choice, or a line still being written", name, body)
		}
	}
	for _, s := range everythingSet {
		if !seen[s.glob] {
			t.Errorf("nothing sent for %s, which the question names", s.glob)
		}
	}
	if b.size[0] > batchCap {
		t.Errorf("a batch of %d bytes, over the %d the question says", b.size[0], batchCap)
	}
	if got[".hive/runs/old/ro/brief.md"] != "" {
		t.Error("a brief from before the choice was sent")
	}
	if !strings.Contains(got[".hive/chats/lead.jsonl@18"], "after") {
		t.Errorf("the chat's new lines weren't sent from where it stood: %v", keys(got))
	}

	// The next batch holds only what is new.
	appendTo(t, ws, "daemon.log", "ne\n")
	(&Module{C: c}).Send()
	if n := len(b.batches()); n != 2 {
		t.Fatalf("%d batches after a new line", n)
	}
	if next := b.batches()[1]; len(next) != 2 || !strings.Contains(next[".hive/daemon.log@13"], "half a line") {
		t.Fatalf("the second batch: %v", next)
	}
	(&Module{C: c}).Send()
	if n := len(b.batches()); n != 2 {
		t.Fatalf("a batch with nothing new: %d", n)
	}

	// Lower: no more batches, though the records go on.
	if _, err := Save(ws, Failures, ""); err != nil {
		t.Fatal(err)
	}
	appendTo(t, ws, "daemon.log", "later\n")
	(&Module{C: c}).Send()
	if n := len(b.batches()); n != 2 {
		t.Fatalf("failures sent a batch")
	}
	// Chosen again: from then, not from where it stopped.
	if _, err := Save(ws, Everything, ""); err != nil {
		t.Fatal(err)
	}
	appendTo(t, ws, "daemon.log", "again\n")
	(&Module{C: c}).Send()
	if bs := b.batches(); len(bs) != 3 || strings.Contains(bs[2][".hive/daemon.log@"+fmt.Sprint(len("before\nafter\nhalf a line\nlater\n"))], "later") || !strings.Contains(fmt.Sprint(bs[2]), "again") {
		t.Fatalf("everything again: %v", bs[len(bs)-1])
	}
	// Off: no reports either, and the queue is dropped.
	Save(ws, Off, "")
	before := requests(e)
	c.J.MustAppend("task.saved", map[string]any{"id": "t2", "stage": "blocked", "reason": "stuck"})
	if res, _ := (&Module{C: c}).Send(); res != "sharing is off" || requests(e) != before || len(b.batches()) != 3 {
		t.Fatalf("off sent: %q", res)
	}
}

// A batch never passes its cap: what doesn't fit waits for the next.
func TestBatchIsBounded(t *testing.T) {
	b := newBundles(t)
	c := open(t, func(cfg *core.Config) { cfg.ShareBundleURL = b.URL })
	Save(c.WS, Everything, "")
	line := strings.Repeat("x", 1023) + "\n"
	write(t, c.WS, "daemon.log", strings.Repeat(line, (batchCap+batchCap/2)/len(line)))
	m := &Module{C: c}
	m.sendBatch(c.WS.LoadConfig())
	m.sendBatch(c.WS.LoadConfig())
	m.sendBatch(c.WS.LoadConfig())
	bs := b.batches()
	if len(bs) != 2 {
		t.Fatalf("%d batches for one and a half caps", len(bs))
	}
	total := 0
	for i, x := range bs {
		if b.size[i] > batchCap {
			t.Fatalf("batch %d is %d bytes", i, b.size[i])
		}
		for name, body := range x {
			if name != "batch.json" {
				total += len(body)
			}
		}
	}
	if want := (batchCap + batchCap/2) / len(line) * len(line); total != want {
		t.Fatalf("sent %d of %d bytes", total, want)
	}
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
