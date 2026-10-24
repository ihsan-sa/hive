package tui

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ihsan-sa/hive-standalone-build/internal/api"
	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/release"
)

// fakeDaemon serves /api/state from st, holding each answer while hang is
// set, and writes daemon.json for it.
type fakeDaemon struct {
	mu   sync.Mutex
	st   api.State
	hang chan struct{}
	ws   core.Workspace
	info api.DaemonInfo
}

func newFakeDaemon(t *testing.T) (*fakeDaemon, *api.Client) {
	d := &fakeDaemon{ws: core.Workspace{Root: t.TempDir()}, st: sample()}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		hang, st := d.hang, d.st
		d.mu.Unlock()
		if hang != nil {
			<-hang
			d.mu.Lock()
			st = d.st
			d.mu.Unlock()
		}
		json.NewEncoder(w).Encode(st)
	}))
	t.Cleanup(srv.Close)
	d.info = api.DaemonInfo{PID: 1, Port: srv.Listener.Addr().(*net.TCPAddr).Port, Token: "t", Version: "v0.1.57"}
	d.write(t, false)
	c, err := api.Dial(d.ws)
	if err != nil {
		t.Fatal(err)
	}
	return d, c
}

func (d *fakeDaemon) write(t *testing.T, restarting bool) {
	info := d.info
	info.Restarting = restarting
	raw, _ := json.Marshal(info)
	os.MkdirAll(d.ws.Private(), 0o700)
	if err := os.WriteFile(filepath.Join(d.ws.Private(), "daemon.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (d *fakeDaemon) set(f func(*api.State)) {
	d.mu.Lock()
	f(&d.st)
	d.mu.Unlock()
}

// look is one tick of the control center: the state it asks for comes back
// on the returned channel, as the program would deliver it.
func look(m *model, at time.Time) chan tea.Msg {
	out := make(chan tea.Msg, 1)
	busy := !m.inflight.IsZero()
	m.Update(tickMsg(at))
	if !busy {
		go func() { out <- m.fetchState()() }()
	}
	return out
}

func status(m *model) string {
	v := plain(m.View())
	return v[strings.LastIndex(v, "\n")+1:]
}

// An update under an open control center: the status line shows each step
// with the download's progress, then while the Hive restarts it says it is
// reconnecting and why, the keys still answer, and once the new daemon
// answers it says which version it is back on.
func TestUpdateShowsUnderTheControlCenter(t *testing.T) {
	d, c := newFakeDaemon(t)
	m := newModel(c, d.ws)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	d.set(func(st *api.State) { st.Version = "v0.1.57" })
	m.Update(<-look(m, time.Now()))

	d.set(func(st *api.State) {
		st.Updating = &release.Progress{To: "v0.1.58", Step: release.StepDownloading, Done: 4 << 20, Total: 12 << 20, Started: time.Now()}
	})
	m.Update(<-look(m, time.Now()))
	if s := status(m); !strings.Contains(s, "updating to v0.1.58: downloading 4/12 MB") {
		t.Fatalf("while downloading: %q", s)
	}
	d.set(func(st *api.State) { st.Updating.Step = release.StepRestarting })
	m.Update(<-look(m, time.Now()))
	if s := status(m); !strings.Contains(s, "updating to v0.1.58: restarting") {
		t.Fatalf("restarting: %q", s)
	}

	// The old daemon hands over: daemon.json says so, and nothing answers.
	hang := make(chan struct{})
	d.set(func(*api.State) {})
	d.mu.Lock()
	d.hang = hang
	d.mu.Unlock()
	d.write(t, true)
	pending := look(m, time.Now())
	m.inflight = time.Now().Add(-5 * time.Second) // asked 5s ago, no answer yet
	look(m, time.Now())
	m.Update(m.probe()())
	if s := status(m); !strings.Contains(s, "reconnecting… (the Hive is restarting after an update to v0.1.58, 5s)") {
		t.Fatalf("during the restart: %q", s)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	if !m.help || !strings.Contains(plain(m.View()), "Keys") {
		t.Fatal("keys don't answer while the Hive is away")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})

	// The new daemon answers on the new version.
	d.set(func(st *api.State) { st.Version, st.Updating = "v0.1.58", nil })
	d.write(t, false)
	d.mu.Lock()
	d.hang = nil
	d.mu.Unlock()
	close(hang)
	m.Update(<-pending)
	m.notice = nil
	if s := status(m); !strings.Contains(s, "back on v0.1.58") || strings.Contains(s, "reconnecting") {
		t.Fatalf("after the restart: %q", s)
	}
}

// An outage that is no update says how long the Hive hasn't answered and
// why, and a clock at the status line's end moves with each screen update,
// so a still screen with a still clock is the terminal, not the Hive.
func TestOutageAndHeartbeat(t *testing.T) {
	d, c := newFakeDaemon(t)
	m := newModel(c, d.ws)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	at := time.Date(2026, 10, 24, 14, 3, 22, 0, time.Local)
	m.Update(<-look(m, at))
	if s := status(m); !strings.HasSuffix(strings.TrimRight(s, " "), "14:03:22") {
		t.Fatalf("no heartbeat: %q", s)
	}
	m.Update(tickMsg(at.Add(2 * time.Second)))
	m.inflight = time.Time{}
	m.Update(stateMsg{err: errors.New("dial tcp 127.0.0.1:1: connection refused")})
	m.down = time.Now().Add(-12 * time.Second)
	m.Update(m.probe()())
	s := status(m)
	if !strings.Contains(s, "reconnecting… (the Hive is not answering for 12s: dial tcp 127.0.0.1:1: connection refused") || !strings.Contains(s, "14:03:24") {
		t.Fatalf("an outage: %q", s)
	}
	checkWidth(t, m.View(), 120, 30, "outage")
	m.Update(<-look(m, time.Now()))
	if s := status(m); !strings.Contains(s, "answering again") {
		t.Fatalf("back from an outage: %q", s)
	}
}
