package work

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// mergedTries is how many runs a seat gets at a merged change before an
// unusable verdict each time leaves it to the person.
const mergedTries = 3

// ReadMerged gives a change already merged the read a release needs and found
// missing, with the same seats as the merge path: for root paths (what runs
// with elevated rights) the Hive's security seat and then its panel, else one
// ordinary reviewer. Each reads the merge's own diff, rebuilt from the host
// clone (head against its merge base with main before the merge) and taken
// only when it is the diff the merge record names. passed says the read the
// release needs is there; why, when not, says why only the person can settle
// it (a seat asked for fixes, gave no usable verdict, or the diff can't be
// rebuilt); neither means the seats are still reading. Each call records the
// seats that have ended and starts the ones still missing, so the release
// module calls it again until it says one or the other.
func (m *Module) ReadMerged(p core.Project, pr int, head, mergeSHA, diff string, root []string) (passed bool, why string) {
	ch := core.Change{Security: root, SecurityRoot: root, DiffID: diff}
	if diff == "" {
		return false, "its merge record names no diff, so no read can be bound to it"
	}
	if len(root) > 0 && m.C.Effects.SecurityOK(p.Name, ch) || len(root) == 0 && len(m.C.Effects.Reads(p.Name, head, diff)) > 0 {
		return true, ""
	}
	if o, ok := m.C.Effects.OutsideRead(p.Name, diff); ok && o.Verdict == "fix" {
		return false, "the box's " + o.By + " read asked for fixes: " + core.Clip(o.Note, 300)
	}
	text, err := m.mergedDiff(p, pr, head, mergeSHA)
	if err != nil {
		return false, err.Error()
	}
	ch.Diff = text
	if got := core.DiffID(text); got != diff {
		return false, fmt.Sprintf("its own diff rebuilt from the merge reads as %s, not the %s its merge record names", got, diff)
	}
	// Twice: the security seat's land, recorded on the first pass, seats
	// the panel on the second.
	for pass := 0; pass < 2; pass++ {
		seats, why := mergedSeats(m.C.Effects.SecurityReads(p.Name, diff), root)
		if why != "" {
			return false, why
		}
		for _, seat := range seats {
			if why := m.mergedSeat(p, pr, head, mergeSHA, ch, seat); why != "" {
				return false, why
			}
		}
		if len(root) > 0 && m.C.Effects.SecurityOK(p.Name, ch) || len(root) == 0 && len(m.C.Effects.Reads(p.Name, head, diff)) > 0 {
			return true, ""
		}
	}
	return false, ""
}

// mergedSeats are the seats still to read a merged change: one ordinary
// reviewer ("") for a change with no root path, else the security seat and
// then each panel seat with no verdict on the diff yet. why says a recorded
// read already failed it.
func mergedSeats(reads map[string]core.Verdict, root []string) (seats []string, why string) {
	if len(root) == 0 {
		return []string{""}, ""
	}
	v, ok := reads[core.SeatSecurity]
	switch {
	case !ok:
		return []string{core.SeatSecurity}, ""
	case v.Verdict == "fix":
		return nil, "the Hive's security seat asked for fixes: " + core.Clip(verdictText(v), 400)
	}
	for i := 0; i < core.PanelSeats; i++ {
		if _, ok := reads[core.PanelSeat(i)]; !ok {
			seats = append(seats, core.PanelSeat(i))
		}
	}
	if len(seats) == 0 { // all read, and SecurityOK found no majority
		_, fixes := core.PanelTally(reads)
		return nil, fmt.Sprintf("the Hive's security panel asked for fixes (%d of %d)", fixes, core.PanelSeats)
	}
	return seats, ""
}

// mergedDiff rebuilds a merged head's own diff in the host clone: head against
// its merge base with the merge's first parent (main just before it). A head
// gone from the clone is fetched from the PR's ref on the forge.
func (m *Module) mergedDiff(p core.Project, pr int, head, mergeSHA string) (string, error) {
	dir := m.C.WS.Project(p.Name)
	if _, err := core.Git(dir, "cat-file", "-e", head+"^{commit}"); err != nil {
		ref := "refs/hive/merged/" + strconv.Itoa(pr)
		if _, err := core.GitEnvT(5*time.Minute, dir, core.ForgeEnv(p.Repo), "fetch", "-q", "--no-tags", "--no-write-fetch-head", "origin", "+refs/pull/"+strconv.Itoa(pr)+"/head:"+ref); err != nil {
			return "", fmt.Errorf("its head %s isn't in the host clone and can't be fetched: %s", core.Short(head), core.Clip(err.Error(), 200))
		}
		if _, err := core.Git(dir, "cat-file", "-e", head+"^{commit}"); err != nil {
			return "", fmt.Errorf("its head %s isn't in the host clone or on the PR's ref", core.Short(head))
		}
	}
	base, err := core.Git(dir, "merge-base", mergeSHA+"^1", head)
	if err != nil || base == "" {
		return "", fmt.Errorf("can't find where %s left main before %s", core.Short(head), core.Short(mergeSHA))
	}
	text := core.OwnDiff(dir, base, head)
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("the own diff of %s came out empty", core.Short(head))
	}
	return text, nil
}

// mergedSeat starts seat's read-only run on the merged head, or records the
// verdict of one that ended. A run that ended with no usable verdict gets the
// next try; why is set once the seat asked for fixes or used every try.
func (m *Module) mergedSeat(p core.Project, pr int, head, mergeSHA string, ch core.Change, seat string) (why string) {
	name := seat
	if name == "" {
		name = "read"
	}
	who := "the reviewer"
	if seat != "" {
		who = "the security seat " + seat
	}
	for try := 0; try < mergedTries; try++ {
		id := fmt.Sprintf("m-%s-%d-%s-%s-%d", p.Name, pr, core.Short(ch.DiffID), name, try)
		r, ok := m.C.Runs.Get(id)
		if !ok {
			if err := m.startMerged(p, pr, head, mergeSHA, ch, seat, id); err != nil {
				return "can't start " + who + "'s read: " + core.Clip(err.Error(), 200)
			}
			return ""
		}
		if !r.Terminal() {
			return ""
		}
		var v core.Verdict
		var tok struct{ Token string }
		json.Unmarshal(r.Structured, &tok)
		if err := json.Unmarshal(r.Structured, &v); err != nil || (v.Verdict != "land" && v.Verdict != "fix") || (v.Verdict == "land" && tok.Token != reviewToken(r.ID)) {
			continue
		}
		// Recorded once: an ordinary verdict on the head, a seat's on the diff.
		got, ok := m.C.Effects.GetVerdict(p.Name, head)
		if seat != "" {
			got, ok = m.C.Effects.SecurityReads(p.Name, ch.DiffID)[seat]
		}
		if !ok || got.Run != id {
			v.Project, v.SHA, v.Run = p.Name, head, id
			if err := m.C.Effects.RecordVerdict(v); err != nil {
				return "can't record " + who + "'s verdict: " + core.Clip(err.Error(), 200)
			}
			m.C.Effects.RemoveCheckout(p.Name, id)
		}
		// A panel seat's fix is one vote: the tally decides once every seat
		// has read (mergedSeats, SecurityOK).
		if v.Verdict == "fix" && !strings.HasPrefix(seat, "panel-") {
			return who + " asked for fixes on the merged #" + strconv.Itoa(pr) + ": " + core.Clip(verdictText(v), 400)
		}
		return ""
	}
	return who + " gave no usable verdict on the merged #" + strconv.Itoa(pr) + " " + strconv.Itoa(mergedTries) + " times"
}

// startMerged starts a read-only reviewer on a merged head: a security seat
// briefed as on the merge path, or the ordinary reviewer.
func (m *Module) startMerged(p core.Project, pr int, head, mergeSHA string, ch core.Change, seat, id string) error {
	dir, err := m.C.Effects.NewCheckout(p, id, head)
	if err != nil {
		return err
	}
	main, _ := core.Git(m.C.WS.Project(p.Name), "rev-parse", "--verify", "-q", mergeSHA+"^1")
	subject, _ := core.Git(m.C.WS.Project(p.Name), "log", "-1", "--format=%B", mergeSHA)
	account := fmt.Sprintf("## The change\n\nThis change already merged as #%d (%s) and is in the next release. It merged without the read a release needs, so the Hive reads it now, before the release goes out; a fix holds the release for the person. The merge's message:\n\n%s", pr, core.Short(mergeSHA), core.Clip(subject, 4000))
	brief := fmt.Sprintf("Review of the merged #%d at %s, for a release.\n\nThe brief, the exact diff and the test run are in $HIVE_RUN (brief.md, diff.patch, tests.txt). Give your verdict.", pr, core.Short(head))
	rules := ""
	if seat != "" {
		brief = fmt.Sprintf("Security read (%s) of the merged #%d at %s, for a release.\n\nThe brief, the exact diff and the test run are in $HIVE_RUN (brief.md, diff.patch, tests.txt). Give your verdict.", seat, pr, core.Short(head))
		rules = securityRules(ch) + "\n\n"
	}
	spec := m.Agents.Spec(id, "reviewer", p.Name, dir, brief)
	if seat != "" {
		h, model, route := m.seatModel(p, seat)
		spec.Harness, spec.Model, spec.Route = h, model, route
	}
	spec.ReadOnly, spec.Schema = true, VerdictSchema
	spec.Inputs = map[string]string{"sha": head, "diff.patch": ch.Diff, "tests.txt": m.testsText(p, head, main),
		"brief.md": "Review token: " + reviewToken(id) + "\n\n" + rules + account + m.reviewRules(p, main)}
	if seat != "" {
		spec.Inputs["seat"] = seat
	}
	_, err = m.C.Runs.Start(spec)
	return err
}
