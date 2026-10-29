package sandbox

import (
	"os"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/proc"
)

// Delegate hands a turn's spec to another program, an executor module, which
// runs it somewhere else and brings its files back before it exits: as WSL
// hands its spec to the distro's sandbox-exec, it writes the spec to a file
// (which the program deletes once read: it carries the turn's token) and
// starts `<Argv...> turn --spec <file> [--runner <Where>]`. The spec is built
// with inside paths, as for the local sandbox, and the program writes the
// turn's output to the spec's stdout and stops when its stop_file appears.
type Delegate struct {
	Module string   // the module's name
	Where  string   // the runner that said it has room
	Argv   []string // the module's program
	Env    []string // its scrubbed environment
	Dir    string   // its folder
	// Started, if set, is called once a turn is handed over, so the room
	// answer it went on isn't spent again.
	Started func()
}

func (d *Delegate) Name() string   { return "module:" + d.Module + "@" + d.Where }
func (d *Delegate) Isolated() bool { return true }
func (d *Delegate) Check() error   { return nil }

func (d *Delegate) Start(s Spec) (Proc, error) {
	specPath, err := writeTempSpec(s)
	if err != nil {
		return Proc{}, err
	}
	argv := append(append([]string{}, d.Argv...), "turn", "--spec", specPath)
	if d.Where != "" {
		argv = append(argv, "--runner", d.Where)
	}
	p, err := proc.StartDetached(argv, d.Env, d.Dir, "", s.Stdout+".dispatch")
	if err != nil {
		os.Remove(specPath)
		return p, err
	}
	if d.Started != nil {
		d.Started()
	}
	return p, nil
}

// Run runs a check's spec on the module the same way, to completion: the
// module writes its output to the spec's stdout, and a check past timeout is
// ended with the module's program, which ends it on the runner too.
func (d *Delegate) Run(s Spec, timeout time.Duration) (int, error) {
	specPath, err := writeTempSpec(s)
	if err != nil {
		return -1, err
	}
	argv := append(append([]string{}, d.Argv...), "turn", "--spec", specPath)
	if d.Where != "" {
		argv = append(argv, "--runner", d.Where)
	}
	if d.Started != nil {
		d.Started()
	}
	code, err := runSync(argv, d.Env, d.Dir, "", s.Stdout+".dispatch", timeout, nil)
	os.Remove(specPath) // the module removes it once read; not if it never started
	return code, err
}
