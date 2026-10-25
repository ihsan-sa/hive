# Modules: writing one, listing it, installing it

A module is a folder with a `module.json` at its root. It can hold prompts, procedures and skills that agents read, and it can carry a program that the daemon runs (see [architecture.md](architecture.md), "Modules that run a program"). The standard modules (agents, chat, improve, kb, system) ship inside the Hive under `modules/` and are installed from the binary. Every other module lives in a repository of its own and installs from a **marketplace index**.

The code is `internal/mod/modules/market.go` (index, plan, fetch, install, update, remove) and, for who says yes, `internal/mod/risk` (`Install`, `UpdateModules`).

## Writing a module

```json
{
  "name": "slack",
  "description": "The lead's conversation in a Slack channel.",
  "exec": ["./slack"],
  "hooks": ["channel"],
  "hosts": ["slack.com", "wss-primary.slack.com"],
  "credentials": [],
  "provides": {"schedules": [], "ops": []},
  "projects": ["*"]
}
```

- `name`: lowercase letters, digits and dashes. It is the module's folder in `.modules/`.
- `exec` and `hooks`: the program and the slices of the control API it calls (`source`, `channel`, `after`, `status`, `idle`). The token it gets reaches only those, and anything else gets 403.
- `hosts`: every network host the program reaches. `credentials`: any credential it uses, only ever through the Hive, never handed to it.
- `provides.schedules` and `provides.ops`: schedules and agent ops it adds.

Together these are its **reach**. Declare all of it. A module asking for anything its listing doesn't declare is refused at install, and its program won't start if its `module.json` later asks for more than it was installed with.

A module can sit at the root of its repository or in a folder of a repository holding several (`path` in the listing). Only that folder is installed, without `.git`, at exactly the pinned commit. Symlinks are not copied.

## Listing it

An index is a git repository (or, for your own, any folder) with `index.json` at its root:

```json
{
  "modules": [
    {
      "name": "slack",
      "description": "The lead's conversation in a Slack channel.",
      "repo": "https://github.com/someone/hive-slack",
      "path": "",
      "sha": "3f9c0e1d2b4a5968776655443322110fedcba987",
      "trust": "reviewed",
      "read": "Read at 3f9c0e1 by the Hive's security seat on 2026-10-24: posts and reads one channel through slack.com, keeps its token in HOME, nothing else.",
      "needs": {"program": true, "hooks": ["channel"], "hosts": ["slack.com", "wss-primary.slack.com"]}
    }
  ]
}
```

- `repo` is an `https://` URL. Only an index kept in a folder on this machine (your own) may list a `file://` URL or an absolute path. ssh URLs aren't accepted, since they would use the person's keys. Listings that break these rules are dropped, and the reason is shown as the index's error.
- `sha` is the full 40-character commit. An install checks out exactly that commit and refuses if the repository doesn't have it.
- `trust` is `core-team` (the Hive's own, Hive-RSI-Lab) or `reviewed` (listed after the Hive's security read, plus a tribunal for anything that runs a program or asks for a credential). Anything else counts as `unlisted`. Only the Hive's own index (`https://github.com/Hive-RSI-Lab/marketplace`) can say `core-team`. Another index's `core-team` reads as `reviewed`.
- `read` is the read behind that trust: who read it, at which commit, and what they found. The person and the warden see it before saying yes.
- `needs` is the reach the listing grants, in the same fields as `module.json` (`program`, `hooks`, `hosts`, `schedules`, `ops`, `credentials`).
- `pulled`, if set, takes the listing down and says why. It no longer installs, and the update job notes it for any Hive that has it.

To list a module in the Hive's index, open a pull request to Hive-RSI-Lab/marketplace adding its entry. It is reviewed like any change. A new version is a pull request that moves `sha` and updates `read` (and `needs`, if its reach grew). You can also run an index of your own, and anyone who wants it adds it.

## Installing

Three ways, one path:

- Ask the lead: "install the slack module". The lead calls `install_module --name slack`. A name that is one of the Hive's own projects installs from that project.
- Go to the control center's **Modules** tab. Below the installed modules, `[+]` rows are what the indexes list. Press enter on one to see its manifest, trust and read, then `i` to install.
- Run `hive module add slack`, or `hive module add slack@<sha>` for another commit, or `hive module add <git URL>[//folder][@ref]`.

An install fetches the commit as nobody in particular (no git config, credential helper or `.netrc` of the person's; https and file only), checks it against the listing, and shows what the module declares. One exception: when an agent names a path on this machine, nothing is read from it before the person's yes, because it could be any of their repositories. A name that is one of the Hive's own projects means that project, whoever asks. Then it is routed by trust:

| trust | who says yes |
|---|---|
| core-team, reviewed | a warden reads it, the way autonomy settles any step (the person at autonomy 1), and the person is told after |
| the Hive's own project | the same |
| unlisted (a git URL, or a commit other than the pin) | the person, whatever the autonomy |

`hive module` lists what's installed with its trust. `hive module browse` shows what the indexes list. `hive module index add <url>` adds an index and `hive module index rm <url>` drops one. With none configured, the Hive reads its own.

## Updates

The `modules.update` job (at start, then on its schedule) looks for a newer commit of each installed module:

- A **listed** module updates when its index pins a new `sha`. A warden reads the diff from the installed commit (the person at autonomy 1), and on its yes the module is replaced whole and keeps its grants.
- An **unlisted** module's newer release (its newest `vX.Y.Z` tag, else its HEAD) goes to the person with the diff.
- A module built in one of the Hive's own projects follows that project's main, since its merges were already reviewed.

If an update is declined (`skip`), that commit isn't offered again. The ask also names any reach the new commit adds. A program at a new commit is read again by the module review before it runs.

## Removing

`hive module rm <name>`, `x` on the module in the Modules tab, or ask the lead (`remove_module`, which asks the person). The module's folder and its program's home (`.hive/modules/<name>`) go, and its program stops on the next tick. A standard module stays. To turn one off, take it away from every project.

## What isn't enforced yet

Declared reach is checked at install and before the program starts, and the program's API token reaches only its declared hooks. The program itself doesn't run in the OS sandbox yet, so its declared `hosts` aren't enforced at the network. That is task 261024-174301-2f7050eb10 (issue #444), which reads the reach recorded here (`.hive-source.json` `needs`).
