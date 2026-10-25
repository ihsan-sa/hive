package tui

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/kb"
	"github.com/ihsan-sa/hive-standalone-build/internal/text"
)

// ---- knowledge -------------------------------------------------------------

// kbEntry is one row of a knowledge base folder.
type kbEntry struct {
	name   string
	dir    bool
	recent *kb.Change // a recently updated entry, listed above the folders
}

// kbAbout says what the top-level folders hold, for a person who hasn't read
// the kb module.
var kbAbout = map[string]string{
	"record":   "everything the Hive did, one file per task, request, run, chat and project",
	"practice": "how a Hive works: looking first, deciding, risk, writing, briefs, handoffs",
	"hive":     "the Hive's own source and design, at the running version",
	"isolated": "notes from isolated projects",
}

// kbEntries lists a folder of the knowledge base: folders first, then files
// newest first. Dot files (the repository, the filer's ledger) are left out.
func (m *model) kbEntries() []kbEntry {
	root := filepath.Join(m.ws.KB(), filepath.FromSlash(m.kbDir))
	des, _ := os.ReadDir(root)
	var recent, dirs, files []kbEntry
	if m.kbDir == "" {
		for i := range m.recentKB() {
			c := &m.kbNew[i]
			recent = append(recent, kbEntry{name: c.Path, recent: c})
		}
	}
	mod := map[string]int64{}
	for _, de := range des {
		if strings.HasPrefix(de.Name(), ".") {
			continue
		}
		if de.IsDir() {
			dirs = append(dirs, kbEntry{name: de.Name(), dir: true})
			continue
		}
		if !de.Type().IsRegular() {
			continue
		}
		if fi, err := de.Info(); err == nil {
			mod[de.Name()] = fi.ModTime().UnixNano()
		}
		files = append(files, kbEntry{name: de.Name()})
	}
	sort.SliceStable(files, func(i, j int) bool { return mod[files[i].name] > mod[files[j].name] })
	return append(append(recent, dirs...), files...)
}

// kbRecentN is how many recently updated entries the top folder lists.
const kbRecentN = 6

// recentKB is the knowledge base's newest and latest-changed entries, read
// from its history at most every half minute.
func (m *model) recentKB() []kb.Change {
	if time.Since(m.kbNewAt) > 30*time.Second {
		m.kbNew, m.kbNewAt = kb.Recent(m.ws.KB(), kbRecentN), time.Now()
	}
	return m.kbNew
}

func (m *model) keyKB(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	es := m.kbEntries()
	m.kbSel = min(m.kbSel, max(0, len(es)-1))
	switch k.String() {
	case "up", "k":
		if m.kbSel > 0 {
			m.kbSel--
		}
	case "down", "j":
		if m.kbSel < len(es)-1 {
			m.kbSel++
		}
	case "/":
		m.kbDeep = false
		return m, m.edit("words, ids or #PR numbers to search for", "")
	case "?":
		m.kbDeep = true
		return m, m.edit("a question for a deep search (a small agent digs; minutes, cents)", "")
	case "esc", "left", "h", "backspace":
		if m.kbDir != "" {
			up := path.Base(m.kbDir)
			m.kbDir = strings.TrimPrefix(path.Dir(m.kbDir), ".")
			m.kbSel = 0
			for i, e := range m.kbEntries() {
				if e.name == up && e.recent == nil {
					m.kbSel = i
				}
			}
		}
	case "enter", "right", "l":
		if m.kbSel >= len(es) {
			return m, nil
		}
		e := es[m.kbSel]
		rel := path.Join(m.kbDir, e.name)
		if e.recent != nil {
			rel = e.recent.Path
		}
		if e.dir {
			m.kbDir, m.kbSel = rel, 0
			return m, nil
		}
		raw, err := core.SafeRead(filepath.Join(m.ws.KB(), filepath.FromSlash(rel)), 4<<20)
		if err != nil {
			m.setFlash("error: " + err.Error())
			return m, nil
		}
		return m, m.push(detail{kind: "text", id: rel, title: rel, text: shown(string(raw))})
	}
	return m, nil
}

// searchKB opens what the knowledge base says about the words, as `hive kb`
// and agents' `hive agent search` find it: the kb module's search through the
// Hive, or the built-in word match when the Hive can't answer. A deep search
// sends the module's research agent with the words as its question and shows
// its answer, with sources, once it has one.
func (m *model) searchKB(q string, deep bool) tea.Cmd {
	if q == "" {
		return nil
	}
	if deep {
		id := "deep:" + q
		cmd := m.push(detail{kind: "text", id: id, title: "deep search: " + q, text: "Digging: a small research agent reads the knowledge base and the record to answer this, within a few minutes and cents. Its answer, with sources, shows here."})
		return tea.Batch(cmd, func() tea.Msg {
			var s struct{ Run string }
			if m.c == nil {
				return kbFoundMsg{id, "The deep search needs the Hive running."}
			}
			if err := m.c.Do("POST", "/api/op", map[string]any{"op": "search", "question": q}, &s); err != nil {
				return kbFoundMsg{id, "The deep search couldn't start: " + err.Error()}
			}
			for end := time.Now().Add(20 * time.Minute); time.Now().Before(end); time.Sleep(2 * time.Second) {
				var a struct {
					Text string
					Done bool
				}
				if err := m.c.Do("GET", "/api/op?run="+s.Run, nil, &a); err == nil && a.Done {
					return kbFoundMsg{id, a.Text}
				}
			}
			return kbFoundMsg{id, "The deep search " + s.Run + " has no answer yet; its run is in Agents."}
		})
	}
	id := "search:" + q
	cmd := m.push(detail{kind: "text", id: id, title: "search: " + q, text: "searching…"})
	return tea.Batch(cmd, func() tea.Msg {
		var a struct{ Text string }
		if m.c != nil && m.c.Do("POST", "/api/op", map[string]any{"op": "search", "args": map[string]string{"query": q}}, &a) == nil && a.Text != "" {
			return kbFoundMsg{id, a.Text}
		}
		return kbFoundMsg{id, kb.Find(m.ws.KB(), q, "")}
	})
}

// kbFoundMsg is a search's answer for the detail opened as id.
type kbFoundMsg struct{ id, text string }

func (m *model) kbFound(msg kbFoundMsg) {
	for i := range m.stack {
		if d := &m.stack[i]; d.kind == "text" && d.id == msg.id {
			d.text = shown(msg.text)
		}
	}
}

// shown spells out control characters line by line, so a note can't restyle
// the terminal.
func shown(s string) string {
	ls := strings.Split(strings.ReplaceAll(s, "\r", ""), "\n")
	for i, l := range ls {
		ls[i] = visible(l)
	}
	return strings.Join(ls, "\n")
}

func (m *model) viewKB() []string {
	w := m.mainW() - 5
	var lines []string
	if m.kbDir == "" {
		lines = append(lines, wrap("The knowledge base is the Hive's memory across projects and runs: notes agents leave, and the record of everything the Hive did. Agents search it before they say what happened; so can you.", min(w, 72))...)
		lines = append(lines, "")
	}
	search := stTitle.Render("/") + " " + stDim.Render("search") + "   " + stTitle.Render("?") + " " + stDim.Render("ask (deep search)")
	if m.editing {
		mark := "/"
		if m.kbDeep {
			mark = "?"
		}
		search = stTitle.Render(mark) + " " + m.input.View()
	}
	lines = append(lines, search, "")
	es := m.kbEntries()
	if len(es) == 0 {
		lines = append(lines, stDim.Render("Nothing here yet."))
	}
	top, sel := len(lines), 0
	for i, e := range es {
		if i == 0 && e.recent != nil {
			lines = append(lines, stTitle.Render("Recently updated"))
		}
		if i > 0 && es[i-1].recent != nil && e.recent == nil {
			lines = append(lines, "")
		}
		name, about, width := e.name, "", 28
		switch {
		case e.recent != nil:
			c := e.recent
			name, about, width = text.Visible(c.Title), c.At.Local().Format("Mon Jan 2 15:04")+"  ", min(48, max(28, w/2))
		case e.dir:
			name += "/"
			if m.kbDir == "" {
				about = kbAbout[e.name]
			}
		case m.kbDir == "":
			about = "a note"
		}
		line := pad(clip(name, width-2), width) + stDim.Render(about)
		plainLine := pad(clip(name, width-2), width) + about
		if c := e.recent; c != nil { // new stands out; an edit is dim like the rest
			mark, style := "updated", stDim
			if c.New {
				mark, style = "new", stGood
			}
			line += style.Render(pad(mark, 7)) + "  " + stDim.Render(c.Path)
			plainLine += pad(mark, 7) + "  " + c.Path
		}
		if i == m.kbSel && m.focus == 1 {
			line = stSel.Render(fit(plainLine, w))
			sel = len(lines)
		}
		lines = append(lines, clip(line, w))
	}
	if m.focus != 1 || len(es) == 0 {
		sel = top
	}
	lines = append(lines, "", stDim.Render(clip("From a shell: "+core.Cmd+" kb <words>", w)))
	return indent(window(lines, sel, m.bodyHeight()), "  ")
}
