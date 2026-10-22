# The Hive

A small team of AI agents that lives in a folder on your computer and works on your projects while you do other things.

You talk to a lead in a chat. The lead turns what you say into briefs. Workers build each piece in their own sandboxed checkout. Tests run where the agent can't touch them, a reviewer reads the exact commit, and the change merges on GitHub pinned to the commit that was checked. After every merge the Hive measures the project against its score and keeps the change or reverts it.

## Install

Windows (PowerShell), with the GitHub CLI logged in:

    gh api repos/ihsan-sa/hive-standalone-build/contents/install/install.ps1 -H "Accept: application/vnd.github.raw" | Out-String | iex

macOS / Linux:

    gh api repos/ihsan-sa/hive-standalone-build/contents/install/install.sh -H "Accept: application/vnd.github.raw" | sh

Then run `hive`. (The installer puts it on your PATH as `hive-v0`, or the name in `HIVE_CMD`; a build from source is `hive`.) The first run checks for git, Claude Code and the GitHub CLI, offers their own logins, asks where to live (Enter for `~/hive`) and for a GitHub repository (Enter makes a small private demo project), and opens the control center with the lead's chat.

On Windows, agents are sandboxed inside WSL 2; the installer installs or updates Claude Code there.

## Use

    hive                  control center: Chats, Dashboard, Waiting, Modules, Settings
    hive ask "..." --wait ask the lead from a shell
    hive status           what it's doing
    hive improve          run an improvement sweep now
    hive doctor           check the install and sandbox
    hive update           newest release
    hive shutdown         stop it (agents finish their turn; `hive` starts it again)

The Hive keeps running in the background and starts with the machine. Closing the control center never stops work.

## What's safe and what isn't

- Agents hold no logins; they get a short-lived token per turn.
- Agents write only their checkout and a scratch home; network is outbound only, never to this machine or your LAN.
- Main changes only through the gate. Goals, the score and project settings are yours to merge.
- When an agent needs something beyond its sandbox, like installing a package, the Hive can run it for them. At the default autonomy (2) a warden agent weighs the step and can run it on your machine, outside any sandbox, as your user (and as root wherever sudo doesn't ask for a password). Those scripts use the host's paths. A step that's hard to undo goes to a panel of three agents that votes first. At autonomy 1 the step comes to you in **Waiting** before any warden sees it.
- Spending money, credentials, opening the machine to the network and anything sent in your name always come to you, whatever a warden or panel says.

See [docs/architecture.md](docs/architecture.md) and [docs/risk.md](docs/risk.md).

## Uninstall

    gh api repos/ihsan-sa/hive-standalone-build/contents/install/uninstall.ps1 -H "Accept: application/vnd.github.raw" | Out-String | iex

Your folder is left alone.
