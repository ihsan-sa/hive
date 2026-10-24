package core

import (
	"errors"
	"slices"
	"strings"

	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
)

// A model on an open harness sometimes answers with the text of a
// `hive agent` command instead of running it. printedCommand finds such a
// command at the start of a line of a turn's answer (inside a code fence,
// after a "$ " prompt or in backticks, too) and returns its op and its
// --key value pairs, read the way a shell would split them.
func printedCommand(text string) (op string, args map[string]string, ok bool) {
	text = strings.ReplaceAll(text, "\\\n", " ")
	for i := 0; i < len(text); {
		line := text[i:]
		if j := strings.IndexByte(line, '\n'); j >= 0 {
			i += j + 1
		} else {
			i = len(text)
		}
		rest := strings.TrimLeft(line, " \t`$>")
		if !strings.HasPrefix(rest, "hive agent ") {
			continue
		}
		words := shellWords(strings.TrimPrefix(rest, "hive agent "))
		if len(words) == 0 || strings.HasPrefix(words[0], "-") {
			continue
		}
		args = map[string]string{}
		for k := 1; k < len(words); k++ {
			key, found := strings.CutPrefix(words[k], "--")
			if !found {
				continue
			}
			if key, val, eq := strings.Cut(key, "="); eq {
				args[key] = val
				continue
			}
			if k+1 < len(words) && !strings.HasPrefix(words[k+1], "--") {
				args[key] = words[k+1]
				k++
			} else {
				args[key] = ""
			}
		}
		return words[0], args, true
	}
	return "", nil, false
}

// shellWords splits one command line the way sh would for plain quoting:
// single and double quotes, backslash escapes, ending at an unquoted
// newline, `;`, `&`, `|` or closing backtick.
func shellWords(s string) []string {
	var words []string
	var cur strings.Builder
	in, quote := false, byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote == '\'':
			if c == '\'' {
				quote = 0
			} else {
				cur.WriteByte(c)
			}
		case quote == '"':
			switch {
			case c == '"':
				quote = 0
			case c == '\\' && i+1 < len(s) && strings.IndexByte(`"\$`+"`", s[i+1]) >= 0:
				i++
				cur.WriteByte(s[i])
			default:
				cur.WriteByte(c)
			}
		case c == '\'' || c == '"':
			quote, in = c, true
		case c == '\\' && i+1 < len(s):
			i++
			cur.WriteByte(s[i])
			in = true
		case c == ' ' || c == '\t':
			if in {
				words = append(words, cur.String())
				cur.Reset()
				in = false
			}
		case strings.IndexByte("\n\r;&|`", c) >= 0:
			if in {
				words = append(words, cur.String())
			}
			return words
		default:
			cur.WriteByte(c)
			in = true
		}
	}
	if in {
		words = append(words, cur.String())
	}
	return words
}

// check holds a finish to the rules the finish op holds it to.
func (f Finish) check() error {
	if f.Status != "done" && f.Status != "blocked" {
		return errors.New(`status must be "done" or "blocked"`)
	}
	if strings.TrimSpace(f.Account) == "" {
		return errors.New("account is required: say what you did, or why you stopped")
	}
	return nil
}

// printedNudge is the next prompt for a turn that ended without finishing:
// it says plainly when the answer held a command that was printed, not run.
func printedNudge(text string) string {
	op, args, ok := printedCommand(text)
	if !ok {
		return "You ended your turn without `hive agent finish`. Only a command you run reaches the Hive: writing it in your answer does nothing. If the work is done, commit, then run `hive agent finish --status done --account \"...\"` as a shell command (a tool call). If you can't finish, run it with status blocked and say why."
	}
	why := ""
	if op == "finish" {
		if err := (Finish{Status: args["status"], Account: args["account"]}).check(); err != nil {
			why = " (and as written it would fail: " + err.Error() + ")"
		}
	}
	return "Your last answer was the text of `hive agent " + op + " ...`" + why + ". It was printed, not run: nothing reached the Hive. Run it as a shell command with your bash tool (a tool call), then end your turn."
}

// printedFinish is the finish a successful turn's answer printed instead of
// running, when it would pass the finish op's checks. An account that only
// names the model or the role (as gpt-oss-120b's did) gives way to --tested,
// and the account says the Hive ran it, for the reviewer and the next agent.
func printedFinish(r Run, res *harness.Result) (Finish, bool) {
	if res == nil || res.IsError {
		return Finish{}, false
	}
	op, args, ok := printedCommand(res.Text)
	if !ok || op != "finish" {
		return Finish{}, false
	}
	f := Finish{Status: args["status"], Account: args["account"], Tested: args["tested"], Unsure: args["unsure"]}
	if a := strings.TrimSpace(f.Account); f.Tested != "" && slices.ContainsFunc([]string{r.Model, r.Role, HarnessName(r.Harness)}, func(n string) bool { return n != "" && strings.EqualFold(a, n) }) {
		f.Account = f.Tested
	}
	if f.check() != nil {
		return Finish{}, false
	}
	f.Unsure = strings.TrimSpace(f.Unsure + "\n(The agent printed this finish as its answer instead of running it; the Hive ran it for it.)")
	return f, true
}
