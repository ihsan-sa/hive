package work

import "github.com/ihsan-sa/hive-standalone-build/internal/mod/calls"

// The work module's cadences, caps and counts are calls, not rules (#1940):
// each variable named is the default, and any agent that steers changes it
// at once with `hive agent call` when the work needs another. Those marked
// Bar decide whether a change counts, so a worker doesn't make them.
var (
	callStepLimit     = calls.Duration("work.step_limit", "how long one task's step (a test, a merge, a forge call) may run before it is let go and stepped again", &stepLimit)
	callKillCap       = calls.Count("work.kills_before_stop", "how many of a task's steps in a row may be killed by their timeout before it stops and the lead is told", &killCap)
	CallCantRunTries  = calls.Count("work.cant_run_tries", "how many tries in a row a change's tests may fail to start (a missing tool) before that counts as red and the person is asked for the tool", &CantRunTries).Bar()
	callReruns        = calls.Count("work.reruns_per_head", "how many times a PR head's failed forge checks are run again, by the Hive or with rerun-checks, before they count", &rerunsPerHead).Bar()
	callMainWaitMax   = calls.Duration("work.main_checks_wait", "how long a change waits on main's forge checks finishing before it goes on without them", &mainWaitMax)
	callNoChecksMax   = calls.Duration("work.no_checks_wait", "how long a pushed head waits for any forge check to appear before the Hive acts on it", &noChecksMax)
	CallJudgeTries    = calls.Count("work.judge_tries", "how many judge runs a verdict gets before the gate's numbers decide alone", &JudgeTries).Bar()
	callJudgeWait     = calls.Duration("work.judge_wait", "how long one judge run may take before it is stopped and another is asked", &JudgeWait)
	callJudgeParkWait = calls.Duration("work.judge_park_wait", "how long a judge parked on usage holds a clear keep before the numbers decide", &JudgeParkWait)
	callGateTimeout   = calls.Duration("work.gate_timeout", "how long one recursive gate run may take", &GateTimeout)
	callDraftTell     = calls.Duration("work.draft_tell", "how long a PR stays a draft before the lead is told", &draftTell)
	callKeptFirstFor  = calls.Duration("work.kept_first_for", "how long a gate's measured keep holds other merges that would unseat it, so it merges first", &keptFirstFor).Bar()
)
