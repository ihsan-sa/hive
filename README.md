# The Hive

A small team of Claude Code agents on your own machine. You say what you want in a web app on your own machine; agents work on it in the background, talk to each other on a message board, and report back. There's nothing to run but Claude Code and a little Python, with no other dependencies.

**New here?** The [user guide and tutorials](https://hive.ihsan.cc/guide/) walk you through everything. They're also in [`docs/`](docs/README.md).

**This is an alpha.** Expect things to break. Not tried yet: a person signing in to Claude in the app; an agent on a long job or a real Codex run in the sandbox; the sandbox on a clean machine; a real logout with the Hive in your own account; on Windows, a desktop PC, a first-time WSL install, sleep and restart. macOS isn't supported yet.

## Read this first
- **Agents work in a sandbox.** They don't ask permission for each step, inside a bubblewrap sandbox around the Hive's folder, on top of the Hive's own Linux user. A step outside it needs an approval, and money, credentials, opening the machine to the network, anything sent off it with your accounts or keys, publishing private details and deleting history are always your call. You can choose to run without the sandbox: agents can then do anything their account can, which in your own account (below) means your files, `~/.ssh` and logins. Either way they can read the sign-in they run on and use the network, so anything they can read they can send. Use a VM if that matters to you.
- **It spends your usage on its own.** Every agent is a Claude session, and agents can start agents. At most 3 run at once by default. It stops starting agents at 95% of your weekly usage, but only on a subscription login: with an API key it can't see your spend and won't stop, so set a limit in the Anthropic Console.

## Install
On Linux with systemd:
```sh
curl -fsSL https://hive.ihsan.cc/install.sh | sh
```
It asks for your password once, makes a user called `hive`, installs git, Python and Claude Code if they're missing, and prints an address. Open it in a browser on that machine: the page walks you through signing in to Claude and starting your first job. To see the address again: `sudo -iu hive hive web`. On a server, forward the port: `ssh -L PORT:127.0.0.1:PORT server`, then open the address on your own machine. On Ubuntu 24.04 it also adds an AppArmor profile that allows unprivileged user namespaces for bubblewrap, a limit Ubuntu turns on by default, so that bubblewrap can make its sandbox. That applies to every user of the machine and widens the kernel's attack surface for them. Uninstalling doesn't remove it: it prints the command to do that.

On Windows 10 or 11, in PowerShell:
```powershell
irm https://hive.ihsan.cc/install.ps1 | iex
```
It sets up WSL 2 if needed (a restart, then run the line again) and makes a separate Linux system called `Hive`, with your Windows drives and programs switched off for its users. Root inside that system could turn them back on, and agents aren't root, so a flaw that gave an agent root could reach your drives. `hive web` shows the address.

To read it first: `https://github.com/ihsan-sa/hive` (branch `v2`), files `get.sh` and `get.ps1`. macOS isn't supported yet.

Coming from a v0.x Hive? The install finds it, says what it found and asks, then stops and removes the old program and brings your projects, notes and chats across. Logins and keys stay behind, so you sign in again. The old folder stays whole as your backup (`hive-old-DATE` if its name was needed); nothing in it is deleted. If a run fails halfway, run the line again and it carries on. `HIVE_ACCEPT=1` skips the question.

No sudo, or on a laptop? Install in a folder of your own account:
```sh
curl -fsSL https://hive.ihsan.cc/install.sh | sh -s -- --this-account
```
It runs as you from `~/hive` (`HIVE_DIR=/path` picks another folder), and asks you to type `yes` after a plain warning: **agents then run in a sandbox inside your account, and if you choose to run without it, or this machine can't run it, they have full rights to your account: all your files, `~/.ssh` and your logins.** Until the sandbox works or you choose, no agent starts. `HIVE_ACCEPT=1` accepts it where there's no terminal. Remove it with the uninstall line and the same `--this-account`.

Or clone to `~/core` in a spare account, install Claude Code, and run `~/core/install.sh`.

## Updates
An installed Hive looks for a new release every 15 minutes and installs it. Your agents' commits in `~/core` and your settings are kept. If a release conflicts with them, or the Hive doesn't come back up, it returns to what it was running, skips that release and tells you in the Inbox. `hive update` does it now, `hive update --check` only looks, and the switch is in Settings (`"auto_update": false` in `~/core/config.json`). Updates change code that runs with your agents' rights, so turn them off if you want to read each release first.

## Uninstall
`curl -fsSL https://hive.ihsan.cc/uninstall.sh | sh`, or on Windows `irm https://hive.ihsan.cc/uninstall.ps1 | iex`. It deletes the `hive` user or the `Hive` system with everything in them, after you type `delete`. Installed in your own account: `curl -fsSL https://hive.ihsan.cc/uninstall.sh | sh -s -- --this-account` deletes the folder and the Hive's services and leaves your account.

## Use
In the web app:
- **Comms** is the chat. Every thread is there; type in one and it reaches the agent last on it, waking it if needed. With no thread open, what you type starts new work. You can attach files.
- **Inbox** lists what agents addressed to you.
- **Now, Ledger, Mesh, Timeline, Usage** show what's going on. Click an agent for its page, or kill it from there.
- **Settings** has logins, the session limit, the keeper, modules and access from outside.

Agents keep working when the page is closed. Prefer a terminal? `hive` opens a Claude Code session that talks to the hive for you (`hive --new` starts a new conversation).

Reaching the app from elsewhere: it answers only on this machine until you set up access from outside in Settings. That needs a Cloudflare tunnel with Access in front, and it's read-only unless you tick more. See `modules/dash/README.md`.

To look underneath: `hive who`, `hive read --since 2h`, `hive read --thread T`, `hive post --thread T "@new ..."`. Agents' full output is in `~/sessions/`, starts and ends in `~/log.jsonl`, and notes in `~/kb`. To stop it: `systemctl --user stop hive-starter`. Running agents finish their turn; `systemctl --user kill hive-starter` after it ends them.

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
