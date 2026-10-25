# Priority: leads answer however busy the machine is

A person's message is answered whatever the machine is doing (issue #421). This is how the core keeps it so.

## A lead's turn never waits for strain

Before a turn starts, `Runs.launch` reads Linux's `MemAvailable` and its memory and CPU pressure (`core.Strained`; `mem_floor_mb`, `mem_pressure_max`, `cpu_pressure_max` in `.hive/config.json`) and holds the run while any is past its limit. A lead (the Hive-wide lead and each project's; `Run.Lead`) is held by none of them. Its one hold is an absolute floor of 256 MB available (`core.LeadMemFloorMB`), under which its harness could not run at all; that hold is journalled once as `run.held`, as every hold is, so the control center says why. `load_max` only ever held workers (the work module's queue) and is unchanged.

## Leads step first

`Runs.Tick` steps the leads first, the Hive-wide lead ahead of the projects' (sorted by `Run.Nice`), each on its own goroutine, and steps the other runs only once every lead's step is done, waiting at most 5 seconds for that on a goroutine of its own, never on the daemon loop; a lead's step still going at 5 seconds is named in `daemon.log`. One such pass waits at a time: ticks that come while it waits step their leads and skip the pass, rather than pile up a waiting goroutine each. The other runs are listed after the wait, as they are then, and each is claimed as it is now (`Runs.claim`), so a tick that overlaps a slow lead step never steps a run from an older view. While a lead's launch is in flight (`Runs.leadLaunching`), a launch of any other role returns at once and is due again next tick, so a lead's turn has its process before a queued worker's or review's starts; a run deferred ten ticks in a row is named in `daemon.log`.

## Leads run at a higher priority

Every sandboxed command carries a nice value (`sandbox.Spec.Nice`, from `Run.Nice`): the Hive-wide lead 0, a project's lead 2, every other run 10, and checks and the recursive gate (`hive gate`, which the daemon starts itself) 15 (`core.CheckNice`). A process can only lower its own priority, so each level adds to the daemon's own and is capped at 19 (`sandbox.Lowered`). The bare and Seatbelt sandboxes and the gate prefix `nice -n` through `sandbox.Niced`, which resolves both `nice` and the command on the daemon's own PATH, never on the command's (which carries the tools bins an agent can write to). The Linux launcher (`hive sandbox-exec`) reads its own level and sets the sum with `setpriority` on a thread its goroutine is locked to, since Linux nices a thread and the sandbox is forked from that goroutine later; the init, bubblewrap and the command inherit it, under WSL too.

## Tests

`TestLeadStartsUnderStrainThatHoldsWorker`, `TestTickStartsLeadsFirst`, `TestWorkerLaunchWaitsForLeadLaunch`, `TestOverlappingTicksStepEachRunOnce`, `TestNiceByRole` (internal/core); `TestNicedPrefixesArgv`, `TestNicedResolvesInTheDaemonsPath`, `TestLoweredAddsAndCaps`, `TestLowerPriorityHoldsForTheFork` (internal/sandbox); `TestRunGateReadsTheVerdict` (internal/app, the gate's level under a niced daemon too); `TestLeadTurnStartsUnderStrainThatHoldsAWorker` (internal/daemon, a real memory hold, built only from APIs main had, so the gate's fixes step could run it on both sides).
