package tui

import (
	"fmt"
	"slices"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ihsan-sa/hive-standalone-build/internal/mod/modules"
)

// marketMsg is what the indexes list (the daemon caches it).
type marketMsg struct {
	Modules []modules.Listing `json:"modules"`
	Errors  []string          `json:"errors"`
	at      time.Time
}

// fetchMarket reads the indexes while the Modules tab is open, once a minute.
func (m *model) fetchMarket() tea.Cmd {
	if m.section != secModules || time.Since(m.market.at) < time.Minute {
		return nil
	}
	m.market.at = time.Now() // one read in flight at a time
	return func() tea.Msg {
		var msg marketMsg
		m.c.Do("GET", "/api/module/index", nil, &msg)
		msg.at = time.Now()
		return msg
	}
}

// listable is what the indexes list that isn't installed.
func (m *model) listable() []modules.Listing {
	var out []modules.Listing
	for _, l := range m.market.Modules {
		if _, ok := m.module(l.Name); !ok && !slices.ContainsFunc(out, func(x modules.Listing) bool { return x.Name == l.Name }) {
			out = append(out, l)
		}
	}
	return out
}

func (m *model) selListing() (modules.Listing, bool) {
	l, i := m.listable(), m.modSel-len(m.st.Modules)
	if i < 0 || i >= len(l) {
		return modules.Listing{}, false
	}
	return l[i], true
}

func (m *model) marketRows(w int) []string {
	var lines []string
	for i, l := range m.listable() {
		line := fmt.Sprintf("%-18s%-22s%s", "[+] "+l.Name, l.Trust, l.Description)
		if l.Pulled != "" {
			line = fmt.Sprintf("%-18s%-22s%s", "[!] "+l.Name, "pulled", l.Pulled)
		}
		if len(m.st.Modules)+i == m.modSel && m.focus == 1 {
			lines = append(lines, stSel.Render(fit(line, w)))
			continue
		}
		lines = append(lines, stDim.Render(fit(line, w)))
	}
	return lines
}

// viewListing shows a listing: what it is, where from, its trust and the read
// behind it, and what it needs.
func (m *model) viewListing(name string, w int) []string {
	i := slices.IndexFunc(m.market.Modules, func(l modules.Listing) bool { return l.Name == name })
	if i < 0 {
		return []string{"", "  no index lists it now"}
	}
	l := m.market.Modules[i]
	lines := []string{stTitle.Render(l.Name + " · not installed")}
	lines = append(lines, wrap(l.Description, w)...)
	lines = append(lines, "")
	src := l.Repo
	if l.Path != "" {
		src += " (" + l.Path + ")"
	}
	for _, kv := range [][2]string{{"trust", l.Trust}, {"from", src}, {"commit", l.Sha}, {"needs", l.Needs.String()}, {"index", l.Index}, {"pulled", l.Pulled}} {
		if kv[1] != "" {
			lines = append(lines, stDim.Render(fmt.Sprintf("%-14s", kv[0]))+kv[1])
		}
	}
	if l.Read != "" {
		lines = append(lines, "", stDim.Render("read"))
		lines = append(lines, wrap(l.Read, w)...)
	}
	lines = append(lines, "", stDim.Render("i install: a warden checks it (you at autonomy 1), and you're told after"))
	return lines
}
