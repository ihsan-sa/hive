package gate

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Install puts a kept candidate's binary at target and keeps the one it
// replaces beside it as target+".prev". The new install must answer its health
// check (its --version runs and names the version asked for); if it
// does not, the previous binary goes back and the error says why. Nothing is
// changed unless the gate said keep, so a caller reaches Install only with a
// passing Report.
func Install(bin, target, wantVersion string) error {
	prev := target + ".prev"
	hadPrev := false
	if _, err := os.Stat(target); err == nil {
		if err := copyFile(target, prev); err != nil {
			return fmt.Errorf("keeping the previous binary: %v", err)
		}
		hadPrev = true
	}
	tmp := target + ".new"
	if err := copyFile(bin, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := health(target, wantVersion); err != nil {
		if hadPrev {
			if rerr := os.Rename(prev, target); rerr != nil {
				return fmt.Errorf("the new install failed (%v) and rolling back failed too: %v", err, rerr)
			}
		} else {
			os.Remove(target)
		}
		return fmt.Errorf("the new install failed its health check and was rolled back: %v", err)
	}
	return nil
}

// health runs the installed binary.
func health(bin, wantVersion string) error {
	run := func(args ...string) (string, error) {
		cmd := exec.Command(bin, args...)
		cmd.Env = cleanEnv()
		done := make(chan struct{})
		var out []byte
		var err error
		go func() { out, err = cmd.CombinedOutput(); close(done) }()
		select {
		case <-done:
		case <-time.After(time.Minute):
			cmd.Process.Kill()
			return "", fmt.Errorf("%v timed out", args)
		}
		return strings.TrimSpace(string(out)), err
	}
	out, err := run("--version")
	if err != nil {
		return fmt.Errorf("--version: %v: %s", err, tail(out, 200))
	}
	if f := strings.Fields(out); wantVersion != "" && (len(f) == 0 || f[len(f)-1] != wantVersion) {
		return fmt.Errorf("--version says %q, wanted %q", out, wantVersion)
	}
	return nil
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// InstallTo installs the candidate the report is about, if the report says
// keep, the children ran isolated by bubblewrap, and the binary is byte for
// byte the one built and hashed before any of its code ran. The health check
// after the install wants the version the gate stamped into it.
func (r Report) InstallTo(target string) error {
	switch {
	case !r.Verdict.Keep:
		return fmt.Errorf("the gate did not keep this change")
	case r.Sandbox != "bwrap":
		return fmt.Errorf("the children ran %s, not isolated by bubblewrap", r.Sandbox)
	case r.CandBin == "" || r.CandSHA256 == "":
		return fmt.Errorf("the report names no verified binary")
	case r.CandVersion == "":
		return fmt.Errorf("the report names no version for the binary (a prebuilt one is never installed)")
	}
	if err := r.verify(); err != nil {
		return err
	}
	if got, err := FileSHA(r.CandBin); err != nil || got != r.CandSHA256 {
		return fmt.Errorf("the candidate's binary does not match the hash taken after the build")
	}
	return Install(r.CandBin, target, r.CandVersion)
}
