package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ihsan-sa/hive-standalone-build/internal/api"
	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// A command sent to the daemon's /api/ui moves a following control center:
// it switches view, moves the selection, answers the selected Waiting item,
// says who did it on the hint line and reports back where it is. Commands
// sent before it followed are not acted on.
func TestControlCenterFollowsUICommands(t *testing.T) {
	var bus api.UIBus
	var answered map[string]string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/ui", func(w http.ResponseWriter, r *http.Request) {
		after, err := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		if err != nil {
			after = -1
		}
		json.NewEncoder(w).Encode(bus.Since(r.Context(), after, 0))
	})
	mux.HandleFunc("/api/ui/view", func(w http.ResponseWriter, r *http.Request) {
		var v api.UIView
		json.NewDecoder(r.Body).Decode(&v)
		bus.SetView(v)
		w.Write([]byte(`"ok"`))
	})
	mux.HandleFunc("/api/answer", func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&answered)
		w.Write([]byte(`"ok"`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	ws := core.Workspace{Root: t.TempDir()}
	os.MkdirAll(ws.Private(), 0o700)
	raw, _ := json.Marshal(api.DaemonInfo{Port: port, Token: "t"})
	os.WriteFile(ws.DaemonFile(), raw, 0o600)
	c, err := api.Dial(ws)
	if err != nil {
		t.Fatal(err)
	}

	m := testModel(120, 40)
	m.c = c
	m.st.Asks = append(m.st.Asks, core.Ask{ID: "a2", Title: "Run it?", Options: []string{"run", "decline"}, Asked: time.Now()})
	send := func(cmd api.UICommand) {
		t.Helper()
		if _, err := bus.Send(cmd); err != nil {
			t.Fatal(err)
		}
	}
	follow := func() {
		t.Helper()
		m.Update(m.followUI(m.uiSeq, 0)())
	}
	send(api.UICommand{Action: "view", View: "settings"}) // before the control center followed
	follow()
	if m.section != secChats || m.uiSeq != 1 {
		t.Fatalf("a command from before it followed was acted on: section %d, seq %d", m.section, m.uiSeq)
	}

	send(api.UICommand{Action: "view", View: "waiting", Client: "deck", Control: "pad 3"})
	send(api.UICommand{Action: "down"})
	follow()
	if m.section != secWaiting || m.focus != 1 || m.waitSel != 1 {
		t.Fatalf("after view waiting and down: section %d focus %d waitSel %d", m.section, m.focus, m.waitSel)
	}
	if h := plain(m.hintLine()); h != " ⌁ a client: down" {
		t.Fatalf("hint line %q", h)
	}
	m.uiSent = nil
	m.reportView()()
	v := bus.Since(t.Context(), -1, 0).View
	if v == nil || v.View != "waiting" || v.Item != "a2" || v.Sel != 1 || v.Rows != 2 || v.Applied != 3 || len(v.Options) != 2 {
		t.Fatalf("reported view %+v", v)
	}

	send(api.UICommand{Action: "answer", Answer: "Decline", Client: "deck"})
	_, cmd := m.Update(m.followUI(m.uiSeq, 0)())
	runAll(cmd, 2*time.Second)
	if answered["id"] != "a2" || answered["answer"] != "decline" {
		t.Fatalf("answer sent %v", answered)
	}

	send(api.UICommand{Action: "view", View: "agents"})
	follow()
	if m.section != secDash || !m.dashTree {
		t.Fatalf("view agents: section %d tree %v", m.section, m.dashTree)
	}
	send(api.UICommand{Action: "back"})
	send(api.UICommand{Action: "up", N: 2})
	follow()
	if m.focus != 0 || m.sideKey != "sec:Chats" || m.section != secChats {
		t.Fatalf("back then up twice: focus %d side %q section %d", m.focus, m.sideKey, m.section)
	}
	if _, err := bus.Send(api.UICommand{Action: "quit"}); err == nil {
		t.Fatal("an unknown action was taken")
	}
}

// runAll runs a command and those it batches, leaving any that waits past
// limit (the follow loop's next long poll).
func runAll(cmd tea.Cmd, limit time.Duration) {
	if cmd == nil {
		return
	}
	got := make(chan tea.Msg, 1)
	go func() { got <- cmd() }()
	select {
	case msg := <-got:
		if b, ok := msg.(tea.BatchMsg); ok {
			for _, c := range b {
				runAll(c, limit)
			}
		}
	case <-time.After(limit):
	}
}
