# Changelog

Release notes are on the [releases page](https://github.com/ihsan-sa/hive/releases). Each one says what's new and what to do if you run an older Hive, and links to the guide for limits and tests.

A release can ship several times a day. A Hive installs each one as soon as it's out; **How often to update** in Settings can slow that to about once a day. The latest is at [releases/latest](https://github.com/ihsan-sa/hive/releases/latest).

## Finger-sized links on Chat, Progress and Settings on a phone

On a phone, the links and names on Chat, Progress and Settings are now big enough to hit with a finger. Links inside a running sentence keep their size.

## Flow keeps its height while it loads

Flow's page now opens at the height it had last time and stays there, so the chart and the list below it no longer jump down as the data arrives.

## Finger-sized controls on a phone

Buttons and links on Yours, Agents and Flow are now big enough to tap on a phone, and the rows of Agents stay as short as they were.

## The Chat badge settles before it shows

The Chat badge shows one number from the start, instead of a number that changes a moment later. It and the line on Chat's front page now wait until everything that goes into the count has arrived.

## Chat's front page waits in grey

While Chat's front page reads, grey blocks stand where the waiting rows and the focus cards will land, in place of the bare Reading line.

## The top bar is there from the first moment
The bar across the top now appears as soon as the page opens, at its full height, instead of after a blank moment. The first time a browser opens a Hive, the bar shows your logo and menu until the Hive answers, and the developer bar no longer flashes up.

## Slow pages wait in their own shape
While **Knowledge**, **Usage**, **This machine**, **Admin jobs** and **Design** read their data, grey blocks now stand where the content will land, instead of one line of "Reading…". The list in **Chat** also waits in grey until it knows your focuses, so it no longer draws flat and then re-sorts into groups.

## Chat's page links match the rest of the app, and page widths agree
**Yours**, **Inbox**, **Mail** and **Files** above Chat now look like the row of pages under Agents and Usage, with a line under the page you are on. **Yours** is now the same width as **Inbox**, and **Design** is narrower to match.

## Signing in again no longer strands you on a logged-out page
When full access ends on a Hive you reach through Cloudflare Access, **Sign in again** and **Limited · sign in for full** now take you straight to the sign-in page. Before, they stopped on Cloudflare's "You successfully logged out" page and you had to reopen the Hive yourself.

## Waits on you: the buttons line up with the time
On the **Waits on you** page, Later, Not needed and Answer now sit on the same line as the item's title and time, instead of lower down in the middle of the row.

## Chat: a file path in a message looks like a link
A project file an agent names in a message (like projects/x/notes.md) is now underlined, as attached files already were, so you can see it opens beside the chat. Before, a path the agent had put in bold looked like plain bold text.

## Settings: a fix an agent drafted is easier to read
In Settings, under Data sharing, a fix an agent drafted now wraps its long lines inside its card instead of running off the right edge. If gh isn't installed or signed in, pressing Send now says the fix waits until it is, instead of saying it goes within a few hours.

## Settings: sending a copy or everything shows it's working
After you press Send in the window for **Send a copy now** or **Send everything**, its button now spins and a line says the copy is being packed and sent, instead of the window sitting there with a greyed button.

## Settings: adding a Claude account no longer looks stuck
After you paste the code, Settings now says it is checking the account and making a short test run, instead of seeming stuck on starting the sign-in. The name box empties once the account has signed in.

## Updates go in as soon as they're out
A Hive now installs each release as soon as it's out, instead of waiting until it has been out 12 hours and taking at most one a day. A Hive that was set to about once a day when it was installed moves to every release with this update. If you picked **About once a day** yourself in Settings, it stays; picking it there again keeps it from now on.

## Chat: an agent's closing "Done:" line moves to the status bar
When an agent ends a message with a line like "Done: shipped it" (or "**Done:**"), the line no longer shows in the message, in the side list's preview or under Yours. The conversation's status bar still shows it, and so does a message that is nothing but that line. This also works when the agent names someone on a line after it. "Blocked:" and "Needs you" lines always stay. Under Yours, a wait that names another conversation shows that conversation's title instead of its id. The Chat side list starts with its sections folded on a first visit, and a focus page shows the messages there that are for you under **For you here**.

## Search by meaning in `kb find` (optional module)

The new `kb-dense` module adds a meaning search to `kb find`: passages whose words differ from the question but whose sense is near are merged into the word-search results. It runs as a small local service (CPU only, under 900 MB, a unix socket, nothing on the network) that you start with `systemctl --user start kb-dense`. Without it, or when it is stopped or slow, `kb find` answers exactly as before; `KB_DENSE=0` turns it off for one search.

## The page notices a stop or restart within 2 seconds
An open page now sees within about 2 seconds that the part of the Hive that starts agents has stopped or started again. Before, it took up to 15. The "isn't running" line and the "Taking a break" pill come and go on their own, with no reload.

## Chat: tags on messages
A message now shows small tags under it, in plain words: the goal its conversation belongs to, the agents it names and the other conversations it names. Each one opens that goal, that agent's conversation or that conversation. Search for `tag:NAME` (or `tag:agent:NAME`) in the chat list or in a conversation to find the messages carrying a tag. The simple view shows no tags. From the command line: `hive post --tag kind:id`, and `hive search tag:NAME`.

## Chat: Interrupt, and when a message reached the agent
While an agent is working, the conversation has an **Interrupt** button beside Send. If the agent is waiting on a long command, that command is stopped the way Escape stops it, so the message reaches the agent now rather than after the command ends. Every message you send now says when it reached the agent ("Reached a5c83 · 12:03"); until then it says it reaches the agent after its current step. A file sent with no words shows just the file, with no "Attached:" text, and quoting a message puts a small quote above the box, with × to remove it, instead of pasting the text into what you type. From the command line: `hive post --interrupt`.

## The page notices sooner when the Hive stops or starts
When the part of the Hive that starts agents stops, an open page now says so within about 15 seconds, down from about 35. When it starts again, the "isn't running" line and the "Taking a break" pill go within the same time, with no reload. A clean stop (Stop everything, or `systemctl --user stop`) shows at once instead of after the minute it took the old check to notice. So does a crash: the page sees the process is gone, where it used to wait the minute plus a refresh, about 72 seconds.

## Site edit mode: comment mode
On a site's /edit pages, Ctrl+Alt+M (⌘⌥M on a Mac) turns on comment mode: click to pin a comment or drag a box around something, type, press Enter. Comments stay across pages and reloads until you press Send all, which sends them together, each with a small picture of the spot.

## Slack: connect it from Settings

Settings > Modules > slack-door now has a working **Set up**: make the Slack app from the manifest shown there, paste its bot token (xoxb-) and app token (xapp-), name a channel the app was invited to, and press **Connect**. The Hive checks both tokens and the channel with Slack before it stores anything, says in words what Slack refused, and keeps the tokens on this computer, readable only by you. Everyone in that channel can then direct the Hive, so the channel can have at most 20 people. The row then says **Connected to #channel**, with **Disconnect** next to it, which stops the door and deletes both tokens. If Slack later refuses the stored bot token (revoked, or the app removed), the row says so next to **Connected to**, so a door that has stopped working no longer looks connected. The same goes for the app token (xapp-): when Slack refuses the door's connection, the row says so until a connection works again.

## Agents on a phone: what each one is on

On a phone, the Agents page now shows what each agent is on right beside its name, wrapped so the whole line reads without a sideways scroll. The other columns are still a swipe away. The totals and the cost note fold into one line, one tap to open, so the page opens on the agents.

## Research tools come with your Hive

Agents now have `research`: it searches many engines at once, reads pages through a browser or an archive copy when a plain request gets no text, digs through results round by round, and checks each claim against a page it saved, so a list says which rows are backed and stops short rather than padding. It also does source analysis and forecasts with backtests. It needs no API key, uses the network only when an agent runs it, and `hive module remove research` keeps it out.

## Agents: the On column names the run

An agent that hasn't said what it's on no longer shows its brief's instructions in the On column: it now reads "Keeper round (periodic)" or "On a phone call", with the full text in the tooltip. An agent woken by another agent's post, or by a short question from you that names no work ("what is this about"), shows its conversation's status there instead of that post. On the Work page, Other projects has its space under the last card.

## Calls: you can hear the agent again

A change to the call's speed slider left the agent's voice silent in the browser, though the hold music played and the agent could hear you. The voice is back, keeps its pitch at any speed, and falls back to another way of playing if a browser refuses the first, so a call no longer goes quiet.

## Flow: the work as a live timeline

Flow, beside Work under Chat, draws the last hours of every project as lanes: one bar per agent's run, a diamond where an agent handed off, a tick where a result or an ask landed, a green line at now with what each project does next. It is built from what the Hive already writes, so agents have nothing new to do. Zoom from two hours to a day; a lane opens the project's Work card, a bar opens the agent.

## Agents say what they are on

`hive me "..."` lets an agent set one dated line on what it is broadly on. The Agents table shows it as an **On** column, the Now page and the agent's own page lead with it, so you can find the agent already on the thing you want and message it instead of starting another, or see when one has drifted from what you asked.

## Codex runs can't reach blocked GitHub orgs

A Codex agent now gets the same check as a Claude agent before a shell command or web fetch, so an org your Hive blocks stays blocked when work falls back to Codex.

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

## dash: smarter "not needed" greying
The thread importance pass now re-reads threads as they age and sees how old each post is, so delivered and finished threads get greyed instead of lingering. A thread that names videos, PDFs or other deliverables is never greyed. A thread waiting on a "For you:" answer is no longer archived by age.
