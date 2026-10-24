package improve

import (
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// A start that fails twice at one commit reaches the project's lead as work,
// once, with the command's output; the first failure is only an alert.
func TestRepeatedBaselineAlertReachesTheLead(t *testing.T) {
	m := rig(t)
	p, _ := m.C.Projects.Get("hive")
	ms := Measurement{Project: "hive", SHA: "abc1234", TestsOK: true, Note: "start: score did not produce a number: 59.93\nrun 1: 59.34 tokens/s"}
	leads := func() int {
		return len(m.C.Runs.List(func(r *core.Run) bool { return r.Project == "hive" && strings.HasPrefix(r.Role, "lead") }))
	}
	m.noStart(p, ms)
	if leads() != 0 {
		t.Fatal("the first failed start went to the lead")
	}
	m.noStart(p, ms)
	m.noStart(p, ms)
	if leads() != 1 {
		t.Fatalf("a repeated failed start started %d leads, want 1", leads())
	}
	told := 0
	for _, msg := range m.Chat.Messages("project:hive") {
		if msg.From == "improve" && strings.Contains(msg.Text, "run 1: 59.34 tokens/s") && strings.Contains(msg.Text, "score command") {
			told++
		}
	}
	if told != 1 {
		t.Fatalf("the lead was told %d times, want once with the output", told)
	}
}

// A journal that already holds several failed starts at main's commit (from
// before an upgrade or a restart) still reaches the lead, once.
func TestEarlierBaselineFailuresStillReachTheLead(t *testing.T) {
	m := rig(t)
	p, _ := m.C.Projects.Get("hive")
	ms := Measurement{Project: "hive", SHA: "abc1234", TestsOK: true, Note: "start: score did not produce a number: 59.93\nrun 1: 59.34 tokens/s"}
	for range 3 {
		m.record(ms)
	}
	m.noStart(p, ms)
	m.noStart(p, ms)
	if n := len(m.C.Runs.List(func(r *core.Run) bool { return r.Project == "hive" && strings.HasPrefix(r.Role, "lead") })); n != 1 {
		t.Fatalf("earlier failed starts started %d leads, want 1", n)
	}
}
