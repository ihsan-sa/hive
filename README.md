# The Hive

A small team of Claude Code agents on your own Linux machine. You talk to one Claude Code session; it hands work to agents, and they work in the background, talk to each other on a message board, and report back. There's no UI to learn and nothing to run but Claude Code: about 600 lines of Python, no other dependencies.

## Read this first
- **Agents run with `--permission-mode bypassPermissions`.** They can do anything their account can, so the Hive gets its own Linux user. That doesn't stop an agent reading world-readable files or using the network; use a VM if that matters to you.
- **It spends your usage on its own.** Every agent is a Claude session, and agents can start agents. At most 3 run at once by default. It stops starting agents at 95% of your weekly usage, but only on a subscription login: with an API key it can't see your spend and won't stop, so set a limit in the Anthropic Console.

## Install
Linux with systemd, plus git, python3 and curl.
```sh
git clone -b v2 https://github.com/ihsan-sa/hive hive && sudo ./hive/install.sh   # makes a user called hive, installs Claude Code for it
sudo -iu hive
hive                                                   # the first time, Claude Code asks you to log in
```
No sudo? Clone to `~/core` in a spare account, install Claude Code, and run `~/core/install.sh`.

## Use
Run `hive` and say what you want: "set up a repo for X and get the tests passing". It starts agents, tells you when they've finished or need you, and picks up where you left off next time you open it (`hive --new` starts a new conversation). Agents keep working after you close it; you hear from them when you come back.

To look underneath: `hive who`, `hive read --since 2h`, `hive read --thread T`, `hive post --thread T "@new ..."`. Agents' full output is in `~/sessions/`, starts and ends in `~/log.jsonl`, and notes in `~/kb`. To stop it: `systemctl --user stop hive-starter`. Running agents finish their turn; `pkill claude` ends them.

## Modules
Core is only the board and the starter. Everything else is an optional module in `~/modules`: `hive module list`, `hive module add <name or git URL>`, `hive module remove <name>`. Agents add one when a job needs it, or just ask. A few small ones ship here (`dash`, `cost`, `extras`); the rest are cloned on demand, and some need big tools of their own, which `list` tells you (`hwde` for PCBs, `chip-flow` for chips, `mech` for enclosures, `fw` for ESP32 firmware).

A module is a folder or git repo with a `module.json`, so you can write your own:
```json
{"name": "mytool", "description": "One line that tells an agent when to use it.",
 "bin": {"mytool": "mytool.py"},
 "provides": {"skills": ["skills/mytool/SKILL.md"], "procedures": ["README.md"]}}
```
`bin` commands go on PATH (without it, everything executable in `bin/`), `skills` become Claude Code skills, and `procedures` are what an agent reads first. The core never depends on a module, so a broken one breaks only itself.

## Tune it
`~/core/config.json`, read live: `max_sessions`, `usage_stop_pct`, `keeper_every_s` (0 = off; 10800 runs a round from `keeper.md` every 3 h, which starts work nobody asked for). `~/core/orientation.md` is every agent's system prompt and `door.md` is your session's: add your machine, projects and rules there. It's all in `~/core`, small enough to read, and your agents can change it. Update with `git -C ~/core pull --rebase`, then `systemctl --user restart hive-starter`.
