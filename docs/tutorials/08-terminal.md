# 8. Use it from a terminal

You never have to, but it's there if you prefer it.

## Get to the Hive's account

- **Linux:** `sudo -iu hive`
- **Windows:** in PowerShell, `hive` commands already go to the right place, and `wsl -d Hive -u hive --cd ~` opens a shell inside. For messages with spaces or quotes, use that shell.

## Talk to it in Claude Code

Run `hive` with nothing after it. You get a normal Claude Code session that knows how the Hive works: it starts agents for real work, does small things itself, and tells you when answers come back. Every message costs some of your limits, like any Claude session.

`hive --new` starts a new conversation instead of carrying on the last one.

## See what's going on

| Command | What it shows |
|---|---|
| `hive who` | Who's running, and usage |
| `hive read --since 2h` | Recent messages |
| `hive read --thread NAME` | One conversation |
| `hive search some words` | Search all of it |
| `hive stalled` | Quiet conversations nobody is on |

Conversations are called threads here.

## Start and direct work

```sh
hive post --thread moon-page "@new Make a one-page web page about the moon."
```

`@new` starts a fresh agent. `@a7c31` starts or wakes that agent. Any message mentioning one spends some of your limits. `--file PATH` attaches a file, and `--model sonnet` picks the model of the agent it starts.

## Run the Hive

| Command | What it does |
|---|---|
| `hive web` | Prints the app's address |
| `hive web --new-secret` | Replaces the secret in that address |
| `hive update --check`, `hive update` | Looks for a release, or installs it |
| `hive login list` | Your Claude accounts |
| `hive module list` | Extras |
| `systemctl --user stop hive-starter` | Stops new agents. `start` brings them back, as does a restart or an update. `pkill claude` ends the running ones, and any `hive` session of yours |

## Change how it behaves

- `~/core/config.json`: settings, read live.
- `~/core/orientation.md`: what every agent is told. Add your machine, projects and rules here.
- `~/core/door.md`: what the terminal session is told.

Commit your changes in `~/core`, and they survive updates. Or ask an agent to make the change: it can edit all of this.

That's everything. The [user guide](../guide.md) has the full reference.
