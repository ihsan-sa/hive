# The Hive

A small team of AI agents that lives in a folder on your computer and works on your projects while you do other things.

You talk to a lead in a chat. The lead turns what you say into briefs. Workers build each piece in their own sandboxed checkout. Tests run where the agent can't touch them, a reviewer reads the exact commit, and the change merges on GitHub pinned to the commit that was checked. After every merge the Hive measures the project against its score and keeps the change or reverts it.

## Install

macOS / Linux:

    curl -fsSL https://hive.ihsan.cc/install.sh | sh

Windows (PowerShell; on Windows PowerShell 5.1, run `[Net.ServicePointManager]::SecurityProtocol = 'Tls12'` first so the download can use TLS 1.2):

    irm https://hive.ihsan.cc/install.ps1 | iex

No GitHub login is needed to install. Running the line again reinstalls.

Then run `hive`. (The installer puts it on your PATH as `hive`, or the name in `HIVE_CMD`; `HIVE_VERSION=v0.1.2` installs an older release. Installs from v0.1.2 and before were called `hive-v0`; their next update renames them to `hive` and keeps `hive-v0` working.) The first run checks for git, Claude Code and the GitHub CLI, offers their own logins, asks where to live (Enter for `~/hive`) and for a GitHub repository (Enter makes a small private demo project), and opens the control center with the lead's chat.

On Windows, agents are sandboxed inside WSL 2; the installer installs or updates Claude Code there.

First run also makes the Hive start with the machine. On Linux it installs a systemd user service (`~/.config/systemd/user/hive.service`) and turns on linger (`loginctl enable-linger`), so the Hive runs at boot even before you log in. On macOS it adds a launch agent, and on Windows a Run entry for your account.

## A new project's test and score

A repository you name that doesn't exist yet starts with no test and no score: `.hive/project.json` holds `{"test": ""}`. Changes still get reviewed, but nothing is tested, and the Hive can't keep or revert a merge until it has a score to measure. Add both to `.hive/project.json` on the default branch, or ask the lead to do it:

    {
      "test": "go test ./...",
      "score": {"command": "./score.sh", "direction": "higher", "tolerance": 0.5}
    }

The score command's output ends with one number; `direction` says whether higher or lower is better, and `tolerance` is how much noise to ignore. The demo project comes with both.

## Models

The model workers and leads use and the reviewer's model are under **Settings** in the control center. They're stored in `.hive/config.json` in your Hive folder (`model` and `reviewer_model`), and `projects.<name>.model` overrides the model for one project.

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
- Main changes only through the gate. Goals, the score and project settings merge on a warden's read, and you're told after; at autonomy 1 they're yours to merge.
- When an agent needs something beyond its sandbox, like installing a package, the Hive can run it for them. At the default autonomy (2) a warden agent weighs the step and can run it on your machine, outside any sandbox, as your user (and as root wherever sudo doesn't ask for a password). Those scripts use the host's paths. A step that's hard to undo goes to a panel of three agents that votes first. At autonomy 1 the step comes to you in **Waiting** before any warden sees it.
- Spending money, credentials, opening the machine to the network and anything sent in your name come to you, whatever a warden or panel says. The Hive spots these by reading the step's words, so it errs toward asking you, but a step that hides what it does can slip past.

See [docs/architecture.md](docs/architecture.md) and [docs/risk.md](docs/risk.md).

## Uninstall

macOS / Linux:

    curl -fsSL https://hive.ihsan.cc/uninstall.sh | sh

Windows (PowerShell):

    irm https://hive.ihsan.cc/uninstall.ps1 | iex

Your folder is left alone.
