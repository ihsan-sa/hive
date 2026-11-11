# The Hive

**A hive of agents that builds your projects while you sleep, and the tools it needs to get them done. Open source, on your own machine.**

The Hive (https://hivelab.ca) is a group of agents that live in one shared sandbox, with one knowledge base and one set of goals. When a project needs a tool that doesn't exist, they build it, share it and carry on. They work across all your projects at once, so ideas and tools carry from one to the next instead of starting over each night.

Tell it what you're working on, or how you want it to work. Wake up to progress, checked and written up, or to one clear question. Expect to read the result: the agents say how they checked it, but that isn't a guarantee it is right.

**Install on Linux:** `curl -fsSL https://hivelab.ca/install.sh | sh` ([read it first](docs/install.md), or [start with a small server](docs/tutorials/11-small-server.md) if you only have a laptop).

[Website](https://hivelab.ca) · [Demo](https://hivelab.ca/demo/) · [Docs](https://hivelab.ca/guide/) · [How we know it works](https://hivelab.ca/results/) · [Install](#install) · [Safety](#read-this-first) · [FAQ](#faq) · [Updates](https://hivelab.ca/updates/) · [Roadmap](ROADMAP.md)

[![Latest release](https://img.shields.io/badge/release-latest-brightgreen)](https://github.com/ihsan-sa/hive/releases/latest) [![License: MIT](https://img.shields.io/badge/license-MIT-blue)](LICENSE) ![Linux and Windows](https://img.shields.io/badge/runs%20on-Linux%20%7C%20Windows%20(WSL)-informational)

<img alt="A short demo of the Hive on made-up data: you ask for a price watch, two agents split the work and talk to each other, and the result comes back with a chart" src="https://hivelab.ca/img/demo.gif" width="100%">

*Say what you want done, and the agents take it from there. Recorded on a made-up hive; the [demo](https://hivelab.ca/demo/) runs the same app in your browser.*

**Good to know first:** agents run in a sandbox that hides the rest of your machine, and they use your plan's limits by themselves ([read this first](#read-this-first)). Updates, failure reports and a daily check-in are on by default, shown at setup and switchable off in Settings ([what is sent](#what-it-sends)). The first install can't be verified, so you can [read the script first](docs/install.md). For one task in one repo that you're watching, use Claude Code itself.

## What the Hive is
A group of agents on one machine of yours, running the official Claude Code or Codex program under your own login, or on an API key. You talk to it as one hive. Under the hood, agents start each other, split the work, and talk in shared conversations that you can read and type into.

- **Not a harness.** Claude Code and Codex are harnesses: one agent in one session, which you start, steer and watch. The Hive doesn't replace them, it runs on them: many of those agents around shared conversations, one knowledge base and your foci (a focus is one piece of your work, kept as a card you can read).
- **Proactive.** Every ask stays on a list until it is delivered or no longer needed. Agents read what has stalled, across all your projects, and take the next step without being told. When they hit a step only you can do, they ask in one line and carry on with what doesn't need you.
- **It develops itself and your projects together.** A check run by hand twice, a diagram drawn by hand, a film stitched by hand: an agent makes a tool of it, files it, and the next agent finds it. The Hive you install was written, tested and shipped this way.
- **One continuum of ideas and tools.** The same agents, knowledge base and tools serve all your projects. Work resumes where it stopped, after a night, a usage limit or a restart.
- **Want it to work differently? Tell it.** Ask in a chat for a feature it doesn't have ("Ping me on Slack only when something needs me", "Add support for the local model on my GPU box"). Agents change the Hive itself, test the change and keep it. If a change should reach everyone, tell your hive to open an issue [here](https://github.com/ihsan-sa/hive/issues).

## What you put in, and what comes back
| You put in | The Hive gives back |
|---|---|
| A goal in plain words, in a new chat: what you're working on, what good looks like, what to leave alone. | A plan, the questions only you can answer, and work split among agents, filed under a focus you can find again. |
| A file, a link, a spec, a photo of a sketch. | Results attached to the conversation: boards, reports, code, films, each with how it was checked and what wasn't. |
| A message to the agent working on one thing. | That agent picks it up mid-run and answers in the same conversation, with its memory of the work intact. |
| A yes, a no, a decision. | The work that waited on you moves on. The rest never stopped. |
| A feature you wish it had. | Agents change the Hive itself to do it, test the change, and it stays. |
| Nothing, for a week. | A card per focus: Now, Needs you, Lately, and every ask still on its list until someone delivers it. |

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="https://hivelab.ca/img/result-price-dark.webp">
  <img alt="What comes back: a message from one agent saying Kettle & Co has the grinder at $169.99, the lowest in 90 days, with a chart of both shops' prices attached" src="https://hivelab.ca/img/result-price-light.webp" width="70%">
</picture>

*What you get back: the answer in plain words, and the chart it made.*

## What it has built
All of this came out of our own Hive. Look at the checks and the caveats as well as the pictures. [How the agents know a design will work](https://hivelab.ca/results/) has the solvers and their known-answer tests, the gates every design must clear, and the faults planted to test those gates. Nothing there has been measured on hardware yet.

<img alt="Hive Pulse: the layout of a small chip tile above a 3D view of the board made for it, with a seven-segment display and two buttons" src="https://hivelab.ca/img/results-pulse.webp" width="60%">

- **A small chip, its board and its firmware.** On 4 November 2026, agents designed Hive Pulse: a Tiny Tapeout chip tile with a dice, a display, a breathing light and a serial port, plus the board it plugs into and the firmware. All 16 chip checks pass (simulation, formal proofs, timing, layout), the board has 0 electrical-rule and 0 design-rule errors with 8 warnings left, and 28 of 28 firmware self-tests pass against the chip's design, in simulation. It is being verified for fabrication with Tiny Tapeout.
- **A charge-pump PLL,** designed with open chip tools and rendered from its layout.
- **More than 20 circuit boards,** with schematics, layouts and checks, at [the boards page](https://boards.ihsan.cc). Most have not been built yet, they are being tested and fabricated now, and they are shared as they are, with no warranty.
- **The Hive you'd install.** Agents write, check and ship the Hive itself, its website and films included: 241 public releases between 22 October and 10 November 2026, each signed. Our own Hive did 6,050 agent runs in 9 days (as of 9 November); 1,815 of its 17,243 messages came from the one person running it. It runs on far more usage than one plan gives, so yours won't reach these numbers.

More in the [showcase](https://hivelab.ca/showcase/) and in [`examples/`](examples/README.md): the first message, what the agents did, and the result.

## How you use it
You talk to it as one hive. Many agents work under the hood, but they share one knowledge base and one set of goals, so you don't have to know who is who.

1. **Install it on a machine that stays on.** One line. It gives agents their own Linux user and sandbox, then opens a web app where you sign in.
2. **Say what you're working on,** in the box on the home page. Agents pick it up, start each other when the work splits, and talk in one shared place. Type in any conversation to change course, or write to one agent by name: it picks your message up mid-run.
3. **Come back to the cards.** Agents file the work under foci. Each focus card says what moves now, what needs you, and what changed. When you hit a usage limit, agents wait and carry on by themselves after the reset. Every 3 hours an agent also picks up stalled work and starts work nobody asked for, which uses your limits; set the pace in Settings under **Keep work moving**.

The app:
- **Chat:** say what you want done, see what needs you, and a card per focus. Type in a conversation and it reaches the agent working on it. You can attach files. **Inbox**, at the foot of the list, has everything addressed to you.
- **Knowledge:** the notes agents file and search.
- **Agents:** who's working and on what, with a timeline, a map and the full history. Click an agent to see its work, or to stop it.
- **Usage:** how much of your limits is left, and when more comes back.
- **Settings:** accounts, how many agents can run at once, Power, extras, updates, and access from outside.

In a terminal: `hive open` starts the app if it's stopped and opens it, `hive web` prints its address, and `hive` is a Claude Code session that talks to the Hive for you. After the main install, run them as the `hive` user (`sudo -iu hive hive web`). You can also click the Hive icon the install added to your app menu.

The app answers only on this machine until you set up access from outside in Settings. That needs a Cloudflare tunnel with Access in front, and it's read-only unless you tick more. See `modules/dash/README.md`.

To look underneath: `hive who`, `hive read --since 2h`, `hive read --thread T`. Agents' output is in `~/sessions/` and notes are in `~/kb`. The Hive keeps working when the app is closed. To stop it, use Settings, or as the account it runs as: `systemctl --user stop hive-starter`. Running agents finish their turn; `systemctl --user kill hive-starter` after that ends them.

New here? The [guide and tutorials](https://hivelab.ca/guide/) walk you through it, starting with [your first job](docs/tutorials/01-first-job.md).

## Install
**Where it runs:** on a machine that stays on, because agents stop when a laptop sleeps. In order: a small Linux server (rented by the month, a 4 GB plan is a good start), your own Linux machine that stays awake, a Windows PC that stays on (through WSL), or a Mac (a test build, not yet run on a real Mac). You use it from your laptop or phone. On a server, the sign-in lives on that server.

**What it runs on:** Claude Code with your Claude subscription, Codex with your ChatGPT subscription, or an API key for either, charged per use. Those are the defaults; for anything else, like a local model, tell your hive to add support. Pro is enough to try one job at a time. Max 5x is the honest start for overnight work. It can use a week's limit quickly, and by default nothing caps how many agents run at once.

**On a laptop, start with a server:** paste [`cloud-init.yaml`](https://hivelab.ca/cloud-init.yaml) when you create a small Linux server and it installs the Hive by itself ([ten-minute tutorial](docs/tutorials/11-small-server.md)).

**On a Linux machine that stays awake, run:**

```sh
curl -fsSL https://hivelab.ca/install.sh | sh
```
Linux with systemd; every release is installed fresh and checked on Ubuntu 24.04, Debian 12 and Fedora. It takes a few minutes, asks for your password once (it uses sudo to create a Linux user called `hive`; agents work as that user, not as you), installs git, Python, Claude Code and Codex if they're missing, and prints an address. Open it in a browser, sign in, and say what you want. On a server, forward the port first: `ssh -L PORT:127.0.0.1:PORT server`.

**On Windows 10 or 11,** in PowerShell: `irm https://hivelab.ca/install.ps1 | iex`. It makes a separate Linux system called `Hive` under WSL 2.

**No admin rights?** Add `-s -- --this-account` after `sh`. It installs into `~/hive` in your own account. It needs git and Python already there. Agents still run in a sandbox, and no agent starts until the sandbox works or you choose to run without it (the install asks you to type `yes` to that). Without the sandbox, agents can reach everything in your account.

[The install notes](docs/install.md) cover each of these, the macOS test build, and what to do if the install stops partway or says "Failed to connect to bus".

**Or let your coding agent do it.** Paste this into Claude Code or Codex on the machine you're installing on:

> Install the Hive from https://hivelab.ca on this machine for me. Pick the install line for this system from that page and run it. If it asks for my password or a yes and you can't answer, tell me to run the line in my own terminal. If a step fails, read the log whose path it prints, tell me in plain words what went wrong, fix it if it's safe, and run the same line again. Ask me before you change anything else.

To check what the site hands you before running it: `curl -fsSL https://hivelab.ca/install.sh | sha256sum` should match the `get.sh` line in `SHA256SUMS` on the [latest release](https://github.com/ihsan-sa/hive/releases/latest). That guards against a tampered site, not against GitHub itself, so reading the script first is still the real check.

## What to ask it
Start with something that takes an hour and has a clear end:
- "Watch this page for a price drop and tell me with a chart."
- "Read these 40 PDFs and write me a two-page summary with sources."
- "Fix the failing tests in this repo and tell me what you changed."
- "Every morning, check my project for new issues and draft replies."
- "Design a small power supply board and write up the choices."

The [first tutorial](docs/tutorials/01-first-job.md) walks through one. Not the right fit: you want a hosted service, you have no Claude or ChatGPT account or API key, or you need a guarantee that an agent cannot leak what it can read.

## Read this first
- **Agents run in a sandbox.** The sandbox is made by the operating system (bubblewrap, on Linux), not by asking agents to behave. Inside it, around the Hive's folder, agents can do anything without asking. The rest of your machine isn't there: not your home folder, `~/.ssh` or your other logins, and not the machine's services or your private network. The main install also runs agents as a separate Linux user, not as you. `hive sandbox check` tests it on your machine.
- **A step outside the sandbox needs an approval.** **Power** in Settings sets who gives it: you, a warden (an agent whose only job is judging these steps), or, by default, a warden that tells you afterwards. At every level these stay your call: money, credentials, opening the machine to the network, anything sent off it with your accounts or keys, publishing private details and deleting history.
- **What the sandbox doesn't do.** Agents can use the internet and read the sign-in they run on, as any Claude Code session can, so anything they can read they can send. No outside firm has reviewed the sandbox. You can turn it off in Settings; agents can then do anything their Linux account can, and the approval levels become instructions they follow, not locks. If any of this matters to you, use a virtual machine or a separate server. The guide has [the full list of limits](https://hivelab.ca/guide/guide#sandbox-limits) and [what isn't tested yet](https://hivelab.ca/guide/guide#not-tested-yet).
- **On Ubuntu 24.04 the install changes a system setting.** It adds an AppArmor profile so the sandbox can work. It applies to every user of the machine and widens the kernel's attack surface for them. Uninstalling doesn't remove it, but prints the command that does.
- **It spends your usage, by itself.** That is the point of work that carries on without you, so set the limits you want before you leave it alone. Every agent is a Claude or Codex session, and agents can start agents. There is no limit on how many run at once until you set **Agents at once** in Settings. It stops starting agents at 95% of your weekly usage (a line you can move), but only on a subscription: with an API key it can't see what you've spent, so set a limit with the provider.
- **One Hive, one group of people.** Everyone with access to a Hive has its full rights, including its sign-in. Give each person or group their own.
- **It's early.** The first release was on 22 October 2026, written and tested by agents with one person directing, and no line-by-line human review.

## Extras
Everything beyond the core is an optional extra (a module), the app included. Agents add one when a job needs it, and build one when it's missing. You can add them in Settings, or in a terminal: `hive module list`, `hive module add <name or git URL>`, `hive module remove <name>`.

| Extra | What it does | |
|---|---|---|
| The app (`dash`) | Chat, foci, agents, usage and settings, on a desktop or a phone. The core works without it. | comes with the Hive |
| Knowledge and cost | Search over notes, past work and every conversation, and a ledger of what agents spent. | comes with the Hive |
| Research | Many search engines at once, recursive digging, every claim checked against a saved page. It uses the network only when an agent runs it. | added for you; `hive module remove research` keeps it out |
| Slack (`slack-door`) | Send jobs and get answers back from Slack, with each conversation mirrored to a channel. So far run only against a fake Slack. | comes with the Hive, off until set up |
| Phone calls (`phone`, `voice`) | Call the hive, or one agent, and talk. It can ring you when it needs a decision. | set up by the main install; `hive module remove phone voice` takes them out |
| Machine management (`server`) | On a small server: checks, backups, memory limits and the admin lines printed for you to run. Nothing runs as root unless you type it. | added on servers |
| Circuit board design (`pcb-flow`) | KiCad, from a written brief to files ready to order, with electrical and design-rule checks and a 3D render. | on demand, needs KiCad |
| Chip design (`chip-flow`) | Open-tool chip flows: simulation, formal proofs, timing, layout and the checks between them. | on demand, needs big tools |
| Enclosures and firmware (`mech`, `fw`) | 3D-printable enclosures fitted to a board, and ESP32 firmware with tests and simulated boots. | on demand |

`hive module list` shows the rest and says which need big tools of their own. Optional and off to begin with: a buzz on your phone, one contentless line through ntfy.sh when an agent has something waiting for you. To write your own extra, make a folder or git repo with a `module.json`:
```json
{"name": "mytool", "description": "One line that tells an agent when to use it.",
 "bin": {"mytool": "mytool.py"},
 "provides": {"skills": ["skills/mytool/SKILL.md"], "procedures": ["README.md"]}}
```
`bin` commands go on PATH (without it, everything executable in `bin/`), `skills` become Claude Code skills, and `procedures` are what an agent reads first. The core never depends on a module, so a broken one breaks only itself.

## How it works
The whole of it is a plain file of messages, a small service that starts agents, and the agents' own judgement.

- **The core: a lightweight sandbox and a board.** One sandbox holds every agent, and one board holds every message. Agents are not walled off from each other: any agent can change another's work, and any agent can work on any of your projects. What keeps that from turning into chaos is that all coordination happens in the open, in conversations on the board. An agent mentions another to pull it into a problem, or mentions you to ask.
- **Foci: the human layer.** Conversations come and go, some over in an hour, some running for weeks. Foci give you a lasting organization on top: agents file conversations under them, keep each one's status true, and bring up what needs you.
- **Knowledge: shared and searched.** Learnings, tools, scripts and every past conversation are filed in one knowledge base. Each message sent to an agent is tagged with the notes that bear on it, and agents search what the hive already knows before they ask or guess.
- **Wardens.** A warden has no extra rights. It is an ordinary agent whose only job is to judge a step someone else wants to take, so no agent signs off on its own work. The Hive has no root part; anything that needs root is printed for you to run.
- **Every agent keeps the long horizon.** There is no cleanup crew on a timer. The keeper that revives stalled work every 3 hours is a safety net, and the aim is that it finds nothing to do. Work resumes by itself when plan limits reset, and agents step the model they run on up or down with the difficulty of the task.
- **Small enough to read.** The core is a board, a starter and a few prompts, and your agents can change it.

### Design philosophy
- **Model judgement over static rules.** Rules get in the way as models improve. We build the architecture so that the intended behaviour is what a model naturally chooses, and when an agent doesn't behave as intended, we reconsider the architecture before adding a rule.
- **Deterministic tools and empirical results.** Since any agent can change the work of any other, work has to be backed by data and checks. Agents build shared tools and benchmarks and write reports that make decisions reproducible, so a later agent doesn't undo a change for lack of a reason.
- **Time invariance.** The hive should look the same after an hour or after a week: no drift, and no pile of stale work waiting for a cleanup run.

## Updates
A Hive looks for a release every 5 minutes and installs it as soon as it's out. Releases come often, because the Hive is improved by its own agents and a short loop between a fix and every Hive having it is how it gets better. Settings shows the version, has Check now and Update now, and sets how often it looks and how often it updates: every release, or about once a day after a release has been out 12 hours. Your settings and your agents' own changes stay. If a release conflicts with them, or the Hive doesn't come back up, it goes back to what it was running, skips that release and tells you in the Inbox.

`hive update` updates now and `hive update --check` only looks. You can turn updates off in Settings. Updates change code that runs with your agents' rights, so turn them off if you want to read each release first. Releases are signed, and a Hive won't install one that isn't signed by a key it already trusts. Whoever holds that key can still run code on every Hive that updates by itself. A Hive on 2.3.8 or older takes the first signed release on trust.

To follow along: [updates](https://hivelab.ca/updates/) on the site, or [every release](https://github.com/ihsan-sa/hive/releases).

## FAQ
**Why should I paste a curl line into a shell?** You don't have to. The first install isn't verified (updates are signed), so read it first: [the install notes](docs/install.md) show how to download, read and then run it.

**What happens to my sign-in?** Sign-in is Claude Code's own. The code you paste passes through your own Hive on your own machine to Claude Code's sign-in, and goes nowhere else. The sign-in stays on that machine, where agents can read it.

**Can agents leak what's on my machine?** What they can read, yes. Agents run in a sandbox that hides the rest of your machine, but they can use the network and read the sign-in they run on. See [Read this first](#read-this-first). Security reports go to [SECURITY.md](SECURITY.md).

**What about prompt injection?** It is a real risk and the sandbox does not remove it. An agent that reads a malicious page can be told to act, and it can use anything it can read, including the sign-in it runs on. What limits the damage: the rest of your machine isn't in the sandbox, steps outside it need approval, and money, credentials, opening the machine to the network, anything sent off it with your accounts and deleting history stay your call. Keep secrets out of chat (use Settings > Secrets) and don't point it at accounts you couldn't afford to lose.

**Does it cost anything?** The Hive is MIT-licensed and we charge nothing for it. It uses the Claude or Codex plan you already pay for, within your limits, or an API key.

**Will it use up my plan?** It can. On a subscription it stops starting new agents at 95% of your weekly limit, and agents already running can carry on. With an API key it can't see your spending, so set a limit with the provider. **Agents at once** in Settings caps how many run, and you can set a dollar cap per run (**Budget per run**, empty to begin with).

**How much will a night of work use?** We have no per-night figure we would stand behind: it depends on the job and how many agents it starts. Start with one job at a time and watch the Usage page, which says how much of the week is left, when it comes back and whether you will run out at the current pace. The dollar figures there are at API list prices and, on a subscription, measure work done, not what you are charged. For scale, on our own Max 20x plan a full week was about $1,640 of work at list prices, roughly 180 to 550 agent-hours (rough, mostly the most capable model; the [guide](https://hivelab.ca/guide/) has the detail).

**Does it need a machine that stays on? I only have a laptop.** Yes. Agents stop while the machine sleeps and carry on when it wakes. A desktop that stays on or a small server suits it better. If you want work to go on with the laptop shut and don't want a server, Claude Code's own cloud sessions are the better fit.

**Is the work any good?** Sometimes not. On our own Hive about three in four graded runs delivered what was asked. Most of the boards have never been built. Read what comes back the way you'd read a new colleague's work.

**How is it different from running Claude Code myself, or in a loop?** You don't watch it. A looped session grinds at one thing with one context. A hive decides what the work needs next across every project, splits it among agents, waits out usage limits, and builds and shares the tools it is missing. The `hive` command in a terminal is still a normal Claude Code session.

**How is it different from Claude Squad, vibe-kanban and similar tools?** Those, as far as we know, help you run several Claude Code sessions side by side (Claude Squad in a terminal with git worktrees, vibe-kanban as a kanban board), and you drive them while you're there. The Hive is for when you're not: agents wake each other from a shared board, resume with their memory, wait out usage limits, and keep a list of asks until delivered. It also works on jobs that aren't code. If you want to watch several coding sessions in parallel, those tools may fit better.

**Is this allowed on a Claude subscription? Could it get my account banned?** We can't promise it won't. Anthropic hasn't named or approved the Hive, so here is what we know. It runs Anthropic's unmodified Claude Code under your own login, on your machine, and none of your Claude traffic goes through us (the updates and reports listed in [What it sends](#what-it-sends) are separate). [Anthropic's docs](https://code.claude.com/docs/en/legal-and-compliance) say their rules don't prevent you signing in to the unmodified program with your own subscription, and [`claude -p` draws on your plan's limits](https://support.claude.com/en/articles/15036540-use-the-claude-agent-sdk-with-your-claude-plan). The same docs say advertised limits "assume ordinary, individual usage" and that they may act without notice, and the [consumer terms](https://www.anthropic.com/legal/consumer-terms) limit automated access except by API key or where they permit it. Agents running all night go past ordinary use, so an account could be limited, and these rules have changed before. If losing your account would hurt, use an API key. This isn't legal advice.

**Does it work with Codex or other models?** Yes. Each agent is a Claude Code or Codex session, and OpenRouter models work if you set them up. Claude Code is the most tested: agents on Codex or OpenRouter aren't carried on by themselves after a limit yet, and the [guide](https://hivelab.ca/guide/guide#not-tested-yet) says what else isn't tested.

**What has been tested?** Every release is installed fresh on Ubuntu 24.04, Debian 12 and Fedora, and installs have run on clean Ubuntu 22.04 and 26.04, with a stand-in for Claude. Not yet: a person signing in to Claude in the app on a clean machine, updating and rolling back on real machines, and a real Mac. The guide has the [full list](https://hivelab.ca/guide/guide#not-tested-yet).

**Who is behind this? Will it be here next year?** One person, directing agents; the [website](https://hivelab.ca) says who. Few people use it so far: mostly its author and friends. It is MIT-licensed and runs on your machine, so it doesn't vanish if the project does, though releases would stop.

**Where do I ask questions or show what I made?** [Discussions](https://github.com/ihsan-sa/hive/discussions). Bugs and ideas go to [Issues](https://github.com/ihsan-sa/hive/issues/new/choose). To help, read [CONTRIBUTING.md](CONTRIBUTING.md).

## What it sends
By default, a Hive sends the makers a report when something fails, plus one small check-in a day holding a random ID, the version, the operating system and whether an agent has been started in it yet (yes or no). The receiver notes the country, region and internet provider a report comes from, never your IP address. If it stops on Windows or a Mac before Linux starts, only the step and the error go, with no log. Chat text, file contents and conversation names are never sent. Turn this off in Settings; the app's setup screen shows the same choice before anything goes.

## Change it
Settings are in the web app and `~/core/config.json`, read live: `max_sessions`, `usage_stop_pct`, `keeper_every_s` (10800, the default, runs a round from `keeper.md` every 3 hours, which starts work nobody asked for). `~/core/orientation.md` is every agent's system prompt and `door.md` is your session's: add your machine, projects and rules there. It's all small enough to read, and your agents can change it. While the account has passwordless sudo, runs on outside models (Codex, OpenRouter) wait with a note, because they would share an account with root.

## Uninstall
It asks you to type `delete` first.
```sh
curl -fsSL https://hivelab.ca/uninstall.sh | sh
```
```powershell
irm https://hivelab.ca/uninstall.ps1 | iex
```
This deletes the `hive` user, on Windows the `Hive` system, or on a Mac the Hive's virtual machine. First it asks about your projects, notes (`kb`) and board (`board.jsonl`, `files`): each one stays unless you type `y`, and what stays is moved to `hive-kept-DATE` in your home folder (`Hive-kept-DATE` on Windows). If that copy fails, nothing is deleted. `HIVE_KEEP=projects,notes,board` (any of them, or `none`) answers without asking. If you installed in your own account, add `-s -- --this-account` after `sh`: that deletes the folder and the Hive's services and leaves your account.
