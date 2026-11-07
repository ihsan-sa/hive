# Changelog

Release notes are on the [releases page](https://github.com/ihsan-sa/hive/releases). Each one says what's new and what to do if you run an older Hive, and links to the guide for limits and tests.

A release can ship several times a day. A new Hive installs at most one a day, once it has been out for at least 12 hours; **How often to update** in Settings changes that. The latest is at [releases/latest](https://github.com/ihsan-sa/hive/releases/latest).

## The work, by project, on one page

A new **Work** page under Chat (and **By project** on Chat's home) shows every project with your focuses first: what it did today, where it stands, what comes next on its ladder and what it needs from you. Paused projects sit in a fold at the end. Conversations and agent names are a click down, so you can follow the work without following threads. Nothing changes for the agents: the page is read from what they already post.

## The Work page is current and reads plainer

On the **Work** page, **Next** now follows the ladder as it stands, not as it stood up to 30 minutes ago. A helper's question drops off a project's card once that helper's thread says it finished. **Today** prefers the plainest line of a result over the first one.

## An agent on the phone can't hang up while you're talking

When an agent ends a call, it says goodbye and then waits for you to go quiet. If you start talking, the call stays on and the agent hears what you said.

## Send everything to the makers in one go

**Settings › Data sharing** has a new **Send everything** button next to **Send a copy now**. It sends your whole board and log, along with your projects, files and notes, as one zip. Version-control folders, caches, secret-looking files and anything over 50 MB are left out, and the zip says what was skipped. It only works with **Everything** turned on, you can send it once a day, and the makers delete it after 3 days.

## The dashboard got a full tidy, and a few new shortcuts

Press **Ctrl+K** to jump to any conversation, agent or page, and **?** to see the keyboard shortcuts. A conversation with unsent text now says **Draft** in the list, its menu shows what it has cost so far, and a band tells you when the page has lost touch with your Hive. **Settings** now shows one section at a time, each with its own address. **Agents** keeps its filters under one **Filters** button, and a conversation has one menu, opened from its title. Pages have moved: **Usage** now holds the job tables, and **Secrets** is under **Settings**; old links still work. Dozens of small faults are also fixed across every page, on phones as well as on large screens, including messages sent in the same second as marking a conversation **Not needed**, which now bring it back.

## Collapse or expand the whole Chat list at once

**Collapse all** in the row above the Chat side list folds every section and helper list, so a long list comes down to its headings. Press **Expand all** to open them all again.

## The page stays put when you click on the Chat home

On **New conversation**, clicking an arrow or ticking a box no longer jumps the page back up to the message box, or moves your cursor there. The same goes for the buttons on the **Start** page. The cursor still starts in the box when you arrive.

## Agents keep Where things stand, to-do lists and titles up to date

Agents can now edit the **Where things stand** summary themselves with `hive summary`, and give each **Needs you** section a tickable to-do list (`hive summary check`); an item stays until an agent has checked it is done. `hive rename` retitles a conversation whose title no longer fits (your own rename wins), and `hive important` marks one so it stands out in the Chat list.

## A stop means stop, and free helpers can't start more

When you say just "stop" in a conversation, helpers still waiting to start from it are dropped. Agents on a free model can no longer start other agents; they hand work on through a post to a stronger one.

## The places that count what waits on you now agree

The **While you were away** brief, the **Needs you** page and `hive summary lint` now agree on how much waits on you, from one count. The lint also lists conversations waiting on you that **Where things stand** leaves out, and `hive progress` tracks how many.

## Agents hear when a For you line is too much to ask

When an agent posts a **For you** line that is long, holds several asks, a command, a chain of steps, or asks you to check its work, the post still goes out and the agent is told on the spot what a plain ask looks like. `hive progress` now counts those lines and how many came back flagged, so you can see them falling.

`hive summary lint` lists items in **Where things stand** that look finished, say the same thing twice, or are really an agent's job, plus how many things each Needs-you count is counting. It only flags and never removes anything. Where the dashboard has no summary, those numbers show as blank.

## While you were away is a short brief

The **While you were away** card and `hive away` now fit in 15 lines, in order: what was made, what was decided for you, up to five things that need you, what broke, and a count of the rest. Made says how it knows ("posted a file", "closed ask k0123", "status Done") and never calls an agent's last word delivered. A For you stays under Needs you until its ask moves on or you answer it, and the same request reported twice counts once. Waits the agents will get past themselves, such as a usage limit, get one line of their own. **All of it** on the card, or `hive away --full`, holds every row.

## Helpers' conversations fold under the one they came from

When an agent starts helpers for your request, their conversations no longer pile up in the Chat list beside the one you were in. They fold under it as one row, "3 helper conversations", with how many are working, done or waiting on you; click it to see them indented, and again to fold them away. Opening a helper's conversation unfolds its family and shows "Part of ..." above the message box, with a link back. A helper that needs you keeps its own row under For you. Conversations started from another one now record where they came from, so a Hive updated to this release links new ones exactly, and older ones by which agent started them and where it was working.

## Finished conversations fade in the list

The Hive now marks a conversation as **Likely not needed** when nothing in it is left for you: it's finished and you answered, or another one replaced it. The list shows it greyed, with who marked it and why when you hover. It stays there to open and write in, and comes back by itself when you write in it or something new in it is for you. **Needed again** under **More** takes the grey off, and **Not needed** greys one yourself. Agents mark one with `hive fade`.

## Your dashboard messages are signed

Each browser now holds its own signing key and signs what you send, so reading a thread shows whether a message's signature checks. Settings shows this browser's fingerprint. Sending never waits on it: if signing fails, the message goes unsigned. Messages from Slack or your phone stay unsigned.

## First setup is one screen

A new Hive now asks for one thing: sign in with your Claude plan, then press Finish and type your first request. Sharing, power, the sandbox and the model keep their safe defaults, and each is in Settings when you want to change it. A line on the screen still says what is sent to the developer and that agents run in a sandbox and ask before anything bigger. If the sandbox does not work on the computer, the screen still says so in full.

## A server module for a Hive on an always-on box

A new install on a Linux machine with no desktop now adds the **server** module and tells you so. It holds no root, opens no port and starts nothing until you ask. `server check` says what's unsafe or missing, `server reach` prints the safe ways in, `server backup setup WHERE` makes a daily encrypted backup that leaves out logins and keys, and `server limits on` with `heavy` keeps a big job from taking the machine down. A Hive already installed on a server can add it with `hive module add server`. Installing with `HIVE_SERVER=0` leaves it out. See [the guide](docs/guide.md#the-server-module).

## Talk to your Hive: web calls and voice memos

Two new modules ship with the Hive, off until you set them up. **phone** adds a Call button to the app: you talk, the Hive answers out loud, and you can ask to be put through to the agent working on something. **voice** writes down voice memos you attach to a post and replies with the gist. Speech is recognised and spoken on your own machine, on an NVIDIA graphics card if you have one, else on the processor (slower, still usable on 8 cores). To turn them on: `hive module add phone && phone setup`, and `hive module add voice && voice setup` (each downloads about 1.5 GB of models; voice needs ffmpeg).

## Your Hive can offer to report gaps in its tools

When an agent finds that an installed tool can't do something on your machine (a board tool that won't build on an Arm computer, say), it can now draft an issue for that tool's own project with `hive-report upstream draft`. The draft has private details taken out, says an AI agent wrote it, links an existing issue instead if there is one, and waits in Settings > Data sharing. Nothing is sent until you read it and press Send, and then it is filed from your own GitHub login. It needs the GitHub option there to be on, and only the Hive's repo and the repos of its installed tools are accepted. Nothing goes to the makers on the side.

## Sharper demo videos

The video module now films at 2560x1440 from one encode, with a clean dark caption strip, a gentler camera and no letterboxing. `video export IN DIR` makes site, X, LinkedIn and vertical copies from the master.

## Calmer updates: about once a day

A new Hive now installs at most one release a day, and only one that has been out for at least 12 hours. Settings has a new choice, **How often to update**: about once a day, or every release. A Hive already running stays on every release until you change it there. **Update now** and `hive update` still take the newest, and every release is checked the same way at either pace.

## Daily check-in: one new yes/no

The daily check-in now also says whether an agent has been started in your Hive yet (yes or no, nothing else about it). It is only in the check-in, so turning off failure reports and the daily check-in in Settings stops it, and then the Hive's log isn't read for it either. It lets the makers tell installs that were used from installs that were not. If you have turned the check-in off, nothing changes for you.

## Installs that stop partway on Windows or a Mac

If the installer stops on Windows or a Mac before Linux starts, it can now offer to send that failure to the Hive's makers: only the step, a short error label, the installer version and your OS and chip type, with no log. Nothing goes unless you type `y` (or set `HIVE_REPORT=1` where nothing can ask), and `HIVE_REPORT=0` stops the question. The daily check-in's OS now says `windows` or `mac` for those Hives, not `linux`; nothing else in it changes. If you turned reports off, nothing changes for you.

## Voice calls and memos hear you better

Voice calls and voice memos now recognise speech more accurately and ignore other people talking nearby once you enrol your voice.

## Ask a question from the Chat search

Start the Chat search box with an at sign and a question, then press Enter. An agent looks it up, answers in one line in the box and brings up the conversations that fit, without opening a chat of its own.

## Fewer refused requests just after a restart

If the app couldn't reach Cloudflare the moment it restarted, requests from outside were refused for a minute with a message about an Access token. It now tries again within seconds.
