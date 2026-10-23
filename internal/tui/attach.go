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

// clipTools follows Claude Code: osascript on macOS, wl-paste on Wayland,
// xclip on X11, and PowerShell on Windows and from WSL.
func clipTools() []clipTool {
	ps := []string{"-NoProfile", "-Command", `Add-Type -AssemblyName System.Windows.Forms; $i = [Windows.Forms.Clipboard]::GetImage(); if ($i) { $m = New-Object IO.MemoryStream; $i.Save($m, [Drawing.Imaging.ImageFormat]::Png); [Convert]::ToBase64String($m.ToArray()) }`}
	b64 := func(b []byte) ([]byte, error) { return base64.StdEncoding.DecodeString(strings.TrimSpace(string(b))) }
	var out []clipTool
	switch {
	case runtime.GOOS == "darwin":
		out = append(out, clipTool{[]string{"osascript", "-e", "get the clipboard as «class PNGf»"}, func(b []byte) ([]byte, error) {
			s := strings.TrimSpace(string(b))
			s = strings.TrimSuffix(strings.TrimPrefix(s, "«data PNGf"), "»")
			return hex.DecodeString(s)
		}})
	case runtime.GOOS == "windows":
		out = append(out, clipTool{append([]string{"powershell"}, ps...), b64})
	default:
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			out = append(out, clipTool{[]string{"wl-paste", "--no-newline", "--type", "image/png"}, raw})
		}
		if os.Getenv("DISPLAY") != "" {
			out = append(out, clipTool{[]string{"xclip", "-selection", "clipboard", "-t", "image/png", "-o"}, raw})
		}
		out = append(out, clipTool{append([]string{"powershell.exe"}, ps...), b64})
	}
	return out
}

var (
	pngMagic   = []byte("\x89PNG")
	errNoImage = errors.New("no image on the clipboard")
	errNoTool  = errors.New("no clipboard tool found to read a pasted image" + toolHint(runtime.GOOS))
)

// toolHint names what to install, where that is wl-clipboard or xclip.
func toolHint(goos string) string {
	if goos == "darwin" || goos == "windows" {
		return ""
	}
	return "; install wl-clipboard (Wayland) or xclip (X11)"
}

// clipboardImage reads an image off the clipboard with the first tool found
// that has one.
func clipboardImage() (chat.File, error) {
	err := errNoTool
	for _, t := range clipTools() {
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
