package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/ihsan-sa/hive-standalone-build/internal/api"
	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// answers points m at a daemon that records the ids of the asks answered.
func answers(t *testing.T, m *model) *[]string {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/answer" {
			var a map[string]string
			json.NewDecoder(r.Body).Decode(&a)
			got = append(got, a["id"])
		}
		w.Write([]byte("{}"))
	}))
	t.Cleanup(srv.Close)
	ws := core.Workspace{Root: t.TempDir()}
	port, _ := strconv.Atoi(srv.URL[strings.LastIndexByte(srv.URL, ':')+1:])
	raw, _ := json.Marshal(api.DaemonInfo{Port: port, Token: "t"})
	os.MkdirAll(filepath.Dir(ws.DaemonFile()), 0o700)
	os.WriteFile(ws.DaemonFile(), raw, 0o600)
	m.c, _ = api.Dial(ws)
	return &got
}

func enter(m *model) {
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil {
		m.Update(cmd())
	}
}

func waitingOn(t *testing.T, ids ...string) (*model, *[]string) {
	m := testModel(80, 24)
	m.st.Asks = nil
	for _, id := range ids {
		m.st.Asks = append(m.st.Asks, askNamed(id))
	}
	m.pick(sideRow{sec: secWaiting})
	m.focus = 1
	return m, answers(t, m)
}

func askNamed(id string) core.Ask {
	a := protectedAsk()
	a.ID, a.Title = id, "Merge "+id+"?"
	return a
}

// look draws the screen and lets the person read it: by the next key the
// selected ask has been on screen, and the last Enter was, waitSettle ago.
func look(m *model) {
	m.View()
	m.waitSince, m.waitEnter = m.waitSince.Add(-waitSettle), m.waitEnter.Add(-waitSettle)
}

// Enter answers only the ask the last draw showed selected, once it has been
// on screen long enough to read: Enters typed ahead of the screen approve
// nothing else, whatever happened to the list meanwhile.
func TestWaitingAnswersOnlyWhatWasDrawn(t *testing.T) {
	t.Run("a1 swapped for a2, two enters, no draw between", func(t *testing.T) {
		m, got := waitingOn(t, "a1")
		look(m)
		enter(m) // answers a1, which was read
		m.st.Asks = []core.Ask{askNamed("a2")}
		enter(m)
		enter(m)
		if len(*got) != 1 || (*got)[0] != "a1" {
			t.Fatalf("only a1, which was on screen, may be approved: %v", *got)
		}
		look(m)
		enter(m)
		if len(*got) != 2 || (*got)[1] != "a2" {
			t.Fatalf("after a2 is read, enter answers it: %v", *got)
		}
	})
	t.Run("a1 swapped for a2 before the first enter", func(t *testing.T) {
		m, got := waitingOn(t, "a1")
		look(m)
		m.st.Asks = []core.Ask{askNamed("a2")}
		enter(m)
		enter(m)
		if len(*got) != 0 {
			t.Fatalf("a2 was never drawn: %v", *got)
		}
	})
	for _, drawEmpty := range []bool{true, false} {
		t.Run("an empty list between, drawn "+strconv.FormatBool(drawEmpty), func(t *testing.T) {
			m, got := waitingOn(t, "a1")
			look(m)
			m.st.Asks = nil // a1 answered elsewhere
			if drawEmpty {
				look(m)
			}
			enter(m)
			m.st.Asks = []core.Ask{askNamed("a2")}
			enter(m)
			enter(m)
			if len(*got) != 0 {
				t.Fatalf("an empty list must not clear the way for a2, which was never drawn: %v", *got)
			}
		})
	}
	t.Run("drawn but not yet read", func(t *testing.T) {
		m, got := waitingOn(t, "a1")
		m.View()
		enter(m)
		if len(*got) != 0 {
			t.Fatalf("a1 came on screen just now: %v", *got)
		}
		look(m)
		enter(m)
		if len(*got) != 1 || (*got)[0] != "a1" {
			t.Fatalf("read, enter answers a1: %v", *got)
		}
	})
	t.Run("a draw of another view", func(t *testing.T) {
		m, got := waitingOn(t, "a1")
		look(m)
		m.help = true
		look(m) // the help covers the asks
		m.help = false
		enter(m)
		if len(*got) != 0 {
			t.Fatalf("the last draw showed no ask: %v", *got)
		}
	})
}

// The same sequences as bubbletea runs them: View after every message, the
// keys and the state updates that change the list alike, and buffered or
// repeated keys arriving milliseconds apart. No Enter approves an ask nobody
// had the time to read.
func TestWaitingEntersInTheRealLoop(t *testing.T) {
	at := time.Now()
	clock = func() time.Time { return at }
	t.Cleanup(func() { clock = time.Now })
	// step is one turn of bubbletea's loop: Update, then View; the commands
	// a key returns run and their messages come back the same way
	var step func(m *model, msg tea.Msg)
	step = func(m *model, msg tea.Msg) {
		at = at.Add(3 * time.Millisecond)
		_, cmd := m.Update(msg)
		m.View()
		if cmd != nil {
			if msg := cmd(); msg != nil {
				step(m, msg)
			}
		}
	}
	state := func(m *model, asks ...core.Ask) stateMsg {
		st := m.st
		st.Asks = asks
		return stateMsg{st: st}
	}
	key := tea.KeyMsg{Type: tea.KeyEnter}
	read := func() { at = at.Add(2 * time.Second) }
	start := func(t *testing.T) (*model, *[]string) {
		m, got := waitingOn(t, "a1")
		m.View()
		read()
		return m, got
	}
	want := func(t *testing.T, got *[]string, ids ...string) {
		t.Helper()
		if strings.Join(*got, " ") != strings.Join(ids, " ") {
			t.Fatalf("approved %v, want %v", *got, ids)
		}
	}
	t.Run("a1 swapped for a2, enters buffered", func(t *testing.T) {
		m, got := start(t)
		step(m, key)
		step(m, state(m, askNamed("a2")))
		for range 5 {
			step(m, key)
		}
		want(t, got, "a1")
		read()
		step(m, key)
		want(t, got, "a1", "a2")
	})
	t.Run("an empty list, enter, a2 arrives, enters buffered", func(t *testing.T) {
		m, got := start(t)
		step(m, state(m))
		step(m, key)
		step(m, state(m, askNamed("a2")))
		for range 5 {
			step(m, key)
		}
		want(t, got)
	})
	t.Run("an empty list, enter, a2 arrives and stays", func(t *testing.T) {
		m, got := start(t)
		step(m, state(m))
		step(m, key)
		step(m, state(m, askNamed("a2")))
		read()
		step(m, key) // a2 landed in an empty selection: this one says so
		want(t, got)
		if v := ansi.Strip(m.hintLine()); !strings.Contains(v, "a new ask came in") {
			t.Fatalf("enter should say a new ask came in: %s", v)
		}
		read()
		step(m, key)
		want(t, got, "a2")
	})
	t.Run("a held enter", func(t *testing.T) {
		m, got := start(t)
		for i := range 100 { // three seconds of key repeat
			at = at.Add(27 * time.Millisecond)
			step(m, key)
			if i == 5 {
				step(m, state(m, askNamed("a2"))) // a1 went; a2 slides under the cursor
			}
		}
		want(t, got, "a1")
		read() // let go, read a2
		step(m, key)
		want(t, got, "a1", "a2")
	})
	t.Run("the options change under the same id", func(t *testing.T) {
		m, got := start(t)
		a := askNamed("a1")
		a.Options = []string{"merge anyway", "leave open"}
		step(m, state(m, a))
		step(m, key)
		want(t, got)
		read()
		step(m, key)
		want(t, got, "a1")
	})
}

// Ask text an agent wrote reaches the feed, the away list and the answered
// flash spelled out, as it does in the Waiting view.
func TestAskTextIsSpelledOutEverywhere(t *testing.T) {
	const evil, shown = "ok\x1b[8m\u202eexe\u200b\u3164", `ok\x1b[8m\u202eexe\u200b\u3164`
	clean := func(t *testing.T, where, v string) {
		t.Helper()
		if !strings.Contains(v, shown) {
			t.Errorf("%s should spell the ask out as %s:\n%s", where, shown, v)
		}
		for _, r := range v {
			if r != '\n' && (unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '\u3164') {
				t.Errorf("%s: %U reached the screen", where, r)
			}
		}
	}
	a := askNamed("a1")
	a.Title, a.Options = evil, []string{evil, "no"}
	raw, _ := json.Marshal(a)
	ev := core.Event{Seq: 1, At: time.Now(), Kind: "person.asked", Data: raw}

	m := testModel(200, 40)
	m.feed = nil
	m.addEvents([]core.Event{ev})
	m.pick(sideRow{sec: secDash})
	clean(t, "the feed", strings.Join(m.viewFeed(), "\n"))

	clean(t, "the away list", strings.Join(summarize([]core.Event{ev}), "\n"))

	m, _ = waitingOn(t)
	m.st.Asks = []core.Ask{a}
	look(m)
	enter(m)
	clean(t, "the answered flash", ansi.Strip(m.hintLine()))
}
