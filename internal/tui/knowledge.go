package tui

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/kb"
)

// ---- knowledge -------------------------------------------------------------

// kbEntry is one row of a knowledge base folder.
type kbEntry struct {
	name string
	dir  bool
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
	var dirs, files []kbEntry
	mod := map[string]int64{}
	for _, de := range des {
		if strings.HasPrefix(de.Name(), ".") {
			continue
		}
		if de.IsDir() {
			dirs = append(dirs, kbEntry{de.Name(), true})
			continue
		}
		if !de.Type().IsRegular() {
			continue
		}
		if fi, err := de.Info(); err == nil {
			mod[de.Name()] = fi.ModTime().UnixNano()
		}
		files = append(files, kbEntry{de.Name(), false})
	}
	sort.SliceStable(files, func(i, j int) bool { return mod[files[i].name] > mod[files[j].name] })
	return append(dirs, files...)
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
		return m, m.edit("words to search for", "")
	case "esc", "left", "h", "backspace":
		if m.kbDir != "" {
			up := path.Base(m.kbDir)
			m.kbDir = strings.TrimPrefix(path.Dir(m.kbDir), ".")
			m.kbSel = 0
			for i, e := range m.kbEntries() {
				if e.name == up {
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
// and agents' `hive agent search` find it.
func (m *model) searchKB(q string) tea.Cmd {
	if q == "" {
		return nil
	}
	return m.push(detail{kind: "text", id: "search:" + q, title: "search: " + q, text: shown(kb.Find(m.ws.KB(), q, ""))})
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
	search := stTitle.Render("/") + " " + stDim.Render("search")
	if m.editing {
		search = stTitle.Render("/") + " " + m.input.View()
	}
	lines = append(lines, search, "")
	es := m.kbEntries()
	if len(es) == 0 {
		lines = append(lines, stDim.Render("Nothing here yet."))
	}
	top := len(lines)
	for i, e := range es {
		name, about := e.name, ""
		if e.dir {
			name += "/"
			if m.kbDir == "" {
				about = kbAbout[e.name]
			}
		} else if m.kbDir == "" {
			about = "a note"
		}
		line := pad(clip(name, 26), 28) + stDim.Render(about)
		if i == m.kbSel && m.focus == 1 {
			line = stSel.Render(fit(pad(clip(name, 26), 28)+about, w))
		}
		lines = append(lines, clip(line, w))
	}
	lines = append(lines, "", stDim.Render(clip("From a shell: "+core.Cmd+" kb <words>", w)))
	return indent(window(lines, top+m.kbSel, m.bodyHeight()), "  ")
}
