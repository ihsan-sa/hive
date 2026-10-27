# The control center scrolls, collapses and groups right

Issue #139. Spec §11, "Rules for the whole UI": everything is expandable and
collapsible level by level, with one key going deeper and one coming back; no
line runs off the screen; information is grouped so a screen reads at a glance.
The pieces were there, but several edges broke those rules. This is what was
wrong for the person, what changed, and what is left for them to decide.

## What it does for the person

**Scrolling goes where you point and stops where the text does.**
- The mouse wheel scrolls the pane under the pointer. Before, it scrolled
  whichever pane had the keys, so wheeling over the thread while typing in the
  channel moved the wrong one, and the wheel did nothing on the dashboard,
  Waiting, Modules or Settings.
- Over a list (the feed, the status tree, Waiting, Knowledge, Modules, Settings) the wheel
  moves the cursor one row a notch, and the pane takes the focus so the cursor
  shows. Over the sidebar it does nothing: moving that cursor changes what the
  main pane shows, which a stray scroll shouldn't do.
- A detail (an agent's log, a task, a sweep, a diff) scrolls to its last full
  page and stops, instead of scrolling the text away to one line and leaving
  ↑ presses that do nothing. Home and End (or g and G) jump, and the header
  says `21–40 of 112` when there's more than a screen.
- In a chat, PgUp, PgDn, Home, End and the wheel scroll the channel even with
  a thread picked. Before, the picked thread pulled the view back on the next
  frame, so you couldn't read above it. Moving the pick (↑↓) or opening a
  thread brings it back into view.
- The Modules list keeps its cursor on screen when there are more modules than
  rows.

**Collapsing is one level at a time.**
- In the sidebar, → opens a closed project group, then steps into its first
  chat; ← goes from a chat up to its group, then closes it. Space and Enter
  still toggle a group. Before, → on an open group closed it, and ← did
  nothing.
- A collapsed group shows ● when one of its chats has news, so collapsing a
  project never hides that something arrived. The ● now also shows on the row
  under the cursor.

**The cursor stays on what you picked.**
- The sidebar and the status tree keep their cursor on a row by its key, not
  its position. Agents start and end all the time, adding and removing rows
  above, which used to slide the cursor onto another chat; Enter then focused
  a chat that wasn't the one on screen. A row hidden by collapsing its group
  puts the cursor on the group. A run listed twice in the tree (under its task
  and under agents) keeps the copy nearer the cursor.

**Groups don't lose chats.**
- An agent whose project isn't in the state (removed, or not loaded yet) gets
  a group under its project's name instead of vanishing from the sidebar.

## How it fits

All in `internal/tui`; no API, daemon, sandbox, self-update or login change.
The key hint lines are unchanged (the tmux e2e reads them); the help screen
gains ← →, Home/End and "the wheel scrolls the pane under it".

## What it leaves out

- The dashboard tree still lists a project's work under "work in flight", not
  under each goal as §11 says. Tasks don't carry the goal they serve, so that
  needs a data change first, not a TUI one.
- The sidebar has no wheel scrolling of its own (see above). With many agents
  it windows on its cursor, as before.
- No scroll bar: the "x–y of n" note is the position.

## Open questions for the person

1. Should the Hive's own group (`hive`, with the workspace lead) sit first in
   the sidebar, above the projects? Today it comes last, after every project,
   though it's the chat the control center opens on.
2. Should a collapsed group remember being collapsed across restarts (in
   `ui.json` with the seen sequence)? Today every group opens again.
3. Should ended agents' chats (`(ended)`) fold away under their own row once
   a project has many, rather than listing each one?

## Follow-up: issue #384

The person's reports from the control center, and what each now does:

- **"It's not possible to scroll up in a thread."** The thread's reply box has
  the keys, so ↑ went into an empty box and did nothing. With nothing typed, ↑
  and ↓ now scroll the thread three lines a press (as a wheel notch), PgUp and
  PgDn a page, and Home and End jump; with a draft they move in it as before.
- **"A shortcut to go back to the bottom of a channel."** End (or G) in the
  channel goes to the newest message and drops the pick, so nothing pulls the
  view back up and it never opens a thread; ctrl+end does the same from any
  box, a draft kept. End and newest are on the hint line.
- **"Sends two messages at once, then the second disappears."** The refresh
  every 1.5 seconds could bring a message back before the post that sent it
  answered; the answer then added it a second time, until the next refresh.
  Now a message is added once, a refresh that left before the post can't drop
  it, and a reply a refresh already brought no longer also shows as sending.
- **"The last text is right above the box."** Content cut to keep a long
  picked thread in view ran into the box. Two blank rows now always part a
  channel from its box, one a thread from its box.
- **Expand or collapse by level.** e opens one level more of the page and E
  (shift+e) closes one: the status tree level by level (every node on the
  shallowest level with a closed one), a channel's threads all at once (it has
  the one level), the sidebar's project groups and then their ended-agent
  folds.
