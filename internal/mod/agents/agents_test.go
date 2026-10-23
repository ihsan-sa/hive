package agents

import (
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/modules"
)

// In the first real run a lead wrote "no python on the machine" into three
// briefs from one exit 127, and it was wrong: the lead's prompt says a claim
// about the machine comes from a check, or is marked unchecked.
func TestLeadChecksMachineClaims(t *testing.T) {
	ws := core.Workspace{Root: t.TempDir()}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	c, err := core.Open(ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	mods := modules.New(ws, c.J)
	if err := mods.InstallDefaults(); err != nil {
		t.Fatal(err)
	}
	s := (&Agents{C: c, Mod: mods}).Spec("l", "lead", "", t.TempDir(), "")
	if !strings.Contains(s.System, "from a check you ran") || !strings.Contains(s.System, "marked unchecked") {
		t.Fatal("the lead's prompt doesn't ask for machine claims to be checked or marked")
	}
}
