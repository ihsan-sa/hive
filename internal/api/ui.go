package api

import (
	"fmt"
	"strings"
	"time"
)

// UI commands let a client the person holds (the deck, a script) drive a
// running control center: POST /api/ui sends one, every control center
// following GET /api/ui acts on it at once, and each reports back where it
// is (POST /api/ui/view), which GET /api/ui returns so a client can light
// its pads to match. All three are the person's: a module's token is
// refused. These are the wire types; the ui module (internal/mod/ui) holds
// the commands in the daemon's memory, not the journal: they steer a screen
// and change nothing in the Hive (an answer goes through /api/answer as the
// person's own does). docs/ui-api.md documents them.

// UIActions are the commands a control center follows.
var UIActions = []string{"up", "down", "left", "right", "pageup", "pagedown", "view", "open", "back", "answer"}

// UIViews are the views "view" switches to.
var UIViews = []string{"chats", "threads", "thread", "dashboard", "agents", "waiting", "feedback", "knowledge", "modules", "settings"}

// UICommand is one command to the control center.
type UICommand struct {
	Seq     int64     `json:"seq"`
	At      time.Time `json:"at"`
	Action  string    `json:"action"`
	N       int       `json:"n,omitempty"`       // up, down, pageup, pagedown: how many steps (1 if unset)
	View    string    `json:"view,omitempty"`    // view: one of UIViews
	Answer  string    `json:"answer,omitempty"`  // answer: an option of the selected Waiting item; unset, the one picked
	Client  string    `json:"client,omitempty"`  // who sent it ("deck"), for the control center's status line
	Control string    `json:"control,omitempty"` // which of its controls ("pad 3", "knob")
}

// Check says what is wrong with a command, if anything.
func (c UICommand) Check() error {
	if !has(UIActions, c.Action) {
		return fmt.Errorf("action must be one of %s", strings.Join(UIActions, ", "))
	}
	if c.Action == "view" && !has(UIViews, c.View) {
		return fmt.Errorf("view must be one of %s", strings.Join(UIViews, ", "))
	}
	if c.N < 0 || c.N > 100 {
		return fmt.Errorf("n must be 0 to 100")
	}
	if len(c.Client) > 40 || len(c.Control) > 40 || len(c.Answer) > 200 {
		return fmt.Errorf("client and control are at most 40 bytes, answer 200")
	}
	return nil
}

// Label is what the control center's status line says of it.
func (c UICommand) Label() string {
	what := c.Action
	switch {
	case c.View != "":
		what += " " + c.View
	case c.Answer != "":
		what += " " + c.Answer
	case c.N > 1:
		what += fmt.Sprintf(" ×%d", c.N)
	}
	who := c.Client
	if who == "" {
		who = "a client"
	}
	if c.Control != "" {
		who += " " + c.Control
	}
	return who + ": " + what
}

func has(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// UIView is where a control center is: what a client lights its pads by.
type UIView struct {
	By      string    `json:"by,omitempty"`      // the control center reporting it (host and pid)
	At      time.Time `json:"at"`                // when it reported
	View    string    `json:"view"`              // one of UIViews, or "detail" with an item open on top
	Focus   string    `json:"focus"`             // sidebar, main or thread
	Chat    string    `json:"chat,omitempty"`    // the open chat
	Detail  string    `json:"detail,omitempty"`  // the open item's kind (run, task, ask, diff...)
	Sel     int       `json:"sel"`               // the selected row in the focused list, -1 for none
	Rows    int       `json:"rows"`              // rows in that list
	Item    string    `json:"item,omitempty"`    // the selected item's id
	Title   string    `json:"title,omitempty"`   // and its title
	Options []string  `json:"options,omitempty"` // a selected Waiting item's answers
	Option  int       `json:"option"`            // the answer picked among them
	Applied int64     `json:"applied"`           // the last command it acted on
}

// UIState is GET /api/ui: the commands after the asked seq, the newest seq,
// and the view last reported.
type UIState struct {
	Seq      int64       `json:"seq"`
	Commands []UICommand `json:"commands"`
	View     *UIView     `json:"view,omitempty"`
}
