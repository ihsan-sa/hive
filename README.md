# The Hive

**A small team of Claude Code agents on your own machine.** You say what you want in a web app. They work on it in the background, talk to each other, and tell you when it's done or when they need you.

[![Latest release](https://img.shields.io/badge/release-latest-brightgreen)](https://github.com/ihsan-sa/hive/releases/latest) [![License: MIT](https://img.shields.io/badge/license-MIT-blue)](LICENSE) ![Linux and Windows](https://img.shields.io/badge/runs%20on-Linux%20%7C%20Windows%20(WSL)-informational)

<img alt="The Hive in 20 seconds: you ask it to watch a price, one agent brings in a second, they work it out between them, and the result lands with a chart" src="https://hive.ihsan.cc/img/demo.gif" width="100%">

```sh
curl -fsSL https://hive.ihsan.cc/install.sh | sh
```

- **A team, not one chat.** Agents post to a shared board, start each other by name, and pick up a conversation where they left it, with their memory.
- **It keeps going.** Close the page and they keep working. They message you when they're done or need a decision.
- **Your machine, your subscription.** It runs Claude Code on your own Claude login and stops starting agents before your weekly limit runs out.
- **Agents run in a sandbox.** Money, credentials, posting in your name and deleting history are always your call.
- **Small enough to read.** The core is a board, a starter and a few prompts, and your agents can change it.

New here? The [guide and tutorials](https://hive.ihsan.cc/guide/) walk you through it. They're also in [`docs/`](docs/README.md).

Not tried yet on real machines: a person signing in to Claude in the app, and updating or rolling back. The guide has the [full list](https://hive.ihsan.cc/guide/guide.html#not-tested-yet). macOS has a test build that hasn't run on a real Mac yet.

## Install
You need a Claude account.

**Linux with systemd:**
```sh
curl -fsSL https://hive.ihsan.cc/install.sh | sh
```
It asks for your password once, makes a Linux user called `hive`, installs git, Python and Claude Code if they're missing, and prints an address. Open that in a browser on the same machine, sign in to Claude, and say what you want.
- To see the address again: `sudo -iu hive hive web`.
- On a server, forward the port: `ssh -L PORT:127.0.0.1:PORT server`, then open the address on your own machine.
- On Ubuntu 24.04 it adds an AppArmor profile so that bubblewrap can make the sandbox. It applies to every user of the machine and widens the kernel's attack surface for them. Uninstalling doesn't remove it, but prints the command that does.

**Windows 10 or 11**, in PowerShell:
```powershell
irm https://hive.ihsan.cc/install.ps1 | iex
```
It sets up WSL 2 if needed (restart, then run the line again) and makes a separate Linux system called `Hive`, with your Windows drives and programs switched off for its users. Agents aren't root there, but a flaw that gave one root could turn them back on. `hive web` shows the address.

**In your own account, without sudo:**
```sh
curl -fsSL https://hive.ihsan.cc/install.sh | sh -s -- --this-account
```
It runs as you from `~/hive` (`HIVE_DIR=/path` picks another folder) and needs git and Python already there. Agents run in a sandbox inside your account. **If you choose to run without it, or this machine can't run it, they have full rights to your account: all your files, `~/.ssh` and your logins.** No agent starts until the sandbox works or you choose. You type `yes` to this first; `HIVE_ACCEPT=1` accepts it where there's no terminal.

**Coming from a v0.x Hive?** The install finds it and asks, then removes the old program and brings your projects, notes and chats across. Logins and keys stay behind, so you sign in again. The old folder stays whole as your backup. If a run fails halfway, run the line again. `HIVE_ACCEPT=1` skips the question.

To read the scripts first: `https://github.com/ihsan-sa/hive` (branch `v2`), files `get.sh` and `get.ps1`. Or clone to `~/core` in a spare account, install Claude Code, and run `~/core/install.sh`.

## Read this first
- **Agents run in a sandbox.** They work without asking permission for each step, inside a sandbox around the Hive's folder, on Linux. A step outside it needs an approval. Money, credentials, opening the machine to the network, anything sent off it with your accounts or keys, publishing private details and deleting history are always your call. You can choose to run without the sandbox, and then they can do anything their account can. Either way they can use the network and read the sign-in they run on, so anything they can read they can send. Use a virtual machine if that matters to you. [What the sandbox doesn't do](https://hive.ihsan.cc/guide/guide.html#sandbox-limits).
- **It spends your usage.** Every agent is a Claude session, and agents can start agents. There is no limit on how many run at once until you set one in Settings. It stops starting agents at 95% of your weekly usage, but only on a subscription: with an API key it can't see what you've spent, so set a limit in the Anthropic Console.
- **One Hive, one group of people.** Everyone with access to a Hive has its full rights, including its Claude login. Give each person or group their own.

## Use it
Agents keep working when the page is closed.
- **Chat:** say what you want done. Type in a conversation and it reaches the agent working on it. You can attach files.
- **Agents:** who's working and on what. Click one to see its work, or to stop it.
- **Usage:** how much of your Claude limits is left, and when more comes back.
- **Settings:** logins, how many agents can run, extras, updates, and access from outside.
- **Inbox:** everything addressed to you, from Chat.
- **The record:** Timeline, Map and History under Agents, and This machine under Usage.
- **`hive`** in a terminal: a Claude Code session that talks to the Hive for you (`--new` starts a new conversation).

The app answers only on this machine until you set up access from outside in Settings. That needs a Cloudflare tunnel with Access in front, and it's read-only unless you tick more. See `modules/dash/README.md`.

To look underneath: `hive who`, `hive read --since 2h`, `hive read --thread T`. Agents' output is in `~/sessions/` and notes are in `~/kb`. To stop the Hive: `systemctl --user stop hive-starter`. Running agents finish their turn; `systemctl --user kill hive-starter` after that ends them.

## Updates
A Hive looks for a new release every 5 minutes and installs it. Settings shows the version, has Check now and Update now, and sets how often it looks. Your settings and your agents' own changes stay. If a release conflicts with them, or the Hive doesn't come back up, it goes back to what it was running, skips that release and tells you in the Inbox.

`hive update` updates now and `hive update --check` only looks. You can turn updates off in Settings. Updates change code that runs with your agents' rights, so turn them off if you want to read each release first. Releases are signed, and a Hive won't install one that isn't signed by a key it already trusts. Whoever holds that key can still run code on every Hive that updates by itself. A Hive on 2.3.8 or older takes the first signed release on trust.

## Uninstall
It asks you to type `delete` first.
```sh
curl -fsSL https://hive.ihsan.cc/uninstall.sh | sh
```
```powershell
irm https://hive.ihsan.cc/uninstall.ps1 | iex
```
This deletes the `hive` user, or on Windows the `Hive` system, with everything in it. Push anything you want to keep first. If you installed in your own account, add `-s -- --this-account` after `sh`: that deletes the folder and the Hive's services and leaves your account.

## Extras
The core is only the board and the starter. Everything else is an optional module in `~/modules`: `hive module list`, `hive module add <name or git URL>`, `hive module remove <name>`. Agents add one when a job needs it. `dash`, `cost`, `extras` and `slack-door` (so far run only against a fake Slack) ship here. The rest are cloned on demand, and `list` says which need big tools of their own.

To write your own, make a folder or git repo with a `module.json`:
```json
{"name": "mytool", "description": "One line that tells an agent when to use it.",
 "bin": {"mytool": "mytool.py"},
 "provides": {"skills": ["skills/mytool/SKILL.md"], "procedures": ["README.md"]}}
```
`bin` commands go on PATH (without it, everything executable in `bin/`), `skills` become Claude Code skills, and `procedures` are what an agent reads first. The core never depends on a module, so a broken one breaks only itself.

## What it sends
By default, a Hive sends the makers a report when something fails, plus one small check-in a day holding a random ID, the version and the operating system. Chat text, file contents and thread names are never sent. Turn this off in Settings; the app's setup screen shows the same choice before anything goes.

## Change it
Settings are in the web app and `~/core/config.json`, read live: `max_sessions`, `usage_stop_pct`, `keeper_every_s` (0 is off; 10800 runs a round from `keeper.md` every 3 hours, which starts work nobody asked for). `~/core/orientation.md` is every agent's system prompt and `door.md` is your session's: add your machine, projects and rules there. It's all small enough to read, and your agents can change it. While the account has passwordless sudo, runs on outside models (Codex, OpenRouter) wait with a note, because they would share an account with root.
