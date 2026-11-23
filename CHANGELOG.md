# Changelog

Release notes are on the [releases page](https://github.com/ihsan-sa/hive/releases). Each one says what's new and what to do if you run an older Hive, and links to the guide for limits and tests.

A release can ship several times a day. A Hive installs each one as soon as it's out; **How often to update** in Settings can slow that to about once a day. The latest is at [releases/latest](https://github.com/ihsan-sa/hive/releases/latest).

## Smaller fixes, 2026-11-23

- The release check no longer picks a sign-in with no room left in its 5-hour window.
- The README and the website now explain the six ideas behind the Hive, each in one line with the details folded underneath.

## Markdown files as pages you can mark up and edit

A Markdown file opened in the preview panel now has three tabs. **Page** sets it out as printed US Letter pages in a book face, with page numbers, and **Save as → PDF** prints those same pages. **Text** is the plain rendered view. **Edit** opens the text in an editor, with the pages beside it when the panel is wide. The **Box** tool works on the pages: draw a box or select words to leave a note. **Edit** appears for notes and project files the web app may write. It saves with Ctrl/⌘+S. If the file changed on disk while you were editing, it says so and keeps your text, and you can save over the change. Closing the panel or leaving the page with unsaved text asks first. Files with Windows line ends keep them.

## Smaller fixes, 2026-11-21

- A message sent to an agent while one of its helpers is working now reaches the agent itself.
- Reminders now honor `--to NAME` immediately after the time; previously those words could become message text and the reminder went to the person.
- The hourly grader no longer drafts notes that merely restate a code fix or contain dates and other day-bound numbers.
- hivelab.ca has an `llms.txt` guide to the site for AI tools.
- A pause with no same-day clock deadline no longer expires on its own after a fixed number of days.
- A message of yours to an agent no longer gets a pointer to weak knowledge-base matches that nobody opened.

## New module: inquiry

**inquiry** is a set of tools for an investigation of a large release of PDF documents. It gathers the documents, searches them with page citations, and keeps a store of sourced facts. Each fact has a quote that the tool checks against its page, says how it was read, and carries a reading confidence. A person's standing (accused, named in a civil filing, charged or convicted) needs a fact with a confidence of 0.75 or more, and a face match stays a lead until a document backs it. Victims are known by number, under the record's own label. A victim gets a name only from a fact that has the name as whole words and shows that the person speaks publicly under it. Pages flagged as possibly showing a victim are never rendered, and no command tries to undo a redaction. Add it with `hive module add inquiry`. It needs `curl`, and `pdftotext` and `pdftoppm` from the poppler-utils package.

## Every running Claude and Codex process shows, with who started it

The helper lists now also show the Claude and Codex processes running outside an agent's own run. That covers a one-off `claude -p` or `codex exec` from an agent's shell, a script's model call, a session open in a terminal, and the copy kept warm for the next new agent. One that an agent started sits under that agent with its helpers. One that no agent started is listed on **Now** under **Also running, not started by an agent**, and in **All agents**, with what started it in place of an owner agent. Its drawer gives its folder and process number. None of them count as agents. `python3 ~/core/subagents.py --running` lists the same in a terminal.

## Helpers name their owner agent and show on the Map

In **All agents**, when **Show** is **Helpers** or **Both**, each helper row has an **Owner agent** column that names the agent that started it. The helper drawer has the same field. On the **Map**, an agent with helpers running has small grey dots on its card, one for each helper, and its side pane lists them under **Helpers**. The dots don't show when the Map is zoomed out to tiles. Helpers are still never counted as agents.

## The dashboard shows each agent's helpers

The helpers (subagents) an agent starts inside its run, in Claude Code or Codex, now show in the web app. The top bar reads "5 agents working · 3 helpers". On **Now** each agent's helpers sit under it, and the Today card counts the helpers that finished. **All agents** has a **Show** switch for **Agents**, **Helpers** (the last 7 days, each with its agent) or **Both**; on a phone it is in **Filters**. An agent's page lists its helpers. Helpers are never added into an agent count. A helper's cost is already part of its agent's, so **Total cost** is the same whatever you show. A Hive whose agents start no helpers sees no change.

## lab loads training checkpoints and random weights

`lablib.load(name, revision="step1000")` loads a model at one of its published training checkpoints (Pythia has `step0` to `step143000`), and `lablib.load(name, random_seed=0)` builds the same model shape with random weights from a seed: the same seed gives the same weights. Cached activations and fitted lenses are keyed by checkpoint and seed, so they never mix with the trained model's, and the published lens is not used for either. Pythia models now load (a lens layout mismatch with newer transformers is handled). `examples/checkpoints.py` shows two checkpoints and a seed twice; run it with `lab run examples/checkpoints.py`.

## Smaller fixes, 2026-11-18

- `hive fresh` lists each downstream doc or copy with its source and fails when the source changed after it.
- A project that already has a list of next steps is no longer treated as having none right after a build, so its open steps keep getting rounds, and a conversation gets one round at a time, not two.
- The improver can run on a model of its own, set in the config, and its brief is one short line again.
- On a Mac, the install no longer sits silent after "The sandbox works" while voice calls build in the background.
- A focus whose remaining steps all wait on a date now gets one round a day that asks what else would move it, instead of standing still until the date.
- A focus whose last post came from a timer script is woken again: its wake-up was addressed to the script name, which starts nobody.
- The starter no longer stops for good when a power cut leaves one of its files empty, and if it keeps stopping on an error it tells you.
- The diagnostic pack you can send now also carries the end of the sandbox helper's log, which logins are paused and since when, and which release is installed, so a stuck install can be diagnosed faster. Nothing with keys or tokens goes in.
- When every login is paused, the notice now says what really happens on your install (no more claim of a Codex fallback you don't have), and a pause that flips on and off no longer posts again and again.

## Why a board's Wi-Fi link dropped

The remote bench's log for a board that dials in over Wi-Fi said only "the link dropped". It now says why: the board closed the link (with its close code), the socket ended with no close, nothing came from the board for 90 s, or a socket error (with its errno). When a board dials in again while the bench still holds its old link, the log says so and how long ago the old link was last heard from.

## Firmware: coverage, fuzzing and fault scenarios in QEMU

The fw module, which builds and tests ESP32 firmware, gives every ESP-IDF project three more kinds of test, with nothing to write but a few lines in its `fw.json`:

- `fw coverage` builds the host unit tests again with gcov and says which lines, branches and functions of the portable code they never reach.
- `fw fuzz` runs libFuzzer harnesses (clang, with AddressSanitizer and UBSan) over the code that takes input from outside: a console, a JSON API, a signed message. `fw fuzz --new NAME` writes a harness to start from, and the dictionary is made from the strings the code compares against. On the hifi receiver's firmware the first runs found five bugs, each now fixed with a test.
- `fw qemu --scenario NAME` boots the real firmware image in QEMU and injects a fault while it runs: a sagging supply (a brown-out), a lost USB-C power contract, a stalled audio output, a full settings flash, or RAM squeezed nearly to nothing. Each scenario then checks over the network and the console that the firmware copes and recovers. Small stand-ins for the ADC, GPIO inputs and the heap are linked into the QEMU build only, so the board's own build is untouched.

## Agent cards and small links on a phone

On a phone, the card of an agent that had not said anything yet (the warden's, most often) squeezed the agent's name to nothing and put its status word over its picture. The name now has its own line again. A long agent name on the Agents page ends in "…" at the edge of the card instead of running under the next one, the link under each thing the Hive remembers (in Settings) is easy to tap, and on a focus card the chip in its first lines is no longer covered by an agent's name on the line below.

## A conversation with no summary still says where it stands

A conversation that nobody had written a summary for showed no word at its top and none on its row in the list, so you had to open it and read to learn that it was over, or that your own message was the last thing in it. Such a conversation now says **Quiet** with the one thing known about it: who wrote last and when ("An agent wrote last, 2d ago"). When you wrote last and an agent read it but wrote nothing back, it says so.

When you wrote last and no agent has started on it, the word is **Not picked up**, so a message that went nowhere is easy to see. It is said only when that is certain: the message asked for an agent of this Hive (or a new one, or nobody by name), at least three minutes have passed, no agent was at work there when you wrote, and the page holds the whole log. A message that is only "stop" is never counted as not picked up, since it starts nothing on purpose.

## A conversation says where it stands, at its top and in the list

Opening a conversation used to mean reading its last few messages to learn whether it was finished, still being worked on, or waiting on you. The top of a conversation now says it before any message: a mark and a word (Needs you, Working, Stuck, Failed, Paused, Waiting on others, Done, and a few more), then one line on where it is, then a second, quieter fact such as what was asked or what the summary said. A click on the top opens the whole summary under it.

The list of conversations now carries the same mark and the same word on every row, so a row never says less than the conversation it opens, or something else. On a busy Hive more than half the rows said nothing where the conversation itself said Done, Waiting on others or Needs you. A row says **Needs you** exactly when the count at the top of Chat includes that conversation, so the count, the group a row sits under and the word on the row cannot disagree. The line that counts an agent's helpers ("2 working, 1 needs you, 3 done") counts them by the same words, and a phone's home shows the word and the one line under each title.

Some words changed with this. A row that said "Waiting for your answer" now says **Needs you**. One that said "Stopped with an error" says **Failed**, and the top of the conversation says when it failed and what wakes it. In Simple view a row's own words ("Working on it", "Taking a break", "Ran into a problem") gave way to the ones above, so the list and the conversation say one thing in both views.

A summary often names other agents by their five-character codes, and the page says each one by what it is working on. It now leaves such a code as written when it is part of a file name, a folder, a command or a web address. When a summary names two agents that have no name of their own, it says "an agent" and "another agent", so two helpers no longer read as one.

## A paused conversation says when your message will be read

When an agent stopped at a usage limit, or lost its connection and was due another try, the conversation said it would carry on by itself and added "A message wakes it sooner." That was not so. The Hive holds such a run until the limit resets or the next try is due, and a message sent in the meantime waits for it. The page now says what happens: while a message of yours waits for a paused agent, the line over the message box ends with "It reads your message then.", and the top of the conversation says the same. The **Carry on** button is not offered while your message already waits, because pressing it only added a second message to the same wait.

## An agent that will carry on by itself no longer shows as Failed

When an agent lost its connection to Claude (or the machine restarted under it), the Hive said it would carry on by itself, but the conversation showed the same run as **Failed** with "a message wakes it to try again" right underneath. Now the conversation says it stopped, why, and that it carries on by itself; the bar reads **Paused**, and it isn't counted as needing you. If the Hive gives up after several tries, it shows as Failed as before.

## A button that wipes the knowledge base, with a way back

Settings has a new last section, **Wipe the knowledge base**, for when a Hive should start a piece of work without what its agents learned before. It moves every note in the knowledge base into a private copy on the same machine. Your priorities and your standing directions stay where they are, and so do the board, the projects, the core and the modules. Nothing is deleted: the section lists the copies from earlier wipes, and **Put back** merges one in again without overwriting a note that is there now.

It is made hard to do by accident. The window has three steps. The first shows which Hive this is, its folder, how many files move, what stays and which agents are running. The second has three boxes to tick. In the third you type the Hive's name by hand, and the last button stays off for ten seconds. Nothing is sent before that last button, and a put back asks for typed words too.

The section shows only to the owner's full sign-in at the Hive's own address, with the files and settings rights: not to a guest, a shared link or a site's own name. From outside the machine the sign-in must also be from the last 15 minutes, and the window offers to sign in again when it is older.

Agents are told on the board when a wipe or a put back happens, and search stops returning the moved notes. A wipe that can't move an entry stops there and says which one, and what had moved stays in the copy, ready to put back. If the answer gets lost on its way to the page, the window reads the list of copies and says what that shows; it says nothing was moved only when the Hive refused.

The copy still takes its space on the disk, and a backup of the machine made before the wipe still holds the notes: this is a fresh start for the agents, not a way to erase what was written.

## The home lists your focuses by where they stand

With many focuses, Chat's home was a wall of cards, and finding the one that needed you meant reading all of them. With seven or more focuses the home now lists them instead, one row each, grouped by where each one stands: Needs you first, then Blocked, At risk, On track, Waiting on others, Paused, Done, and No state yet. A focus goes under Needs you as soon as an ask in it waits on you, whatever its In short says.

A row is the focus's name, its In short sentence, and only what you would act on: how many lines are new to you, how many asks are yours, and how many agents are working. **List** and **Cards**, beside the Focuses heading, switch between the rows and the cards, and the browser remembers your pick. With fewer than seven focuses the cards stay the default. The first few times the list appears by itself, one line says why and where the cards are.

The agents' own check of the cards finds more of what reads wrong to you: a card that points at a date already past or still says "today" a day later, an In short that says Needs you when nothing is listed as waiting, and a step that is on your to-do list twice or that nothing else tells you about any more. An agent that ticks a to-do it had put on you, or closes a For you line, is told which other place still says the step waits on you, so the two are closed together.

## Each focus says where it stands and what is new to you

A focus card on Chat's home was a column of status lines of equal weight. Nothing said whether the focus was going well, or which lines you had already read. Each card and each focus page now opens with **In short**: one or two sentences from the agents on where the focus stands against what it is for, with its state as one word at the top of the card (on track, at risk, blocked, needs you, waiting on others, paused or done). If you read one thing on a card, read that.

A bar at the left edge of a line means it was written since you last looked, and the head of the Focuses section counts the focuses with such news. A card with an In short shows only a few of its lines, the new ones first, and links to the rest. A focus page says the same in one sentence under its In short ("New since you last looked 22h ago: 2 in Now, 1 in Lately; 3 conversations have new messages"), and pressing a part of that sentence takes you to those lines.

The word is only as fresh as the sentence under it. When the state changed since you last looked, the card says what it was ("at risk, was on track"). When nobody has said the In short again for more than a day and a half, its state word turns grey with its age beside it ("said 2d ago"), so an old verdict does not read as today's. A look counts once you have stayed on a page for four seconds: passing through a page marks nothing as read.

A step only you can take that an agent wrote among its status lines, instead of under Needs you, is now listed with what waits on you, so a card no longer says nothing waits on you above such a line. Where a card already lists asks, it says how many more lines may be yours. These are found by their wording, so the page says "reads as yours" and does not call them asks.

Agents set the In short with `hive focus lead FOCUS "On track: ..."`, and `hive focus lint` names a focus that has none, or one that has gone old.

## The Agent comms button is always in Chat's Show menu

A browser that had never turned Agent comms on (a new computer, or the app at a new address) left that button out of Chat's Show menu, and in simple mode there was no other way to turn it on. The button is now always there next to For you and Updates. It still starts off.

## Two new modules: lab and lit

**lab** is a workbench for quick experiments on small open language models. An agent can read a model's internal state with a lens, fit a probe, steer the model, or ablate and patch an activation, and each of these runs with its control built in. It also brings `askbench`, for behaviour tests on a Claude model, and `npedia`, a command-line client for Neuronpedia. Add it with `hive module add lab`, then run `lab setup`, which downloads PyTorch and two small models (about 9 GB of disk; a graphics card is optional).

**lit** searches, fetches and reads ML and AI-safety papers from the command line, and checks a quote, a claim or a reference against the paper's own text. Add it with `hive module add lit`. Reading PDFs needs `pdftotext`, from the poppler-utils package.

## Web search puts the page you meant first

The research module's merged search ranked a page that repeats the words of your query above the page those words name: a paper's own page or a project's repository often came second or third, behind posts about it. The order the search engines give now stands once a result speaks to the query at all, and word overlap only sinks results that barely fit. On 24 fresh lookups the right page came first in 22, up from 11.

Checking a person's post against a page also reads "Founder, CEO" as CEO, and no longer takes "Name at the Mathematics Genealogy Project" as an employer.

## Tall videos show whole in the preview

A video taller than the preview panel, such as a phone clip or a 9:16 story, ran off the bottom of the panel, so its lower part was cut off. It now fits the height of the panel, the way a picture does.

## Modules update by themselves

A module you added from the catalog now takes new releases on its own, the same way the Hive does and at the same pace. Each release is signed and passed its checks first. A module with your own changes in it is left alone, and one that fails to start after an update goes back to the version it was running.

## The circuit board module is now pcb-flow

The module that designs circuit boards was called `hwde`; it is now `pcb-flow`, to sit beside `chip-flow`. Its command is `pcb-flow`, and `hwde` still runs it. A Hive that added it under the old name keeps its folder and shows it once, as `pcb-flow`; nothing needs doing.

## Choose how hard an agent thinks

The message box has an Effort menu beside the model, and an agent's page has one in its Effort row. A choice holds on that agent from its next run, because a run's effort is fixed when it starts. On a running agent's page, **Restart now** asks it to finish its current step and come back in the same conversation at the new effort. The menu is off for agents on other providers, which don't take an effort.

## Knowledge, Chat and Yours open faster

On a Hive with a large archive, Knowledge could sit on grey rows for 8 seconds while it listed every folder. It now shows the last list at once and refreshes it in the background. Chat and Yours also draw 3 to 4 times faster on long histories, and search still looks through a list at most a minute old.

## Settings, Inbox, Design and Start hold still while they load

These pages started in the wide column and moved across to the narrow one when their data arrived, a jump of 270 px on a wide screen. They now start in the narrow column, and so do their loading and error messages.

## Needs you rows read in plain words

Rows on the Needs you page and the home could show ask codes such as `k0863` or "(an ask, an ask)", and some ended with a raw thread name. The codes are gone from every row, and a thread named at the end of a row now appears as that conversation's title beside the time.

## The Chat badge is right as soon as it appears

The number on the Chat badge could be one short for about a second and a half after the page loaded, until the next update corrected it. It now shows the right count from the moment it appears.

## Agents list holds still while it loads

The long list on the Agents page no longer shrinks for a moment when the older half of the history is slow to arrive, and the other pages that wait for the whole history no longer treat it as complete early. Narrowing the window after the page has loaded now switches to the shorter phone list, and widening it again brings the full list back.

## What waits on you counts acts, not threads

The summary of what waits on you now groups threads that one act would settle, so one payment or one call is one line. A line that names no money, account, voice, device or root work now draws a warning to decide it instead.

## What waits on you stays true

The summary of what waits on you is now checked against the threads each time it is rebuilt. Anything still open comes back even if someone removed it from the list, and something finished leaves on its own. Agents can take back a For you line that no longer applies, and one repeated in new words now counts once.

## Phone pages start sooner

On a phone, Timeline and the Map keep one row of controls above the chart, with the rest behind an Options button that also shows when a hidden filter is on. The Map now comes before its summary, the first section of a page sits right under the title, and every Settings page shows without scrolling sideways.

## Chat and Yours stay still as they open

The Chat and Yours pages no longer drop down when the app finishes loading. The first visit in a browser can still move once, because it has nothing remembered yet.

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
