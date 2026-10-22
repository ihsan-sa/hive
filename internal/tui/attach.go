package tui

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
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
// every word of the paste names a file that exists, so text stays text.
func pastedPaths(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if isFile(s) {
		return []string{s}
	}
	var out []string
	for _, w := range words(s) {
		p := hostPath(w)
		if !isFile(p) {
			return nil
		}
		out = append(out, p)
	}
	return out
}

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
				w = strings.TrimPrefix(w, "/")
			}
		}
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

// readAttachment reads a pasted file, within the size cap.
func readAttachment(p string) (chat.File, error) {
	fi, err := os.Stat(p)
	if err != nil {
		return chat.File{}, err
	}
	if err := chat.TooBig(filepath.Base(p), fi.Size()); err != nil {
		return chat.File{}, err
	}
	data, err := os.ReadFile(p)
	return chat.File{Name: filepath.Base(p), Data: data}, err
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
)

// clipboardImage reads an image off the clipboard with the first tool found.
func clipboardImage() (chat.File, error) {
	for _, t := range clipTools() {
		if _, err := exec.LookPath(t.argv[0]); err != nil {
			continue
		}
		out, _ := exec.Command(t.argv[0], t.argv[1:]...).Output()
		img, err := t.decode(out)
		if err != nil || !bytes.HasPrefix(img, pngMagic) {
			return chat.File{}, errNoImage
		}
		return chat.File{Name: "pasted.png", Data: img}, chat.TooBig("the clipboard image", int64(len(img)))
	}
	hint := "no clipboard tool found to read a pasted image"
	if runtime.GOOS == "linux" {
		hint += "; install wl-clipboard (Wayland) or xclip (X11)"
	}
	return chat.File{}, errors.New(hint)
}
