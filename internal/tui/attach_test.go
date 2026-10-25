package tui

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

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
	m.focusBox("")
	f := filepath.Join(t.TempDir(), "shot.png")
	os.WriteFile(f, []byte("\x89PNG..."), 0o644)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("'" + f + "' "), Paste: true})
	if m.input.Value() != "" || len(m.files) != 1 || m.files[0].Name != "shot.png" || string(m.files[0].Data) != "\x89PNG..." {
		t.Fatalf("input %q files %+v", m.input.Value(), m.files)
	}
	if v := plain(m.View()); !strings.Contains(v, "📎 shot.png") {
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
	m.focusBox("")
	m.Update(clipMsg{f: chat.File{Name: "pasted.png"}, compose: stale})
	if len(m.files) != 0 {
		t.Fatalf("a stale clipboard read attached: %+v", m.files)
	}
	m.Update(clipMsg{f: chat.File{Name: "pasted.png"}, compose: m.composeN})
	if len(m.files) != 1 {
		t.Fatalf("a current clipboard read didn't attach: %+v", m.files)
	}
	// The box stays open after a send, so a read still running for the sent
	// message stays out of the next one too.
	sentN := m.composeN
	m.send("with the picture")
	m.Update(clipMsg{f: chat.File{Name: "late.png"}, compose: sentN})
	if len(m.files) != 0 {
		t.Fatalf("a read for a sent message attached to the next: %+v", m.files)
	}

	// A drop while the sidebar or the channel has focus, before composing,
	// starts a message with the file attached.
	for focus := 0; focus < 2; focus++ {
		m := testModel(100, 30)
		m.focus = focus
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("'" + f + "' "), Paste: true})
		if !m.composing || m.input.Value() != "" || len(m.files) != 1 || m.files[0].Name != "shot.png" {
			t.Fatalf("focus %d: composing %v input %q files %+v", focus, m.composing, m.input.Value(), m.files)
		}
	}
	// With a thread open, the drop goes to its reply box.
	r := testModel(120, 40)
	r.thread, r.focus = "1", 0
	r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("'" + f + "' "), Paste: true})
	if r.focus != 2 || r.reply != "1" || len(r.files) != 1 {
		t.Fatalf("drop with a thread open: focus %d reply %q files %+v", r.focus, r.reply, r.files)
	}

	t.Setenv("PATH", t.TempDir())
	for _, v := range []string{"WAYLAND_DISPLAY", "DISPLAY", "SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY"} {
		t.Setenv(v, "")
	}
	if _, err := clipboardImage(); !errors.Is(err, errNoTool) && !thisSetup().wsl { // WSL may find C:'s PowerShell
		t.Fatalf("no tool: %v", err)
	}
	// With no tool, ctrl+v still hands the input its text paste.
	_, cmd := m.keyInput(tea.KeyMsg{Type: tea.KeyCtrlV})
	if c, ok := cmd().(clipMsg); !ok || c.text == nil {
		t.Fatalf("ctrl+v with no tool: %+v", c)
	}
	// alt+v, the image key where the terminal keeps ctrl+v, says why no
	// image came instead of pasting text.
	_, cmd = m.keyInput(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v"), Alt: true})
	if m.flash != "reading the clipboard…" {
		t.Fatalf("alt+v shows nothing while it reads: %q", m.flash)
	}
	c, ok := cmd().(clipMsg)
	if !ok || c.text != nil || c.err == nil {
		t.Fatalf("alt+v with no tool: %+v", c)
	}
	m.Update(c)
	if !strings.HasPrefix(m.flash, "error: ") || !strings.Contains(m.flash, "clipboard") {
		t.Fatalf("alt+v's error isn't shown: %q", m.flash)
	}
}

// TestPowerShellFailureSays: PowerShell that fails says why, where before
// its error read as an empty clipboard and a paste changed nothing.
func TestPowerShellFailureSays(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("a shell script stands in for powershell.exe")
	}
	if _, err := os.Stat(winPowerShell); err == nil {
		t.Skip("a real powershell.exe is reachable here")
	}
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "powershell.exe"), []byte("#!/bin/sh\necho 'Add-Type : Cannot add type.' >&2\necho 'more' >&2\nexit 1\n"), 0o755)
	t.Setenv("PATH", bin)
	t.Setenv("WSL_DISTRO_NAME", "Ubuntu")
	for _, v := range []string{"WAYLAND_DISPLAY", "DISPLAY", "SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY"} {
		t.Setenv(v, "")
	}
	_, err := clipboardImage()
	if !errors.Is(err, errNoImage) || !strings.Contains(err.Error(), "powershell.exe couldn't read it: Add-Type : Cannot add type.") {
		t.Fatalf("got %v", err)
	}
	// One that runs and finds no image is just no image.
	os.WriteFile(filepath.Join(bin, "powershell.exe"), []byte("#!/bin/sh\n"), 0o755)
	if _, err := clipboardImage(); err != errNoImage {
		t.Fatalf("empty: %v", err)
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

// TestClipboardPerSetup: each way a person may run the control center reads
// the clipboard it can reach, and where none answers says why and what to do.
func TestClipboardPerSetup(t *testing.T) {
	argv0 := func(ts []clipTool) (out []string) {
		for _, t := range ts {
			out = append(out, t.argv[0])
			if strings.Contains(t.argv[0], "powershell") || strings.Contains(t.argv[0], "pwsh") {
				if !slices.Contains(t.argv, "-STA") {
					panic(t.argv[0] + " runs without -STA, where the clipboard reads nothing")
				}
			}
		}
		return out
	}
	for _, c := range []struct {
		name  string
		s     clipSetup
		tools []string
		says  []string // what the no-tool message says
		not   []string // and doesn't
	}{
		{"Windows binary in Windows Terminal", clipSetup{goos: "windows"}, []string{"powershell", "pwsh"}, []string{"powershell", "paste its path"}, []string{"xclip"}},
		{"Linux binary under WSL", clipSetup{goos: "linux", wsl: true}, []string{"powershell.exe", winPowerShell, "pwsh.exe"}, []string{"WSL", "paste its path"}, []string{"xclip"}},
		{"WSL with WSLg", clipSetup{goos: "linux", wsl: true, wayland: true, x11: true}, []string{"powershell.exe", winPowerShell, "pwsh.exe", "wl-paste", "xclip"}, nil, nil},
		{"Linux host over SSH from Windows", clipSetup{goos: "linux", ssh: true}, nil, []string{"SSH", "scp", "path of a file on this machine", "remote "}, []string{"xclip", "wl-clipboard"}},
		{"SSH forwarding X", clipSetup{goos: "linux", ssh: true, x11: true}, []string{"xclip"}, []string{"SSH"}, nil},
		{"Linux desktop", clipSetup{goos: "linux", wayland: true}, []string{"wl-paste"}, []string{"wl-clipboard", "xclip"}, nil},
		{"Linux console", clipSetup{goos: "linux"}, nil, []string{"no display", "paste its path"}, []string{"xclip"}},
		{"macOS", clipSetup{goos: "darwin"}, []string{"osascript"}, []string{"osascript"}, []string{"xclip"}},
	} {
		if got := argv0(clipTools(c.s)); !slices.Equal(got, c.tools) {
			t.Errorf("%s: tools %q, want %q", c.name, got, c.tools)
		}
		err := noTool(c.s)
		if !errors.Is(err, errNoTool) {
			t.Errorf("%s: %v isn't errNoTool, so ctrl+v wouldn't paste text", c.name, err)
		}
		for _, w := range c.says {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("%s: %q doesn't say %q", c.name, err, w)
			}
		}
		for _, w := range c.not {
			if strings.Contains(err.Error(), w) {
				t.Errorf("%s: %q says %q", c.name, err, w)
			}
		}
	}
}

// TestThisSetup: WSL and SSH are read from the session's environment.
func TestThisSetup(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("WSL is Linux")
	}
	for _, v := range []string{"WSL_DISTRO_NAME", "WSL_INTEROP", "SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY"} {
		t.Setenv(v, "")
	}
	t.Setenv("WSL_DISTRO_NAME", "Ubuntu")
	t.Setenv("SSH_CONNECTION", "10.0.0.2 50000 10.0.0.1 22")
	if s := thisSetup(); !s.wsl || !s.ssh {
		t.Fatalf("setup %+v, want WSL over SSH", s)
	}
}
