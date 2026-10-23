// Package setup is the first run, `hive login`, `hive doctor`, starting with
// the machine, and uninstalling. The Hive never stores, copies or prints a
// login: it runs the tools' own logins in the person's terminal.
package setup

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
	"github.com/ihsan-sa/hive-standalone-build/internal/sandbox"
)

// Tool is a prerequisite.
type Tool struct {
	Name       string
	Found      bool
	LoggedIn   bool
	NeedsLogin bool
	Get        string // what to get and where
	Login      []string
}

// Check finds git, Claude Code and the GitHub CLI, and whether they're logged in.
func Check() []Tool {
	tools := []Tool{
		{Name: "git", Get: "Install git: https://git-scm.com/downloads"},
		{Name: "claude", NeedsLogin: true, Login: []string{"claude", "auth", "login"},
			Get: "Install Claude Code: https://claude.com/claude-code  (macOS/Linux: curl -fsSL https://claude.ai/install.sh | bash   Windows: irm https://claude.ai/install.ps1 | iex)"},
		{Name: "gh", NeedsLogin: true, Login: []string{"gh", "auth", "login"},
			Get: "Install the GitHub CLI: https://cli.github.com"},
	}
	for i := range tools {
		t := &tools[i]
		_, err := exec.LookPath(t.Name)
		t.Found = err == nil
		if !t.Found || !t.NeedsLogin {
			t.LoggedIn = t.Found
			continue
		}
		switch t.Name {
		case "gh":
			t.LoggedIn = exec.Command("gh", "auth", "status").Run() == nil
		case "claude":
			out, err := exec.Command("claude", "auth", "status", "--json").Output()
			t.LoggedIn = err == nil && regexp.MustCompile(`"loggedIn"\s*:\s*true`).Match(out)
		}
	}
	return tools
}

// Login runs each missing login in the person's terminal.
func Login(all bool) error {
	for _, t := range Check() {
		if !t.Found || !t.NeedsLogin || (t.LoggedIn && !all) {
			continue
		}
		fmt.Printf("\nLogging in to %s with its own login. Follow its link or code in your browser.\n", t.Name)
		cmd := exec.Command(t.Login[0], t.Login[1:]...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s login: %v", t.Name, err)
		}
	}
	return nil
}

// AddLogin runs harness h's own login into a config folder of its own under
// the workspace, which adds it to the logins runs are spread over. Deleting
// the folder removes it. A new folder whose login failed or left no
// credential is removed, so it never joins the pool.
func AddLogin(ws core.Workspace, h, name string) error {
	h = core.HarnessName(h)
	env := core.LoginDirEnv[h]
	hn, err := harness.Get(h)
	if env == "" || err != nil {
		return fmt.Errorf("%s has no separate logins", h)
	}
	if !filepath.IsLocal(name) || filepath.Base(name) != name || name == "." {
		return fmt.Errorf("a login's name is one plain word, not %q", name)
	}
	dir := filepath.Join(ws.LoginsDir(h), name)
	if err := os.MkdirAll(ws.LoginsDir(h), 0o700); err != nil {
		return err
	}
	fresh := os.Mkdir(dir, 0o700) == nil
	argv := hn.LoginCommand()
	fmt.Printf("\nLogging in to %s as %q with its own login. Follow its link or code in your browser.\n", h, name)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), env+"="+dir)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err = cmd.Run()
	if err == nil && !core.LoggedIn(core.Login{Harness: h, Name: name, Dir: dir}) {
		err = errors.New("it left no credential")
	}
	if err != nil {
		if fresh {
			os.RemoveAll(dir)
		}
		return fmt.Errorf("%s login: %v", h, err)
	}
	return nil
}

// Prompt asks a question on the terminal.
func Prompt(q, def string) string {
	if def != "" {
		fmt.Printf("%s [%s]: ", q, def)
	} else {
		fmt.Printf("%s: ", q)
	}
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

// SandboxReport explains the sandbox state and what to do about it.
func SandboxReport(ws core.Workspace, hiveLinux string) (string, error) {
	cfg := ws.LoadConfig()
	sb, err := sandbox.Select(sandbox.Options{Mode: "auto", WSLDistro: cfg.WSLDistro, HiveLinux: hiveLinux})
	if err == nil {
		return "sandbox: " + sb.Name(), nil
	}
	hint := ""
	switch runtime.GOOS {
	case "windows":
		if strings.Contains(err.Error(), "Linux hive binary") {
			hint = "The installer puts hive-linux-amd64 next to hive.exe; run the install line again."
			break
		}
		hint = "Agents run sandboxed inside WSL 2. Install it with `wsl --install -d Ubuntu`, then install Claude Code inside it:\n    wsl -d Ubuntu -- bash -lc \"curl -fsSL https://claude.ai/install.sh | bash\"\nand run `" + core.Cmd + " doctor` again."
	case "linux":
		hint = "Agents run in unprivileged user namespaces. If your distribution restricts them (Ubuntu 24.04+: kernel.apparmor_restrict_unprivileged_userns=1), allow them for the hive binary or system-wide, then run `" + core.Cmd + " doctor` again."
	case "darwin":
		hint = "Agents run under sandbox-exec, which ships with macOS."
	}
	return "", fmt.Errorf("%v\n%s", err, hint)
}

// CodexReport runs `codex --version` where agents run, when this Hive uses
// Codex or the machine has it, so a Codex the sandbox can't run shows up
// here rather than at the first turn. "" with no error: nothing to check.
func CodexReport(ws core.Workspace, hiveLinux string) (string, error) {
	cfg := ws.LoadConfig()
	uses := core.HarnessName(cfg.Harness) == "codex" || len(core.NewTokens(ws).Logins("codex")) > 1
	for _, s := range cfg.TribunalModels {
		uses = uses || core.HarnessName(s.Harness) == "codex"
	}
	p, err := exec.LookPath("codex")
	switch {
	case err != nil && !uses:
		return "", nil
	case err != nil && runtime.GOOS != "windows": // on Windows Codex lives inside WSL
		return "", errors.New("Codex is not installed. Install it system-wide, for example `sudo npm install -g @openai/codex` with the system's Node.")
	}
	mode := "auto"
	if cfg.Sandbox == "bare" && cfg.BareAccepted != "" {
		mode = "bare"
	}
	sb, err := sandbox.Select(sandbox.Options{Mode: mode, WSLDistro: cfg.WSLDistro, HiveLinux: hiveLinux})
	if err != nil {
		return "", nil // the sandbox line already says why
	}
	dir, err := os.MkdirTemp("", "hive-codex-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	work := sandbox.Mount{Host: dir, Inside: "/work", RW: true}
	s := sandbox.Spec{
		ID: "codex-check", Argv: []string{"codex", "--version"}, Env: sandbox.InsideEnv("/tmp", false),
		Cwd: sandbox.InsidePath(sb, work), Mounts: []sandbox.Mount{work}, Tools: []string{"codex"},
		Stdout: filepath.Join(dir, "out"), UID: os.Getuid(), GID: os.Getgid(),
	}
	if code, err := sb.Run(s, time.Minute); err == nil && code == 0 {
		return "codex runs where agents run", nil
	}
	errOut, _ := os.ReadFile(s.Stdout + ".err")
	if real, _ := filepath.EvalSymlinks(p); sb.Isolated() && strings.HasPrefix(real, core.HomeDir()+string(filepath.Separator)) {
		return "", fmt.Errorf("Codex is installed under your home folder (%s, from nvm or an npm prefix there), which the sandbox doesn't show agents. Install it system-wide, for example `sudo npm install -g @openai/codex` with the system's Node.", real)
	}
	return "", fmt.Errorf("Codex doesn't run inside the sandbox: %s", strings.TrimSpace(core.Clip(string(errOut), 300)))
}

// AcceptBare records that the person read the warning and opted into bare runs.
func AcceptBare(ws core.Workspace) {
	cfg := ws.LoadConfig()
	cfg.Sandbox, cfg.BareAccepted = "bare", time.Now().UTC().Format(time.RFC3339)
	ws.SaveConfig(cfg)
}

// WSLClaude reports whether Claude Code is installed inside the WSL distro.
func WSLClaude(distro string) bool {
	if runtime.GOOS != "windows" {
		return true
	}
	args := []string{"--exec", "sh", "-c", "command -v claude >/dev/null || test -x \"$HOME/.local/bin/claude\""}
	if distro != "" {
		args = append([]string{"-d", distro}, args...)
	}
	return exec.Command("wsl.exe", args...).Run() == nil
}

// Executable returns the running binary's path.
func Executable() string {
	exe, _ := os.Executable()
	exe, _ = filepath.EvalSymlinks(exe)
	return exe
}
