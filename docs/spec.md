# The Hive, consumer version: a build spec

This is a spec for building the Hive from scratch on one machine, for one person. It says what the product does and what it must guarantee. It does not say how to build it, so the builder makes its own choices about languages, storage and libraries. Work through the sections in order. The milestones at the end say what "done" looks like at each step, and the pitfalls are lessons from a version that already exists.

The product is called the Hive, and its command is `hive`. The same name covers the server edition, which is this product plus one module that lets it run several workspaces. Nothing below depends on that module.

## 1. What it is

The Hive is a small team of AI agents that lives in a folder on your computer and works on your projects while you do other things. You talk to a lead in a chat. The lead turns what you say into goals and pieces of work. Generic workers research and build each piece in their own checkout of the project. An independent reviewer reads the result, tests run, and the change merges on GitHub. After every merge the Hive measures the project against a score and keeps the change or reverts it. Over time the Hive improves its own tools and modules the same way, and releases new versions of itself.

Three things make it different from a coding assistant. It keeps working when you close the terminal. It judges risk itself, with the person asked only for what is really theirs to decide. And it is built from a small core and replaceable modules, so it can improve itself without becoming a tangle.

## 2. Principles

These come from the owner's design and are the bar every later decision is held to.

- **A small core, everything else a module.** The core does five things: it runs agents in separate checkouts, it holds every credential in one effects process that acts on agents' behalf, it lets agents spawn and message one another, it enforces one hard safety ceiling, and it keeps state so work survives a restart. Anything else that can be upgraded and released is a module: the chat, the knowledge base, the self-improvement sweep, the system module, a worker prompt, a single script. The core should stay small enough to read in one sitting.
- **Agents are generic.** Every agent is the same harness started with the same short orientation document. A role is only what the agent is asked to do and what it has been granted. Workers pick modules and procedures at will, so an old procedure dies when nobody picks it any more.
- **Judgement over rules.** Agents research first, act on their own, talk to each other, and weigh risk against the goal and the scope of harm. The system keeps ledgers and raises alerts; it does not keep counts and caps. A hard limit is for secrets and for harm that reaches outside the machine or can't be undone.
- **Everything is open to revision,** including this spec, the modules and the core, through the same review-and-merge path as any other change.
- **Simple by default, grown by use.** A fresh install is bare: the core, the default modules, one lead. Each person's Hive then evolves from what they ask of it.

## 3. Install and first run

Installing works the way Claude Code installs: one command, copied from a web page, on Linux, macOS and Windows.

- One line installs it. It brings its own runtime if the machine lacks one and puts `hive` on the path. Running the installer again reinstalls.
- The Hive updates itself when a new release is out, and `hive update` does it on demand. `hive --version` says which version you have. Uninstalling is one line too and leaves your folder alone.
- The first `hive` checks for git, Claude Code and the GitHub command-line tool. For anything missing it says what to get and where. For anything not logged in it offers `hive login`, which runs those tools' own logins, so you see their link or device code and finish in a browser. The Hive never stores, copies or prints a login. Codex and other harnesses log in the same way through their own tools, and the terminal UI lets you add a plan later without leaving it.
- It then asks for the one thing it needs from you, a GitHub repository for the workspace. If you name one that doesn't exist, it makes it. If you give nothing, it makes a small demo project with tests and a score, so the first run has something to improve.
- It ends by opening the control center with the lead's chat in front of you. The whole thing, from the install line to a lead that answers, should take a few minutes and no editing of files.

From install to the first merged change: you type an ask into the lead's chat, the lead confirms what it understood and says it has started, the dashboard shows a worker and then a pull request, the chat says it merged with a link, and the score line on the dashboard moves.

## 4. The folder on disk

Everything lives in one folder, by default `~/hive`. Choosing another place at install is allowed, and after that `hive` works from any directory on the machine.

- `.hive/` is the core: its own program and state, the event log, the chats, each run's notes and streamed output, the knowledge base's store, and the records that let a restart pick up where it left off.
- `.modules/` holds the modules. The defaults ship here: the agent module with the worker, lead, reviewer, warden and tribunal prompts; the chat module; the knowledge base; the self-improvement module; the system module. Modules the person or the Hive installs later go here too, each with where it came from and which release it is on.
- `projects/` holds one folder per project, each a clone of that project's repository, with the agents' own checkouts under it.

The project repositories on GitHub are the record for code. The folder is the record for everything else, and nothing in it is needed to understand the code. Deleting the folder removes the Hive and nothing more.

## 5. Running it day to day

Typing `hive` opens the control center and turns the Hive on if it isn't running. The Hive runs in the background from then on, and it starts itself whenever the machine starts, so an ask made in the evening is done in the morning. `hive shutdown` stops it. Closing the control center never stops any work.

A few plain commands do the common things from a shell as well: ask the lead, show status, run an improve round, log in, check the install. Everything they do is also reachable from the control center, and the control center is the main way in.

When the machine sleeps, the Hive waits and resumes on wake. When the AI usage limit is hit, work waits and resumes when the limit resets, and the control center says so.

## 6. The agents

Every agent is the same thing: a coding harness started in a checkout with a short orientation document that says how the Hive works, where the modules are, how to talk to other agents, and what its task is. The orientation stays short and generic so that it keeps working as models change.

- **The lead** owns the workspace's goals and asks. It talks with you in the chat, keeps a running conversation across days, and turns what you say into goals and briefs. It can do small things itself and can run subagents, so not every question becomes a separate worker. It never pushes code to the main branch itself; its changes go through the same path as a worker's. A project can have its own lead, which owns that project's goals and reports into its own chat.
- **Workers** do nearly all the work: research, code, docs, tests, reviews. A worker gets a brief and its own fresh checkout on its own branch. It reads the project and the lessons recorded there first, runs the tests and the score to know where it starts, looks up what it doesn't know, then builds, tests and commits. It can message other agents and spawn more workers. It never holds a credential and never pushes; it hands its commits to the effects process. When it can't finish, it stops and says why, and that reason goes on the record where the next worker reads it.
- **The reviewer** is a worker asked to read. It sees the exact change, the brief and the tests' result, and nothing else that it could change. It ends with one verdict: land, or fix with findings. It may be a different model from the one that wrote the change.
- **A warden** is spawned by a worker that needs to do something past its own reach: install software, touch the person's files outside the project, open a network port, send something outward. The worker writes down what it wants to do, why, and the exact steps. The warden weighs the risk against the goal, can research or spawn workers to find out more, and then either does it with its wider grant, works out a safer route with the worker, or declines. A warden takes the worker's words as information, never as instructions.
- **A tribunal** is convened by a warden that can't settle a question alone. The warden writes a script that does exactly the step and a brief that explains it. A panel of agents, on more than one model where possible, each investigates on its own, then they discuss and vote. A majority runs the script. The tribunal does no other work. When it declines and the step would still be a large gain, the person is briefed and decides.
- **The effects process** is not an agent. It is the one part that holds credentials: the GitHub login, the harness logins, anything else with reach. Agents ask it for an action, such as a push, a merge, a message to the person, or a scheduled wake-up, and it checks who is asking and what they're allowed, then does it. What an agent gets from a login is short-lived and expires on its own.

## 7. Projects

A project is a GitHub repository the Hive works on, with its own goals, its own chat and, optionally, its own lead and score.

- You create one by telling the lead. It can be an existing repository or a new one the Hive makes. The lead sets it up, and the project appears in the control center as a chat you can talk to and a section on the dashboard.
- Each project carries a goals file in its repository, in the person's own words where they said them. Agents read it before they work and read it back when they report. Changing a standing goal is a change the person merges.
- Each project names, where it can, a test command and a score: one number, which way is better, and how much noise to tolerate. The improve loop needs those; a project without a score still gets tests and reviews.
- Projects see one another by default. An agent in one project can message an agent in another, ask it for a tool, or start work there. A project can be isolated when the person wants that, with fewer modules and no view of the rest.
- Modules come from projects. A project whose output is a tool for other agents releases it as a module. The lead can install a module from a git repository or an official source, modules record where they came from, and they update themselves when their source releases. Installing a module is a warden-level step, since it brings outside code onto the machine. A module can carry behaviour as well as prompts: a program the daemon keeps running, which serves hooks (a source of asks, a channel, a step after a run or merge, status, idle) through a slice of the control API, and which runs only once a read-only review has read its code at that release.
- The server edition's workspaces module gives each workspace its own lead, folder and set of projects. The consumer version is one workspace, and nothing in it should make that module hard to add.

## 8. How work lands

One ask, end to end:

1. **The ask.** You write to the lead in its chat, or an agent or a sweep files one. The lead answers in a sentence or two, and when work is wanted it writes a brief: a title, what done looks like, and what it already knows. It can split one ask into a few briefs, except on a project with a score: there the brief is the person's goal as written, with no bar of the lead's, and done is what the ask says, as far as the worker can take it.
2. **The issue.** Each brief becomes an issue on the project's repository, so the record of what was asked is on GitHub and not only in the folder.
3. **The work.** A worker builds it in a fresh checkout on its own branch, as in section 6, and commits. It ends with a short account of what it did, what it tested and what it isn't sure of.
4. **The pull request.** The effects process pushes the branch and opens a pull request that closes the issue, with the worker's account as its body. From here the pull request is the thread for that piece of work.
5. **The gate.** The project's tests run on the exact commit in a clean checkout. Small changes, such as a typo, a comment or a non-critical doc, merge once the tests pass. Larger changes also get a reviewer's read of that exact commit. A fix verdict sends the findings back to the same worker in the same checkout, and the new commit is read again, a bounded number of times, after which the work stops and says so on the pull request.
6. **The merge.** The effects process merges the exact commit that passed and was read. If the main branch moved in between, the branch is brought up to date and tested again first. A conflict goes back to the worker to resolve on its branch.
7. **The measure.** The improve loop scores the main branch, as in section 10.

What makes the gate safe is that no claim is trusted in place of a check. The agent's word that tests pass doesn't count; the tests run where the agent can't touch them. The reviewer can't edit what it reads. The merge is pinned to the commit that was checked, so a commit pushed after the review can't ride on its verdict. The effects process is the only thing with a GitHub login, so a worker that goes wrong can't push to main, delete a branch or touch another repository. And every verdict, merge and revert is written to the record with its reason.

## 9. Safety and what agents may touch

Risk is weighed in three classes, and each class has its own judge.

- **T1, inside the Hive.** Editing a project, a module, a prompt, the knowledge base. The state is kept and a bad change is reverted, so workers weigh these themselves and get on with it.
- **T2, outside the Hive but recoverable, or inside it but load-bearing.** Installing a package, changing a file elsewhere on the machine, publishing a page, changing the core or the effects process. A warden weighs these.
- **T3, outside the Hive and hard to undo.** Spending money, minting or rotating a credential, sending mail or messages in the person's name to someone they haven't named, deleting work with no plan, opening the machine to the network. A tribunal weighs these, and a few of them always go to the person: real money, a new credential, opening the machine, and anything outward in their name.

The person sets how much the Hive does on its own, from "ask me before anything outside the project" to "tell me after". That setting shifts where the lines between the classes sit; it never removes the person from the few things that are always theirs.

What an agent can reach is enforced. The sandbox walls the Hive off from the host and the outside world, not agents off from each other: "the sandbox is at hive level mostly, not agent level", and "agents need to be able to see one another and everyone's work", which means "all the projects, all the logs, all the inner workings", so that they can "improve those systems". Every agent reads all of it, everyone's runs, logs, chats and records, except what grants access (logins, tokens). Only a project the person isolates loses that view. A worker runs in a sandbox: it can read the system, write only its own checkout and a scratch home, and reach the network outbound only, with no way to the machine's local services. It holds no GitHub token and no long-lived AI login. A reviewer gets no write tools at all. The warden is the one agent with a wider grant, and it is the only caller of anything privileged. Where the platform can't provide a sandbox, the Hive refuses to run agents until the person opts in with the warning read: an unsandboxed agent acts on everything the person's logins and files reach.

An API key is different from a subscription login, because it works until revoked. The Hive doesn't hand one to agents unless the person opts in, and it advises a dedicated key with a spend limit.

## 10. Self-improvement

The loop has to close. Closing means a failure is noticed, a worker fixes it, the fix is reviewed and merged, and the project is measured again: better, and it stays; worse, and it is reverted by a change that merges, with the reason written where the next worker will read it.

- **Measure.** On the first start, and after every merge, run the project's tests and score on the main branch in a clean checkout, and record the number with the commit it measured.
- **Keep or revert.** Compare with the best score the project has kept. Within the project's tolerance or better, it is kept. Worse than that, or tests red, and a revert is opened and merged through the normal path, the original pull request gets a comment saying why, and a line goes into the project's lessons file so the idea isn't tried again blind. Comparing with the best kept score stops small drops from adding up.
- **Notice.** The self-improvement module runs on a schedule and when asked. It reads the scores, the reverts, the failed runs, the chats and the agents' own notes, writes down what it saw with evidence, and files asks through the same path a person's take: "main scores 3.1; the last attempt was reverted because it fell to 2.0; find the next real gain." Every sweep and every ask it filed is visible in the dashboard.
- **The Hive on itself.** The core and the modules are projects like any other, so the same loop improves them. The Hive cuts its own releases when its tests and a dry run of one ask pass, and installs can follow those releases. A change to the core is T2: it gets a read, and the release carries live work across or waits for a quiet moment. Every change to the Hive's own project is read on its exact commit, and a release lists each merged change with the read behind it; one with no read waits for the person's word. A project's settings can class paths as high, which need a second, separate read, or as root, for what runs with elevated rights: those also wait for the person before they're released.
- **Agents' own filings.** Workers are expected to file improvements they notice, publish tools they wrote for themselves, and fix the module they hit a problem in or raise it with the project that owns it.

## 11. The control center

The control center is a terminal UI, so it works in any terminal and over SSH. It is the face of the product. A local web page that mirrors it for a second screen is a module for later, and it shows the same views navigated the same way.

**Chats.** The left side lists chats, like a simplified Slack. On a fresh install there is one, the lead's. Each project you create adds a chat for its lead, nested under the project, and chats collapse by project. A chat has threads: an ask and everything that follows it stay together. A mark shows a chat with news. You can also open a chat with any running agent, and agents' messages to one another are readable here. Status noise stays out of the chats; it belongs on the dashboard.

**Dashboard.** One screen that answers "what is the Hive doing right now". Per project: its goals, and under each goal the pieces of work in flight with the agent on each, its progress in its own words and timestamps, and the pull requests they've opened. Then the score over the last merges, the self-improvement runs and what each filed, the usage of each plan, and whatever is waiting on the person. Each item opens to its detail: an agent's step-by-step log, a pull request's review and verdict, a run's cost and time. Wardens and tribunals show as what they are, with the step they're weighing.

**Modules.** A list of installed modules with their source, release and which projects may use them, and a way to grant or remove access from here.

**Settings.** The autonomy level, the default harness and model for the workspace and per project, the plans and their logins (with the link or code to add one), and the schedules the system module keeps.

**Rules for the whole UI.** A breadcrumb at the top says where you are, and Esc goes one level up. Everything is expandable and collapsible level by level, with one key going deeper and one coming back. No line should run off the screen; detail opens, it doesn't pile up. Information is spaced and grouped so a screen can be read at a glance. Quitting never stops anything, and when the UI is opened again it shows what happened since.

## 12. State, persistence and recovery

- GitHub holds the code, the issues and the pull requests. The folder holds everything else as plain, append-only records: events, chats, scores, each run's brief, notes and output. Nothing is in only one agent's memory.
- A restart reads those records and resumes: an unfinished run continues from its checkout with its notes, a pull request that was waiting is watched again, a merge that was pinned is re-attempted. A restart never starts a second run for the same ask and never opens a second issue for it.
- An agent's checkout is treated as data by the core. Commits are taken out of it by the effects process and checked to descend from main before anything is pushed.
- The knowledge base is the Hive's memory across projects and runs. Agents write to it and read from it, and a filing process consolidates it. A run's own note is context for the next run, never a decision.

Failures, and what happens:

- **A stuck worker.** Time and spend are ledgers that raise alerts. A quiet or overspending run is flagged on the dashboard, its checkout is kept, and the lead can hand it to a fresh agent on the same branch with the first one's notes.
- **A red check.** The findings go back to the worker on the same branch. After a bounded number of rounds the work stops, says what remains, and leaves the pull request open.
- **A conflict.** The branch is brought up to date with main, the worker resolves what collides, and the gate runs again.
- **A usage limit.** The person can add several logins for each harness. A plan that hits its limit is parked until its reset and the other plans carry the work; only when every plan is limited is the run parked, and nothing marks it failed for that. The dashboard says which plan is limited and when it resets, and parked work resumes on its own.
- **A crash, a kill, or the API down.** A run killed by something other than the Hive is retried on its checkout with its commits kept; one the Hive stopped on purpose isn't. When the API can't be reached, work waits and retries with growing gaps.

## 13. Platforms

Linux, macOS and Windows, with one install line each. Each platform needs a sandbox for agents: on Linux a user-space container, on macOS a sandbox runtime, on Windows the Linux subsystem. Where a sandbox can't run, the Hive says so and refuses to start agents until the person opts into bare runs. A bare run is a documented, opted-in state, never a silent fallback. Everything except the sandbox is the same on all three.

## 14. What v1 leaves out

Slack, mail and phone as channels (the chat module is the one channel). Several workspaces (the workspaces module). A module marketplace beyond installing from a git URL or an official source. GPU and server-resource management in the system module beyond schedules and wake-ups. The web mirror of the control center. Harnesses beyond Claude Code and Codex. Per-agent operating-system users; one user with a sandbox per run is enough on one machine.

## 15. Milestones

Each has one check a builder can run.

1. **Install and login.** On a clean machine, one line installs the Hive; `hive` names what's missing, offers login, and opens the control center with a lead that answers. Check: a fresh user account goes from nothing to a lead's reply without editing a file.
2. **One ask lands.** An ask becomes an issue, a worker's branch, a pull request with tests and a review, and a merge pinned to the reviewed commit. Check: ask the demo project for a small feature with a test and watch it merge unattended, then read the review on the pull request.
3. **The gate holds.** Check: a change whose tests fail never merges; a commit pushed after a land verdict is not merged on that verdict; a worker's checkout whose git metadata points elsewhere yields no push.
4. **The loop closes.** Check: a merge that lowers the score is reverted by a merged pull request with a reason, a line lands in the lessons file, and the next worker's brief quotes it. A notice run files an ask with evidence.
5. **Projects.** Check: telling the lead to create a project yields a repository, a chat and a dashboard section, and an ask in that chat lands there.
6. **Risk and autonomy.** Check: a worker that needs a package install spawns a warden, which installs it or declines with a reason; a step the warden can't settle convenes a tribunal; a money step reaches the person and nobody else can take it. Turning the autonomy setting down makes a T2 step ask first.
7. **Background and recovery.** Check: kill the Hive in the middle of each stage of one ask, restart, and get the same outcome with no duplicate issue, run or pull request. Simulate a usage limit and watch the run park and resume. Reboot the machine and find the Hive running.
8. **Modules and self-release.** Check: the lead installs a module from a git URL, it shows in the modules screen, and a release of its source updates it. The Hive merges a change to itself through its own path and updates a second install.

## 16. Pitfalls

Lessons from building the first version. Each cost real time.

- Copying a login whole signs the person out: two holders of one refresh token revoke each other. Give an agent a short-lived token, read fresh for each run.
- Never let the host decide what to kill, trust or clean up from a file the sandbox can write. Decide from what the host wrote itself.
- An agent's checkout can point its git metadata anywhere on the host. Take commits out from inside the sandbox and verify they descend from main.
- A review binds to one commit. Pin the merge to it, or main moving underneath re-opens the question.
- The loop's own fixes weren't landing, because the gate couldn't run where the worker ran and workers built on a stale main and conflicted. Close the loop end to end, on the real path, before building anything on top of it.
- A daily call cap on the lead stopped it answering in the middle of a conversation. The owner's verdict was that caps like that steer the system from the shadows and break things. Caps become ledgers and alerts.
- Status lines in the chat buried the conversation, and the first dashboards ran long lines with no hierarchy, which the owner found hard to read. Status belongs on the dashboard, and breadcrumbs, collapsing and spacing are part of the first version.
- Measure the starting point before the first merge. If the start is red, measure again until there is a number, or keep and revert have nothing to compare with.
- Compare with the best kept score. Comparing with the last one lets drops just under tolerance add up.
- A release that switches while runs are live can stall the intake of new asks. A release carries live work across or waits for quiet.
- The full engine's core grew to tens of thousands of lines and stopped being readable in a sitting. Keep the core small and move everything that can be a module out.
- One access check made agents unable to message or start work across projects, and seven features quietly depended on it. Test cross-project messaging early.
- Naming projects by the last part of their repository path lets two collide. Give each project a unique name and its own folder.
- A lead cut off from all work is too rigid. Let it do small things and run subagents itself.
