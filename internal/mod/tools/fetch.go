package tools

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"github.com/ihsan-sa/hive-standalone-build/internal/sandbox"
)

// Fetch is `hive tools-fetch <folder> <tool> [version]`, run inside a sandbox
// with the tools folder writable. It installs tool into <folder>/<tool> from
// the official source, checked against the official checksum, and writes the
// version it installed to <folder>/<tool>.version. An empty version is the
// newest stable one.
func Fetch(folder, tool, version string) (string, error) {
	if err := checkInput(tool, version); err != nil {
		return "", err
	}
	if src := os.Getenv("HIVE_TOOLS_SOURCE"); src != "" { // tests: <src>/<tool>.tar.gz and its .sha256
		return unpack(folder, tool, "", "file://"+filepath.Join(src, tool+".tar.gz"), "")
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return "", fmt.Errorf("tools install on Linux and macOS (on Windows, inside the WSL sandbox), not %s", runtime.GOOS)
	}
	return map[string]func(string, string) (string, error){"python": python, "uv": uv, "node": node, "go": goTool}[tool](folder, version)
}

func checkInput(tool, version string) error {
	if !slices.Contains(sandbox.ToolNames, tool) {
		return fmt.Errorf("no tool %q; the Hive installs %s", tool, strings.Join(sandbox.ToolNames, ", "))
	}
	if !versionOK.MatchString(version) {
		return fmt.Errorf("version %q: use digits and dots, like 3.12", version)
	}
	return nil
}

var versionOK = regexp.MustCompile(`^([0-9][0-9.]*)?$`)

var (
	arch     = map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
	nodeArch = map[string]string{"amd64": "x64", "arm64": "arm64"}[runtime.GOARCH]
)

// uv comes from its GitHub releases, each archive with a .sha256 beside it.
func uv(folder, version string) (string, error) {
	if version == "" {
		resp, err := http.Get("https://github.com/astral-sh/uv/releases/latest")
		if err != nil {
			return "", err
		}
		resp.Body.Close()
		version = filepath.Base(resp.Request.URL.Path) // .../releases/tag/<version>
	}
	triple := arch + "-unknown-linux-gnu"
	if runtime.GOOS == "darwin" {
		triple = arch + "-apple-darwin"
	}
	return unpack(folder, "uv", version, fmt.Sprintf("https://github.com/astral-sh/uv/releases/download/%s/uv-%s.tar.gz", version, triple), "bin")
}

// python is uv's: uv installs a standalone build, which it checks against
// the checksums it ships with, and links python and python3 into bin.
func python(folder, version string) (string, error) {
	bin := filepath.Join(folder, "uv", "bin", "uv")
	if _, err := os.Stat(bin); err != nil {
		if _, err := uv(folder, ""); err != nil {
			return "", fmt.Errorf("uv, to install python: %w", err)
		}
	}
	args := []string{"python", "install", "--default", "--force"}
	if version != "" {
		args = append(args, version)
	}
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "UV_PYTHON_INSTALL_DIR="+filepath.Join(folder, "python"), "UV_PYTHON_BIN_DIR="+filepath.Join(folder, "python", "bin"))
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("uv python install: %v\n%s", err, out)
	}
	out, err := exec.Command(filepath.Join(folder, "python", "bin", "python3"), "--version").Output()
	if err != nil {
		return "", fmt.Errorf("python3 --version after the install: %w", err)
	}
	v := strings.TrimPrefix(strings.TrimSpace(string(out)), "Python ")
	return v, os.WriteFile(filepath.Join(folder, "python.version"), []byte(v+"\n"), 0o644)
}

// node takes the newest LTS for no version and the newest match for a
// prefix like 22; SHASUMS256.txt beside the archive has its checksum.
func node(folder, version string) (string, error) {
	var index []struct {
		Version string `json:"version"`
		LTS     any    `json:"lts"`
	}
	if err := getJSON("https://nodejs.org/dist/index.json", &index); err != nil {
		return "", err
	}
	v := ""
	for _, x := range index {
		if version == "" && x.LTS != false || version != "" && (x.Version == "v"+version || strings.HasPrefix(x.Version, "v"+version+".")) {
			v = x.Version
			break
		}
	}
	if v == "" {
		return "", fmt.Errorf("no node release %q", version)
	}
	name := fmt.Sprintf("node-%s-%s-%s.tar.gz", v, runtime.GOOS, nodeArch)
	sums, err := get("https://nodejs.org/dist/" + v + "/SHASUMS256.txt")
	if err != nil {
		return "", err
	}
	return unpackSum(folder, "node", strings.TrimPrefix(v, "v"), "https://nodejs.org/dist/"+v+"/"+name, "", sumFor(sums, name))
}

// goTool reads go.dev's release list, which carries each archive's sha256.
func goTool(folder, version string) (string, error) {
	var rel []struct {
		Version string `json:"version"`
		Stable  bool   `json:"stable"`
		Files   []struct {
			Filename, OS, Arch, Kind, SHA256 string
		} `json:"files"`
	}
	if err := getJSON("https://go.dev/dl/?mode=json&include=all", &rel); err != nil {
		return "", err
	}
	for _, r := range rel {
		if version == "" && !r.Stable || version != "" && r.Version != "go"+version && !strings.HasPrefix(r.Version, "go"+version+".") {
			continue
		}
		for _, f := range r.Files {
			if f.OS == runtime.GOOS && f.Arch == runtime.GOARCH && f.Kind == "archive" {
				return unpackSum(folder, "go", strings.TrimPrefix(r.Version, "go"), "https://go.dev/dl/"+f.Filename, "", f.SHA256)
			}
		}
	}
	return "", fmt.Errorf("no go release %q for %s/%s", version, runtime.GOOS, runtime.GOARCH)
}

// unpack fetches url and its checksum from url+".sha256".
func unpack(folder, tool, version, url, sub string) (string, error) {
	sum, err := get(url + ".sha256")
	if err != nil {
		return "", err
	}
	want, _, _ := strings.Cut(strings.TrimSpace(string(sum)), " ")
	return unpackSum(folder, tool, version, url, sub, want)
}

// unpackSum downloads a .tar.gz, refuses it unless its sha256 is sum, and
// replaces <folder>/<tool> with it (its top directory dropped, under sub).
func unpackSum(folder, tool, version, url, sub, sum string) (string, error) {
	raw, err := get(url)
	if err != nil {
		return "", err
	}
	got := sha256.Sum256(raw)
	if sum == "" || !strings.EqualFold(hex.EncodeToString(got[:]), sum) {
		return "", fmt.Errorf("%s: checksum mismatch (got %x, want %q); nothing installed", url, got, sum)
	}
	stale, _ := filepath.Glob(filepath.Join(folder, "."+tool+"-*")) // left by an install the sandbox timed out
	for _, p := range stale {
		os.RemoveAll(p)
	}
	tmp, err := os.MkdirTemp(folder, "."+tool+"-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	if err := untar(raw, filepath.Join(tmp, sub)); err != nil {
		return "", err
	}
	dst := filepath.Join(folder, tool)
	os.RemoveAll(dst)
	if err := os.Rename(tmp, dst); err != nil {
		return "", err
	}
	if version == "" {
		version = "from " + url
	}
	return version, os.WriteFile(filepath.Join(folder, tool+".version"), []byte(version+"\n"), 0o644)
}

// untar writes a gzipped tar into dir, dropping its top directory. Every
// write goes through an os.Root, so no entry or symlink reaches outside dir,
// and a link whose target leaves dir is refused.
func untar(raw []byte, dir string) error {
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		_, rest, _ := strings.Cut(strings.TrimPrefix(h.Name, "./"), "/")
		if rest == "" {
			continue
		}
		p := filepath.Clean(rest)
		if !filepath.IsLocal(p) {
			return fmt.Errorf("archive entry %q leaves its folder", h.Name)
		}
		root.MkdirAll(filepath.Dir(p), 0o755)
		switch h.Typeflag {
		case tar.TypeDir:
			err = root.MkdirAll(p, 0o755)
		case tar.TypeSymlink:
			if filepath.IsAbs(h.Linkname) || !filepath.IsLocal(filepath.Join(filepath.Dir(p), h.Linkname)) {
				return fmt.Errorf("archive link %q -> %q leaves its folder", h.Name, h.Linkname)
			}
			err = root.Symlink(h.Linkname, p)
		case tar.TypeReg:
			var f *os.File
			if f, err = root.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, h.FileInfo().Mode().Perm()|0o200); err == nil {
				_, err = io.Copy(f, tr)
				f.Close()
			}
		}
		if err != nil {
			return err
		}
	}
}

func get(url string) ([]byte, error) {
	if p, ok := strings.CutPrefix(url, "file://"); ok {
		return os.ReadFile(p)
	}
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func getJSON(url string, v any) error {
	raw, err := get(url)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

// sumFor finds name's checksum in a SHASUMS file ("<hex>  <name>" lines).
func sumFor(sums []byte, name string) string {
	for _, line := range strings.Split(string(sums), "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[1] == name {
			return f[0]
		}
	}
	return ""
}
