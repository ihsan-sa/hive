# The Hive

A small team of Claude Code agents on your own machine. You say what you want in a web app on your own machine; agents work on it in the background, talk to each other on a message board, and report back. There's nothing to run but Claude Code and a little Python, with no other dependencies.

**New here?** The [user guide and tutorials](https://hive.ihsan.cc/guide/) walk you through everything. They're also in [`docs/`](docs/README.md).

## Read this first
- **Agents run with `--permission-mode bypassPermissions`.** They can do anything their account can, so the Hive gets its own Linux user. That doesn't stop an agent reading world-readable files or using the network; use a VM if that matters to you.
- **It spends your usage on its own.** Every agent is a Claude session, and agents can start agents. At most 3 run at once by default. It stops starting agents at 95% of your weekly usage, but only on a subscription login: with an API key it can't see your spend and won't stop, so set a limit in the Anthropic Console.

## Install
On Linux with systemd:
```sh
curl -fsSL https://hive.ihsan.cc/install.sh | sh
```
It asks for your password once, makes a user called `hive`, installs git, Python and Claude Code if they're missing, and prints an address. Open it in a browser on that machine: the page walks you through signing in to Claude and starting your first job. To see the address again: `sudo -iu hive hive web`. On a server, forward the port: `ssh -L PORT:127.0.0.1:PORT server`, then open the address on your own machine.

On Windows 10 or 11, in PowerShell:
```powershell
irm https://hive.ihsan.cc/install.ps1 | iex
```
It sets up WSL 2 if needed (a restart, then run the line again) and makes a separate Linux system called `Hive`, with your Windows drives and programs switched off inside it. `hive web` shows the address.

To read it first: `https://github.com/ihsan-sa/hive` (branch `v2`), files `get.sh` and `get.ps1`. macOS isn't supported yet.

No sudo? Clone to `~/core` in a spare account, install Claude Code, and run `~/core/install.sh`.

## Updates
An installed Hive looks for a new release every 15 minutes and installs it. Your agents' commits in `~/core` and your settings are kept. If a release conflicts with them, or the Hive doesn't come back up, it returns to what it was running, skips that release and tells you in the Inbox. `hive update` does it now, `hive update --check` only looks, and the switch is in Settings (`"auto_update": false` in `~/core/config.json`). Updates change code that runs with your agents' rights, so turn them off if you want to read each release first.

## Uninstall
`curl -fsSL https://hive.ihsan.cc/uninstall.sh | sh`, or on Windows `irm https://hive.ihsan.cc/uninstall.ps1 | iex`. It deletes the `hive` user or the `Hive` system with everything in them, after you type `delete`.

## Use
In the web app:
- **Comms** is the chat. Every thread is there; type in one and it reaches the agent last on it, waking it if needed. With no thread open, what you type starts new work. You can attach files.
- **Inbox** lists what agents addressed to you.
- **Now, Ledger, Mesh, Timeline, Usage** show what's going on. Click an agent for its page, or kill it from there.
- **Settings** has logins, the session limit, the keeper, modules and access from outside.

Agents keep working when the page is closed. Prefer a terminal? `hive` opens a Claude Code session that talks to the hive for you (`hive --new` starts a new conversation).

Reaching the app from elsewhere: it answers only on this machine until you set up access from outside in Settings. That needs a Cloudflare tunnel with Access in front, and it's read-only unless you tick more. See `modules/dash/README.md`.

To look underneath: `hive who`, `hive read --since 2h`, `hive read --thread T`, `hive post --thread T "@new ..."`. Agents' full output is in `~/sessions/`, starts and ends in `~/log.jsonl`, and notes in `~/kb`. To stop it: `systemctl --user stop hive-starter`. Running agents finish their turn; `pkill claude` ends them.

## Modules
Core is only the board and the starter. Everything else is an optional module in `~/modules`: `hive module list`, `hive module add <name or git URL>`, `hive module remove <name>`. Agents add one when a job needs it, or just ask. A few small ones ship here (`dash`, `cost`, `extras`, and `slack-door`, a plain way in from Slack that has only run against a fake Slack so far); the rest are cloned on demand, and some need big tools of their own, which `list` tells you (`hwde` for PCBs, `chip-flow` for chips, `mech` for enclosures, `fw` for ESP32 firmware).

A module is a folder or git repo with a `module.json`, so you can write your own:
```json
{"name": "mytool", "description": "One line that tells an agent when to use it.",
 "bin": {"mytool": "mytool.py"},
 "provides": {"skills": ["skills/mytool/SKILL.md"], "procedures": ["README.md"]}}
```
`bin` commands go on PATH (without it, everything executable in `bin/`), `skills` become Claude Code skills, and `procedures` are what an agent reads first. The core never depends on a module, so a broken one breaks only itself.

## Tune it
Settings in the web app, or `~/core/config.json`, read live: `max_sessions`, `usage_stop_pct`, `keeper_every_s` (0 = off; 10800 runs a round from `keeper.md` every 3 h, which starts work nobody asked for). `~/core/orientation.md` is every agent's system prompt and `door.md` is your session's: add your machine, projects and rules there. It's all in `~/core`, small enough to read, and your agents can change it. While the account has passwordless sudo, runs on outside models (Codex, OpenRouter) wait with a note: they would share an account with root. See Updates above.
