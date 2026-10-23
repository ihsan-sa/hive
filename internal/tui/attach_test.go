package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/ihsan-sa/hive-standalone-build/internal/mod/chat"
)

// TestPastedPaths: each form a terminal pastes for a dropped file reads as
// that file, and anything else stays text.
func TestPastedPaths(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a shot.png"), filepath.Join(dir, "b.go")
	u := filepath.ToSlash(a) // file:///C:/… on Windows
	if !strings.HasPrefix(u, "/") {
		u = "/" + u
	}
	os.WriteFile(a, []byte("x"), 0o644)
	os.WriteFile(b, []byte("x"), 0o644)
	cases := map[string][]string{
		a:              {a}, // bare (Windows Terminal, kitty)
		"'" + a + "' ": {a}, // single quotes (GNOME Terminal, Konsole)
		`"` + a + `"`:  {a}, // double quotes
		"file://" + strings.ReplaceAll(u, " ", "%20"): {a}, // file:// URL
		"'" + a + "' " + b: {a, b},
		"\n" + b + "\n":    {b},
		"look at " + b:     nil, // text that names a file is text
		b + " " + filepath.Join(dir, "missing.png"): nil,
		"hello": nil,
		dir:     nil, // a folder isn't attached
	}
	// One word attaches only when it reads as a path; go.mod alone is typed.
	t.Chdir(dir)
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir) // os.UserHomeDir on Windows
	cases["b.go"] = nil
	// macOS Terminal and iTerm2 escape a space; on Windows no terminal does, and
	// a backslash there is a separator, so the escaped path names no file.
	if runtime.GOOS == "windows" {
		cases[strings.ReplaceAll(a, " ", `\ `)] = nil
	} else {
		cases[strings.ReplaceAll(a, " ", `\ `)] = []string{a}
	}
	cases["./b.go"] = []string{"./b.go"}
	cases["~/b.go"] = []string{b}
	up := "../" + filepath.Base(dir) + "/b.go"
	cases[up] = []string{up}
	for in, want := range cases {
		if got := pastedPaths(in); !reflect.DeepEqual(got, want) {
			t.Errorf("pastedPaths(%q) = %q, want %q", in, got, want)
		}
	}
	for _, p := range []string{`.\a.png`, `..\a.png`, "../a.png"} {
		if !pathLike(p) {
			t.Errorf("%s doesn't read as a path", p)
		}
	}
}

// TestWindowsPaths: a drop from Windows Terminal into WSL gives a Windows
// path, which maps to /mnt/<drive> or the distro's own path.
func TestWindowsPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows paths are native there")
	}
	cases := map[string]string{
		`"C:\Users\me\My Pictures\a.png"`:          "/mnt/c/Users/me/My Pictures/a.png",
		`D:\work\b.go`:                             "/mnt/d/work/b.go",
		`\\wsl.localhost\Ubuntu\home\me\c.txt`:     "/home/me/c.txt",
		`"\\wsl$\Ubuntu-24.04\home\me\my file.md"`: "/home/me/my file.md",
	}
	for in, want := range cases {
		w := words(in)
		if len(w) != 1 || hostPath(w[0]) != want {
			t.Errorf("%s -> %q, want %q", in, w, want)
		}
	}
}

// TestDropAttaches: a pasted path becomes a chip, not text, and goes out
// with the message; a pasted image with no clipboard tool says so.
func TestDropAttaches(t *testing.T) {
	m := testModel(100, 30)
	m.section = secChats
	m.compose("", "")
	f := filepath.Join(t.TempDir(), "shot.png")
	os.WriteFile(f, []byte("\x89PNG..."), 0o644)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("'" + f + "' "), Paste: true})
	if m.input.Value() != "" || len(m.files) != 1 || m.files[0].Name != "shot.png" || string(m.files[0].Data) != "\x89PNG..." {
		t.Fatalf("input %q files %+v", m.input.Value(), m.files)
	}
	if v := ansi.Strip(strings.Join(m.viewChats(), "\n")); !strings.Contains(v, "📎 shot.png") {
		t.Fatalf("no chip:\n%s", v)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("just text"), Paste: true})
	if m.input.Value() != "just text" || len(m.files) != 1 {
		t.Fatalf("text paste: %q %d", m.input.Value(), len(m.files))
	}

	// Over chat_max_mb, alone or with what's attached, is refused before the
	// read, with a message; so is anything that isn't a file.
	m.st.Config.ChatMaxMB = 1
	dir := t.TempDir()
	big, half := filepath.Join(dir, "big.img"), filepath.Join(dir, "half.bin")
	os.WriteFile(big, make([]byte, 2<<20), 0o644)
	os.WriteFile(half, make([]byte, 600<<10), 0o644)
	drop := func(p string) {
		_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(p), Paste: true})
		if cmd != nil {
			m.Update(cmd())
		}
	}
	drop(big)
	if len(m.files) != 1 || !strings.Contains(m.flash, "big.img would make the message 3 MB; one message can be at most 1 MB; chat_max_mb") {
		t.Fatalf("over the limit: files %d flash %q", len(m.files), m.flash)
	}
	m.flash = ""
	drop(half)
	drop(half)
	if len(m.files) != 2 || !strings.Contains(m.flash, "half.bin would make the message 2 MB") {
		t.Fatalf("over the limit in total: files %d flash %q", len(m.files), m.flash)
	}
	if _, err := readAttachment(dir, 0, 1); err == nil || !strings.Contains(err.Error(), "not a file") {
		t.Fatalf("a folder: %v", err)
	}
	m.st.Config.ChatMaxMB = 0
	m.files = m.files[:1]

	// A clipboard image read for a message that was dropped stays out of the next one.
	stale := m.composeN
	m.keyInput(tea.KeyMsg{Type: tea.KeyEsc})
	m.compose("", "")
	m.Update(clipMsg{f: chat.File{Name: "pasted.png"}, compose: stale})
	if len(m.files) != 0 {
		t.Fatalf("a stale clipboard read attached: %+v", m.files)
	}
	m.Update(clipMsg{f: chat.File{Name: "pasted.png"}, compose: m.composeN})
	if len(m.files) != 1 {
		t.Fatalf("a current clipboard read didn't attach: %+v", m.files)
	}

	// A drop while the chat list or thread pane is focused, before composing,
	// starts a message with the file attached.
	for focus := 0; focus < 2; focus++ {
		m := testModel(100, 30)
		m.section = secChats
		for i, r := range m.chatRows() {
			if !r.group {
				m.chatSel = i
				break
			}
		}
		m.chatFocus = focus
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("'" + f + "' "), Paste: true})
		if !m.composing || m.input.Value() != "" || len(m.files) != 1 || m.files[0].Name != "shot.png" {
			t.Fatalf("focus %d: composing %v input %q files %+v", focus, m.composing, m.input.Value(), m.files)
		}
	}

	t.Setenv("PATH", t.TempDir())
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("DISPLAY", "")
	if _, err := clipboardImage(); err != errNoTool {
		t.Fatalf("no tool: %v", err)
	}
	if toolHint("darwin") != "" || toolHint("windows") != "" || !strings.Contains(toolHint("linux"), "xclip") {
		t.Fatal("the install hint names Linux tools only on Linux")
	}
	// With no tool, ctrl+v still hands the input its text paste.
	_, cmd := m.keyInput(tea.KeyMsg{Type: tea.KeyCtrlV})
	if c, ok := cmd().(clipMsg); !ok || c.text == nil {
		t.Fatalf("ctrl+v with no tool: %+v", c)
	}
}

// TestClipboardTools: a tool with no image passes to the next one.
func TestClipboardTools(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("wl-paste and xclip are Linux tools")
	}
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "wl-paste"), []byte("#!/bin/sh\n"), 0o755)
	os.WriteFile(filepath.Join(bin, "xclip"), []byte("#!/bin/sh\nprintf '\\211PNGimg'\n"), 0o755)
	t.Setenv("PATH", bin)
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("DISPLAY", ":0")
	if f, err := clipboardImage(); err != nil || string(f.Data) != "\x89PNGimg" {
		t.Fatalf("got %q, %v", f.Data, err)
	}
	os.WriteFile(filepath.Join(bin, "xclip"), []byte("#!/bin/sh\n"), 0o755)
	if _, err := clipboardImage(); err != errNoImage {
		t.Fatalf("both empty: %v", err)
	}
}
