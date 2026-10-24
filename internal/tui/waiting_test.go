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
		var a map[string]string
		json.NewDecoder(r.Body).Decode(&a)
		got = append(got, a["id"])
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

// Enter answers only the ask the last draw showed, and only as the first key
// since that draw: Enters typed ahead of the screen approve nothing else,
// whatever happened to the list meanwhile.
func TestWaitingAnswersOnlyWhatWasDrawn(t *testing.T) {
	t.Run("a1 swapped for a2, two enters, no draw between", func(t *testing.T) {
		m, got := waitingOn(t, "a1")
		m.View()
		enter(m) // answers a1, which was drawn
		m.st.Asks = []core.Ask{askNamed("a2")}
		enter(m)
		enter(m)
		if len(*got) != 1 || (*got)[0] != "a1" {
			t.Fatalf("only a1, which was on screen, may be approved: %v", *got)
		}
		m.View() // a2 drawn: the next key may answer it
		enter(m)
		if len(*got) != 2 || (*got)[1] != "a2" {
			t.Fatalf("after a2 is drawn, enter answers it: %v", *got)
		}
	})
	t.Run("a1 swapped for a2 before the first enter", func(t *testing.T) {
		m, got := waitingOn(t, "a1")
		m.View()
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
			m.View()
			m.st.Asks = nil // a1 answered elsewhere
			if drawEmpty {
				m.View()
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
	t.Run("another key since the draw", func(t *testing.T) {
		m, got := waitingOn(t, "a1")
		m.View()
		m.Update(tea.KeyMsg{Type: tea.KeyRight})
		enter(m)
		if len(*got) != 0 {
			t.Fatalf("the pick changed after the draw; enter must wait for it to be drawn: %v", *got)
		}
		m.View()
		enter(m)
		if len(*got) != 1 || (*got)[0] != "a1" {
			t.Fatalf("drawn again, enter answers a1: %v", *got)
		}
	})
	t.Run("a draw of another view", func(t *testing.T) {
		m, got := waitingOn(t, "a1")
		m.View()
		m.help = true
		m.View() // the help covers the asks
		m.help = false
		enter(m)
		if len(*got) != 0 {
			t.Fatalf("the last draw showed no ask: %v", *got)
		}
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
	m.View()
	enter(m)
	clean(t, "the answered flash", ansi.Strip(m.hintLine()))
}
