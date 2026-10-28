package setup

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/text"
)

// The names the person gives logins (`hive login name`) are only what they
// see: records, usage and the folders keep core.Login.Key, so renaming a
// login loses none of its history, and the machine's own login, which has
// no folder, can be named too. They live in .hive/logins/names.json, by key.

func namesFile(ws core.Workspace) string { return filepath.Join(ws.Hive(), "logins", "names.json") }

// LoginNames are the names the person gave logins, by key.
func LoginNames(ws core.Workspace) map[string]string {
	names := map[string]string{}
	b, _ := os.ReadFile(namesFile(ws))
	json.Unmarshal(b, &names)
	return names
}

// LoginLabel is how the person sees the login with key: the name they gave
// it with the key beside it, or the key.
func LoginLabel(names map[string]string, key string) string {
	if n := names[key]; n != "" {
		return n + " (" + key + ")"
	}
	return key
}

// NameLogin names or renames a login (args: <login> <name>), or lists the
// logins with their names (no args). A login is picked by its key (claude,
// codex, claude:<folder>), its folder's name or the name it has now; an
// empty name clears it. A name another login goes by already, as its name,
// key or folder, is refused, so a later pick by it stays unambiguous.
func NameLogin(ws core.Workspace, args []string, out io.Writer) error {
	names := LoginNames(ws)
	var all []core.Login
	tok := core.NewTokens(ws)
	for _, h := range []string{"claude", "codex"} {
		all = append(all, tok.Logins(h)...)
	}
	if len(args) == 0 {
		for _, l := range all {
			n := text.Visible(names[l.Key()])
			if n == "" {
				n = "(no name)"
			}
			fmt.Fprintf(out, "%s  %s\n", l.Key(), n)
		}
		return nil
	}
	if len(args) != 2 {
		return fmt.Errorf(`usage: %s login name <login> <name>   ("" clears the name)`, core.Cmd)
	}
	key, err := findLogin(all, names, args[0])
	if err != nil {
		return err
	}
	name := strings.TrimSpace(args[1])
	if name != "" {
		for _, l := range all {
			if l.Key() != key && (name == l.Key() || name == l.Name || name == names[l.Key()]) {
				return fmt.Errorf("%s is what the %s login goes by already", name, l.Key())
			}
		}
	}
	if name == "" {
		delete(names, key)
	} else {
		names[key] = name
	}
	b, err := json.MarshalIndent(names, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(namesFile(ws)), 0o700); err != nil {
		return err
	}
	if err := core.WriteFileAtomic(namesFile(ws), b, 0o600); err != nil {
		return err
	}
	if name == "" {
		fmt.Fprintf(out, "The %s login has no name now.\n", key)
	} else {
		fmt.Fprintf(out, "The %s login is named %s now.\n", key, text.Visible(name))
	}
	return nil
}

// findLogin picks from logins the one the person means by s.
func findLogin(logins []core.Login, names map[string]string, s string) (string, error) {
	var keys []string
	for _, l := range logins {
		if s != "" && (s == l.Key() || s == l.Name || s == names[l.Key()]) {
			keys = append(keys, l.Key())
		}
	}
	switch len(keys) {
	case 0:
		return "", fmt.Errorf("no login is %q; `%s login name` lists them", s, core.Cmd)
	case 1:
		return keys[0], nil
	}
	return "", fmt.Errorf("%q could be %s; name it by one of those", s, strings.Join(keys, " or "))
}
