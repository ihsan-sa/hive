# The Hive's repo guards its root paths

Design for `hive-repo-guards-its-root-paths` (issue #141).

## What it does for the person

Spec section 10 lets a project's settings class paths as **high** (a change needs a second, separate read) or **root** (it runs with elevated rights: high, and the release waits for the person). The gate already did this, but the Hive's own `.hive/project.json` names no classes, so a change to the sandbox or to the code that runs approved scripts on the person's machine got the same single read as a TUI tweak, and released with nobody asked.

Now a self project (`Project.Self`, the Hive's own source) always guards a built-in set of paths, on top of whatever its project.json adds:

| class | paths | why |
|---|---|---|
| root | `internal/sandbox/` | the box itself: what keeps agents off the person's machine |
| root | `internal/mod/risk/` | runs warden- and tribunal-approved scripts in the daemon, outside the box, with `sudo` where it works |
| root | `internal/setup/`, `install/` | install, autostart units, PATH changes on the person's account |
| high | `internal/core/effects.go`, `internal/core/forge.go` | hold the forge login and enforce the gate |
| high | `internal/harness/` | hands harness logins to runs |
| high | `internal/mod/release/`, `internal/daemon/` | cut releases and replace the running binary (self-update) |
| high | `modules/agents/warden.md`, `modules/agents/tribunal.md` | the prompts that decide what runs outside the box |

A change touching a root path needs two separate land reads of its exact commit to merge, and the release that carries it holds for the person's "release". A change touching a high path needs the two reads and releases on its own. The person sees the same holds and asks they already do; nothing new to learn.

## How it fits

- The lists live in the binary (`core.SelfRoot`, `core.SelfHigh`) and are joined in `Effects.Classify` through `ProjectConfig.Guarded`, so the running gate holds them, not the branch under review. A PR can't drop them, the same way checks come from main's project.json.
- Classify reads diffs with `--no-renames`, so moving a file out of a guarded path shows as a change to it.
- Nothing else changes: `readOK`, `effect.merged`'s `root` and the release hold already worked from `Change.High`/`Change.Root`.
- Other projects are untouched; only `Self` projects get the defaults.

## What it leaves out

- **New guarded code elsewhere.** A new package that, say, execs outside the box isn't caught until someone adds it to the list. The reviewer still reads it; the list is a floor, not a fence.
- **Opting out.** The person can add classes in project.json but can't remove a built-in one there. If they want that, it's a settings field to add later.
- **The perms-seat read.** The goals' rule (sandbox, self-update and login changes get one read from the box's perms seat or architect, routed by autobox) stays a PR note, not a gate rule; this doesn't route anything to autobox.
- **Changing the lists** is itself a change to `internal/core/projects.go`, which isn't guarded. Guarding the core whole would put most changes behind two reads.

## Open questions for the person

1. Should self-update (`internal/mod/release/`, `internal/daemon/`) and logins (`internal/harness/`, `internal/core/forge.go`) be **root** rather than high? Root waits for you at every release that carries one, which works against the first milestone's "nobody in between"; high doesn't.
2. Should the defaults go into `.hive/project.json` as well, so they're visible in settings? That file is yours to merge.
3. Should `internal/core/` as a whole be high, given the spec calls a core change T2?
