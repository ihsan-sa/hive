package tui

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/ihsan-sa/hive-standalone-build/internal/mod/chat"
)

// pastedPaths reads a paste as the files a terminal delivers for a drop:
// quoted or backslash-escaped paths, file:// URLs, and Windows paths (as
// /mnt/<drive> or the distro's own path under WSL). It returns nil unless
// every word of the paste names a file that exists, so text stays text, and
// a single word must also read as a path (absolute, ~, ./ or ../): pasting the
// word go.mod types it.
func pastedPaths(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if p := hostPath(s); isFile(p) && pathLike(p) {
		return []string{p}
	}
	ws := words(s)
	var out []string
	for _, w := range ws {
		p := hostPath(w)
		if !isFile(p) || len(ws) == 1 && !pathLike(p) {
			return nil
		}
		out = append(out, p)
	}
	return out
}

func pathLike(p string) bool { return filepath.IsAbs(p) || relPath.MatchString(p) }

var relPath = regexp.MustCompile(`^\.\.?[\\/]`) // ./ ../ .\ ..\

var (
	winDrive = regexp.MustCompile(`^[A-Za-z]:[\\/]`)
	winWord  = regexp.MustCompile(`^([A-Za-z]:|\\)`) // a word that started as C:… or \\server…
)

// words splits a paste like a shell would, except that a backslash in a
// Windows path is a separator, not an escape.
func words(s string) []string {
	var out []string
	var w strings.Builder
	in, quote := false, rune(0)
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else {
				w.WriteRune(c)
			}
		case c == '\'' || c == '"':
			quote, in = c, true
		case c == '\\' && i+1 < len(rs) && !winWord.MatchString(w.String()+string(rs[i+1])):
			i++
			w.WriteRune(rs[i])
			in = true
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			if in {
				out = append(out, w.String())
				w.Reset()
				in = false
			}
		default:
			w.WriteRune(c)
			in = true
		}
	}
	if in {
		out = append(out, w.String())
	}
	return out
}

var wslShare = regexp.MustCompile(`(?i)^\\\\wsl(?:\$|\.localhost)\\[^\\]+`)

// hostPath turns one pasted word into a path on this machine.
func hostPath(w string) string {
	if strings.HasPrefix(w, "file://") {
		if u, err := url.Parse(w); err == nil {
			w = u.Path
			if runtime.GOOS == "windows" {
				w = filepath.FromSlash(strings.TrimPrefix(w, "/")) // /C:/a → C:\a
			}
		}
	}
	if h, err := os.UserHomeDir(); err == nil && strings.HasPrefix(w, "~/") {
		w = filepath.Join(h, w[2:])
	}
	if runtime.GOOS == "windows" {
		return w
	}
	if m := wslShare.FindString(w); m != "" {
		return strings.ReplaceAll(w[len(m):], `\`, "/")
	}
	if winDrive.MatchString(w) {
		return "/mnt/" + strings.ToLower(w[:1]) + strings.ReplaceAll(w[2:], `\`, "/")
	}
	return w
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

// readAttachment reads a dropped file. Before reading it refuses anything
// that isn't a regular file, or that would take the message, with the have
// bytes already attached, over maxMB (chat_max_mb, which the daemon holds a
// message to as well), so a huge drop gets a message, not a frozen screen.
func readAttachment(p string, have int64, maxMB int) (chat.File, error) {
	name := filepath.Base(p)
	fi, err := os.Stat(p)
	switch {
	case err != nil:
		return chat.File{}, err
	case !fi.Mode().IsRegular():
		return chat.File{}, fmt.Errorf("%s is not a file, so it can't be attached", name)
	case have+fi.Size() > int64(maxMB)<<20:
		return chat.File{}, fmt.Errorf("%s would make the message %d MB; one message can be at most %d MB; chat_max_mb in .hive/config.json raises it", name, (have+fi.Size()+1<<20-1)>>20, maxMB)
	}
	data, err := os.ReadFile(p)
	return chat.File{Name: name, Data: data}, err
}

// clipTool is a command that prints the clipboard's image, and how to decode it.
type clipTool struct {
	argv   []string
	decode func([]byte) ([]byte, error)
}

func raw(b []byte) ([]byte, error) { return b, nil }

// clipSetup is where the control center runs, which decides how it can
// reach the clipboard: the person's clipboard is on the machine whose
// screen they look at, which over SSH isn't this one.
type clipSetup struct {
	goos         string
	wsl, ssh     bool // Linux under WSL; a session over SSH
	wayland, x11 bool // a display this session can reach (WSLg's, or one SSH forwards)
}

func thisSetup() clipSetup {
	s := clipSetup{goos: runtime.GOOS, wayland: os.Getenv("WAYLAND_DISPLAY") != "", x11: os.Getenv("DISPLAY") != ""}
	s.ssh = os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_CLIENT") != "" || os.Getenv("SSH_TTY") != ""
	if s.goos == "linux" {
		rel, _ := os.ReadFile("/proc/sys/kernel/osrelease")
		s.wsl = os.Getenv("WSL_DISTRO_NAME") != "" || os.Getenv("WSL_INTEROP") != "" || bytes.Contains(bytes.ToLower(rel), []byte("microsoft"))
	}
	return s
}

// winPowerShell is Windows PowerShell where WSL mounts C:, for a distro that
// leaves Windows off its PATH (appendWindowsPath=false).
const winPowerShell = "/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe"

// clipTools follows Claude Code: osascript on macOS, wl-paste on Wayland,
// xclip on X11, and PowerShell on Windows and from WSL. PowerShell runs -STA,
// since the clipboard reads nothing on another thread (pwsh's default), and
// pwsh stands in where it's the only one.
func clipTools(s clipSetup) []clipTool {
	ps := []string{"-NoProfile", "-STA", "-Command", `Add-Type -AssemblyName System.Windows.Forms, System.Drawing; $i = [Windows.Forms.Clipboard]::GetImage(); if ($i) { $m = New-Object IO.MemoryStream; $i.Save($m, [Drawing.Imaging.ImageFormat]::Png); [Convert]::ToBase64String($m.ToArray()) }`}
	b64 := func(b []byte) ([]byte, error) { return base64.StdEncoding.DecodeString(strings.TrimSpace(string(b))) }
	pwsh := func(names ...string) (out []clipTool) {
		for _, n := range names {
			out = append(out, clipTool{append([]string{n}, ps...), b64})
		}
		return out
	}
	var out []clipTool
	switch {
	case s.goos == "darwin":
		out = append(out, clipTool{[]string{"osascript", "-e", "get the clipboard as «class PNGf»"}, func(b []byte) ([]byte, error) {
			s := strings.TrimSpace(string(b))
			s = strings.TrimSuffix(strings.TrimPrefix(s, "«data PNGf"), "»")
			return hex.DecodeString(s)
		}})
	case s.goos == "windows":
		out = pwsh("powershell", "pwsh")
	default:
		if s.wsl { // Windows' own clipboard first: WSLg's carries images poorly
			out = pwsh("powershell.exe", winPowerShell, "pwsh.exe")
		}
		if s.wayland {
			out = append(out, clipTool{[]string{"wl-paste", "--no-newline", "--type", "image/png"}, raw})
		}
		if s.x11 {
			out = append(out, clipTool{[]string{"xclip", "-selection", "clipboard", "-t", "image/png", "-o"}, raw})
		}
	}
	return out
}

var (
	pngMagic   = []byte("\x89PNG")
	errNoImage = errors.New("no image on the clipboard")
	errNoTool  = errors.New("no clipboard tool found to read a pasted image")
)

// noTool says why no clipboard could be read here and what to do instead.
func noTool(s clipSetup) error {
	switch {
	case s.ssh && !s.wsl:
		// The terminal holds the clipboard, and no terminal hands an image over SSH.
		return fmt.Errorf("%w: over SSH this control center can't reach the clipboard of the computer you're on. Copy the image to this machine (for example `scp shot.png <host>:`) and paste its path here, or run the control center on that computer", errNoTool)
	case s.wsl:
		return fmt.Errorf("%w: powershell.exe isn't reachable from WSL (is C: mounted, with Windows interop on?). Drop the image's file onto the window, or paste its path", errNoTool)
	case s.goos == "windows":
		return fmt.Errorf("%w: neither powershell nor pwsh is on PATH. Drop the image's file onto the window, or paste its path", errNoTool)
	case s.goos == "darwin":
		return fmt.Errorf("%w: osascript isn't on PATH. Drop the image's file onto the window, or paste its path", errNoTool)
	case !s.wayland && !s.x11:
		return fmt.Errorf("%w: this session has no display to read a clipboard from. Drop the image's file onto the window, or paste its path", errNoTool)
	}
	return fmt.Errorf("%w; install wl-clipboard (Wayland) or xclip (X11), or drop the image's file onto the window", errNoTool)
}

// clipboardImage reads an image off the clipboard with the first tool found
// that has one.
func clipboardImage() (chat.File, error) {
	setup := thisSetup()
	err := noTool(setup)
	for _, t := range clipTools(setup) {
		if _, e := exec.LookPath(t.argv[0]); e != nil {
			continue
		}
		err = errNoImage
		out, _ := exec.Command(t.argv[0], t.argv[1:]...).Output()
		if img, e := t.decode(out); e == nil && bytes.HasPrefix(img, pngMagic) {
			return chat.File{Name: "pasted.png", Data: img}, nil
		}
	}
	return chat.File{}, err
}
