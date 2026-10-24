# How to review a change to the Hive

Judge every change against its brief, the spec, and the owner's intent below. A change that matches its brief or the spec but works against one of these rules is **FIX**, even if the brief never mentioned it. Name the rule it breaks. A change that leaves a rule no worse than it found it is fine; not every change has to advance every rule.

## It is the original Hive

- Every Hive thinks it's the original Hive developing itself, and behaves like it: same roles, same wardens and tribunals, same risk management and scoping. The consumer Hive differs only in having one workspace.
- There is one Hive, called `hive`, run by the one command `hive`. A feature built for one copy is built for all of them; nothing forks into a "lite" variant.
- The lead knows its own system and how it works. A fresh install ships default knowledge about how the Hive works, filed where its agents look.
- Every install comes with the knowledge base, self-improvement, autonomy tiers and the default modules, and they stay simple.

## Every agent sees the same thing

- The sandbox walls the Hive off from the host, not agents off from each other. A change that hides one agent's work, logs or messages from another agent inside the same Hive is FIX.
- Every agent can read every project, every log and the Hive's own inner workings, and can propose improvements to them.
- Asking any worker about the Hive gets the same complete answer the lead would give, because they all read the same, current record.
- Logs of everything are kept and readable inside the Hive.

## The person sees agents talk

- Every message between agents is shown to the person: sessions, workers, wardens, tribunals, within and across modules, everything.
- Every agent has a number that only goes up and never repeats, and the person can see it.

## It improves itself

- The Hive can read everything about itself and improve it. That's the point of the system.
- It produces its own releases, and each install evolves past the bare core.
- Knowing what else a change must touch is the knowledge base's job, kept current by consolidation.

## Autonomy

- Agents can install what they need (Python, packages, tools) without the person. That is the autonomy.
- Settings are pick-lists the person chooses from, and the Hive can change them too.
- Running unsandboxed is an option the person can choose.

## Shape

- Everything lives in the install folder, `~/hive/` by default. `hive` opens the control center and starts the Hive; the Hive runs in the background whenever the machine is on; `hive shutdown` stops it.
- Chats work like a simple Slack: a lead chat by default, a chat per project, threads, nested by project. Opening a thread opens it beside the channel, and replying puts you in the thread.
- The person can paste images and drop files into a chat.
- Settings are choices, not typed values. The default model is Opus; a sweep is a number of hours, not a typed phrase.
- The dashboard shows what the owner's own dashboard shows, including messages between agents.
- Modules can come from anyone: the lead can be given a git repo and install it. The system module controls the host, schedules tasks and wakes the Hive.

## Install, update, release

- One command installs it on macOS, Linux or Windows, the way Claude Code installs, and it works with no further setup. Logging in to the model provider is part of it.
- It updates itself when a release comes out, and `hive update` does the same on demand.
- The install offers to share failures, logs and usage with the developer so the Hive can be improved.
- The landing page is bare, matches the developer's website, and links the essay and the install commands.

## Simple, built and tested

- Simple wins. Cut what isn't needed; a change that adds moving parts the rules above don't need is FIX.
- Everything is built and tested, down to the terminal UI and each harness: agent-to-agent messages, visibility, projects, module creation and self-improvement especially.
- Tests include asking agents to do things (improve something, talk to someone) and checking what they actually did.
