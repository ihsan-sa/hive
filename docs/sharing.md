# Sharing has three levels

The person listed "sharing-has-three-levels" under design work and gave no more detail. This is the reading built here. The open questions at the end are the person's to answer.

## What it does for the person

Every project shares at one of three levels. The person picks the level when the project is made (`create_project --sharing`):

| Level | Its agents read | Other projects' agents read | Reaching across (spawn, brief, message, list, status) |
|---|---|---|---|
| **shared** (default) | the whole Hive | all of it | allowed both ways |
| **private** | the whole Hive, apart from other hidden projects | nothing of it | refused both ways |
| **isolated** | only itself, the shared notes and the modules it was granted | nothing of it | refused both ways |

The spec offers two of these: "every agent reads all of it … Only a project the person isolates loses that view." Isolation walls a project off both ways. That suits code the person won't let other agents read, and work that shouldn't read anything else. Often the person only needs the first half. A client's code, or a job search, should stay out of other projects' sight, but the agents doing that work still gain from the Hive's records: how other projects solved a problem, the sweeps' findings, the Hive's own source. Private is that one-way wall.

Private still refuses outward reach. If its agents could file a brief or send a message into another project, they could carry what they read inside it to agents who are meant not to see it. The wall for actions therefore stays two-way, and only reading is one-way.

## How it fits

- **Project record.** The existing `isolated` flag stays, and a `private` flag joins it (`core.Project`). `Sharing()` names the level, and `Hidden()` (private or isolated) is the test wherever the question is "do others see or reach it". Projects recorded earlier read the same as before. `create_project` takes `"sharing"` and still accepts the old `"isolated": true`. A level it doesn't know is refused before anything is made on the forge. A hidden project's lead can't call it at all: a new project's name, description and goals land in the shared record and on the forge, so they would carry the hidden project's text out.
- **Sandbox.** Nothing new is mounted. `Runs.hideIsolated` now covers a private project's clone, runs, chats, its lead's working folder (`leads/project-<name>`), checks, gates, risk folder, scores and notes from every other run, the same way it covers an isolated project's. While any other project is hidden it also covers the whole journal and the record. A private project's own runs are not `Run.Isolated`, so they get `/hive/projects`, `/hive/state` and every module, the same as a shared project's.
- **Reach.** `Runs.Walled` refuses whenever either side is hidden. That covers spawn, message, agents, status, brief, recheck and module installs.
- **Knowledge base.** A private project's notes go in `kb/isolated/<name>/`, which stays out of git and out of the filer's reach. Search from any other run skips them. A project made private later has its old notes moved and the history rebuilt on the next start, as an isolated project's are.
- **Improvement.** The sweep digest leaves private projects out. A private project's step-back reads only its own records, and its fault goes to the person, never to the Hive's public issue tracker.
- **Forge.** Repositories the Hive makes are already private on GitHub, whatever the level.

## What it leaves out

- **Changing the level after creation.** No op or screen does it yet. The kb module's start-up move already handles a project hidden later, but loosening a level (isolated to shared) would show other agents everything the project did while hidden, so that is the person's call (question 2).
- **The control center.** The Projects and Settings screens don't show or set the level yet.
- **A level per agent or per record.** A level applies to a whole project. Splitting further would go against the spec's "the sandbox is at hive level mostly, not agent level".
- **Bare runs.** As with isolation, a run with no sandbox can't cover anything. The level is enforced only where a sandbox runs.

## Open questions for the person

1. **Is this the middle level you meant?** The build reads "three levels" as shared, private (one-way) and isolated. Another reading is a *read-only* project: everyone may read it, but no other project's agents may act on it (no briefs, spawns or messages). That would change only `Walled` and no sandbox mounts. A third reading is about sharing outward (the knowledge base or work shared with child Hives, friends' installs or the public). If either is what you meant, say which, and the private level can be dropped or kept as well.
2. **Can a level change later, and who changes it?** Tightening a level is safe. Loosening one exposes the project's history. Should loosening be yours alone, always?
3. **Should private projects show up by name?** For now other agents don't see a private project at all, not even in `status`. Should they at least know it exists, for example so the workspace lead can say "that's in a private project"?
