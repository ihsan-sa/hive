# Claude Design brief: the Hive's control center

This folder is the package to give Claude Design: this brief and `frames.html`, which is every screen of the control center as the code draws it today, on a dark terminal and a light one. Upload both. The handoff Claude Design produces comes back to the Hive, and an agent implements it in `internal/tui`. A handoff in the form asked for at the end can be built as drawn.

The package is kept current by the code. `frames.html` is generated from the golden frames in `internal/tui/testdata`, and a test fails if either is out of date with the code, or if a frame isn't described below. When a change to the UI adds or changes a screen, the same change regenerates the page (`HIVE_UPDATE_FRAMES=1 go test ./internal/tui -run TestDesignFrames`) and updates this brief.

## What the product is

The Hive is a small team of AI agents (a lead per project, workers, reviewers, wardens, tribunals) that lives in a folder on the person's machine and works on their software projects. It runs on its own as a daemon. The control center (`hive`) is the person's window onto it: one person, often over SSH, often in an 80×24 window beside an editor. It is the face of the product (spec §11).

What the person comes to it for, most-used first:

1. **Talk to the leads.** Each project has a lead, and the person gives it work and answers its questions in a chat. A chat has threads, so an ask and everything that follows it stay together, as in Slack.
2. **Answer what's waiting on them.** Permissions a warden refused, questions, budget asks. These are the only things that block the Hive on the person.
3. **See what the Hive is doing right now.** The dashboard: a live feed of who said what to whom, spend and activity, and a status tree of each project's goals, work in flight, score and agents.
4. **Read agents' feedback about the Hive itself**, and pass it to the lead or set it aside.
5. **Look things up** in the knowledge base. **Change settings and modules**, rarely.

Quitting never stops anything. When the control center opens again, it shows what happened since.

## Hard constraints: it is a terminal UI

Whatever is designed must be drawable in a terminal grid. Designs that can't be will be approximated, so design within these:

- **A grid of monospace cells.** No pixels, no images, no variable type sizes, no rounded panels except box-drawing characters (`╭─╮│╰╯`, `─ │ ┼ ┴`). Glyphs in use: `› ‹ ▸ ▾ ▌ ● ◌ ← → ↑ ↓ · … ✗ [x] [ ]`. A glyph must be one cell wide in common terminal fonts.
- **Sizes.** It must work at **80×24** (the smallest it's designed for) and at **120×36**, and degrade without crashing down to 30×8. At under 120 columns a thread opened beside a chat takes over the screen instead of splitting it. Show every new screen at both sizes.
- **Styling is bold, faint and reverse, with hints of colour.** Colour carries meaning, never decoration, and every colour has a dark-terminal and a light-terminal shade:

  | meaning | dark | light |
  |---|---|---|
  | failed (red) | `#E5534B` | `#C4352B` |
  | running, kept (green) | `#57AB5A` | `#2A7A3B` |
  | waits on the person (amber) | `#DAAA3F` | `#9A6700` |
  | accent | `#39C5CF` | `#1B7C83` |
  | the person ("you"), bold | `#6CB6FF` | `#0969DA` |
  | the lead, bold | `#DCBDFB` | `#8250DF` |
  | other agents, bold, one hue per name | `#F69D50` `#56D4DD` `#FC8DC7` `#8DDB8C` | `#BC4C00` `#1B7C83` `#BF3989` `#2A7A3B` |

  Inline code is ANSI yellow and links ANSI blue, underlined, so the terminal's own theme decides their shade. With `NO_COLOR` set, every hue goes: what was coloured to stand out is bold instead, so a design must still read in bold, faint and reverse alone. The terminal's own background and foreground are the canvas; the UI never paints a background, except reverse for the picked option.
- **Keyboard first; the mouse wheel scrolls the pane under the pointer.** Every action has a key, and the bottom row lists the keys for where the focus is, dropping pairs from the right that don't fit.

## The shell every screen shares

```
 hive                 │   <title>  <faint note>                       <faint right note>
──────────────────────┼──────────────────────────────────────────────────────────────────
 sidebar (22 cells,   │   main pane                        │ side pane (a thread), when open
 18 under 120 cols)   │                                    │
──────────────────────┴──────────────────────────────────────────────────────────────────
 key hints: bold key, faint label, three spaces apart (or one status line: "✗ not sent · …")
```

- **The sidebar** lists the sections (Chats, Dashboard, Waiting, Feedback, Knowledge, Modules, Settings), with counts at the right (Waiting's in amber), then the chats grouped by project: `▾` open, `▸` closed, `›` the one shown, `●` news (also on a closed group that holds news). Ended agents fold into `▸ ended (n)` under their project. When a project is named `hive`, the Hive's own lead sits apart under **Hive-wide**.
- **The header row** is a breadcrumb (`textkit-2 › lead`, `Dashboard › status`) with a faint note (the lead's state, a count) and a faint note at the right (`oldest first`, `esc close`).
- **The selected item** in a list is marked with a bold `▌` bar down its left edge, not a reverse row, so its text stays readable.
- **Navigation rules (spec §11):** Esc goes one level up. Everything expands and collapses level by level, → (or enter) deeper and ← back. Tab moves between panes. No line runs off the screen: text wraps, and a long word breaks. A one-line summary row clips with `…` and its detail opens on enter. A detail that is longer than the pane scrolls, and its header says `21–40 of 112`.
- **Chats:** each message shows its speaker in their colour and the time, then the text. A thread's root shows `▸ 2 replies  last 20:31`, and `▾` when it's open inline. Compose boxes are rounded boxes with their label set into the top edge (`new thread · lead`, `reply · lead`). The box with the focus is drawn normally and the others faint.

## The frames

`frames.html` shows each of these at its size, dark beside light. The plain-text versions are `internal/tui/testdata/<name>.txt`. The data is invented: three projects (textkit-2, hive-web, website), six agents, three asks.

| frame | what it shows |
|---|---|
| `01-thread-open-reply` | 120×36. A project lead's chat, with the thread from 20:22 open in the side pane and a reply being written. The channel marks the open thread with `▌`. |
| `02a-long-reply` | 120×36. The same reply box grown to a multi-line draft with a list; the box grows upward and the thread scrolls. |
| `02a-long-reply-80x24` | 80×24. The same at 80 columns, where the thread takes over the screen. |
| `02b-sending` | 120×18, the thread pane alone. A reply on its way: it shows in the thread at once, marked as sending. |
| `02b-sending-80x24` | 80×24. The same at 80 columns. |
| `02c-sent` | 120×18, the thread pane alone. The reply is stored and reads like any message. |
| `02c-sent-80x24` | 80×24. The same at 80 columns. |
| `02d-failed` | 120×18. A send failed: the text goes back in the box, and the hint row says why (`✗ not sent · connection lost`). |
| `02d-failed-80x24` | 80×24. The same at 80 columns. |
| `03-new-thread` | 120×36. Writing a new thread to the lead in the channel's own box. |
| `03-new-thread-80x24` | 80×24. The same at 80 columns. |
| `04a-80x24-channel` | 80×24. A chat at 80 columns with a thread picked (`▌`, `open →`) but not open. |
| `04b-80x24-thread-takeover` | 80×24. The thread open: it takes the whole screen, with `‹` and `esc back` in the header. |
| `05a-dashboard-all` | 120×36. The dashboard's live feed: a summary line of agents, spend and activity, then every message between agents and the person, newest last, following everyone. |
| `05a-dashboard-80x24` | 80×24. The same at 80 columns, where the project column goes. |
| `05b-dashboard-status` | 120×36. The dashboard's status tree (`s`): spend, activity and all-time totals, each project's goals, work in flight, score and recent work, and the live agents. |
| `06a-waiting` | 120×36. Three asks waiting on the person, oldest first. The selected one shows its options inline, with the picked option in reverse. |
| `06a-waiting-80x24` | 80×24. The same at 80 columns. |
| `06b-modules` | 120×36. Installed modules (`[x]` on), with the selected module's detail under a rule. |
| `06b-modules-80x24` | 80×24. The same at 80 columns. |
| `06c-settings` | 120×36. Settings grouped under General, Agents and Routes: a label column and a value column, with the cursor on a row. Enter opens a pick-list under the row. |
| `06c-settings-80x24` | 80×24. The same at 80 columns. |
| `06d-feedback` | 120×36. Agents' feedback about the Hive: open items newest first (`●` unseen), then what was set aside. The selected item shows in full, with the run it came from. |
| `06d-feedback-80x24` | 80×24. The same at 80 columns. |
| `06d-knowledge` | 120×36. The knowledge base section, empty, with its search. |
| `07a-two-hives` | 120×36. A project named `hive` beside the Hive's own lead, which sits under **Hive-wide**; the Hive's lead chat is empty, with its hint. |
| `07b-two-hives-80x24` | 80×24. The project `hive`'s lead chat, empty, with its hint. |
| `08-ended-agents-80x24` | 80×24. Ended agents folded into `▸ ended (2)` under their project. |

## Work in flight that needs design

These are filed and being built now. Where one is unsettled, a design decides it. Some may already have landed by the time you read this; the frames show what has.

1. **Chat reading and sending** (task 261024-143004-8b8f0676a3). The thread pane scrolls through its whole history. A key (End or G) jumps to the newest message, and a small **"new below"** marker shows while the view is scrolled up. One Enter sends one message: a failed send stays in the box marked failed, with a retry. There is room above the box. `e` expands one more level and `shift+e` closes one. Every agent appears in the sidebar, and **agents are coloured by role** everywhere. *Design:* the "new below" marker, a failed message's look and its retry, and what role colours mean next to the per-name hues above.
2. **Spend and activity on the dashboard; what's new in Knowledge** (261024-132338-0628468534). 05b already shows spend, activity and all-time totals as tree rows. Still to come: a **"recently updated"** list at the top of Knowledge (title, when, new or updated). *Design:* whether the numbers deserve a denser at-a-glance block above the feed, beyond the one summary line in 05a, and the recently-updated list.
3. **An update shows itself** (261024-143355-97c5830dd4). While the Hive updates, every open control center shows a **banner** with the step and its progress (`updating to v0.1.58: downloading 4/12 MB`, `restarting`, `back on v0.1.58`). While the daemon is unreachable, a line says so (`reconnecting… (the Hive is restarting after an update)`), and keys stay live. A **heartbeat** in the status line shows when the screen last updated, so a frozen terminal can be told apart from a quiet Hive. *Design:* where the banner sits (the header row? a line above the hints?), how it looks at 80×24, and the heartbeat.
4. **Paste and images** (261024-140601-cb5f06eb5a). Pasting text and images into the chat box (alt+v, ctrl+v on macOS), on Windows too. Over SSH, an image can arrive by a path or through a local control center. *Design:* how an attached image shows in the box and in the sent message, and the one-line message that names the SSH routes.
5. **Agents talking to each other** (261024-144709-915a49752d). The dashboard feed shows every message between agents (worker→lead, worker→worker, sweep→lead, reviewer→worker), with roles coloured, not only the lead's. *Design:* how the feed stays readable at that volume: filters (`f` follow, `m` only me exist today), grouping by task, or folding.

## Open questions the design can settle

- What sits under the dashboard summary line at 80×24, where the feed loses its project column?
- A setting whose value wraps: show it whole (built), or clip it to one line with the full text on enter?
- Under about 50 columns: a stacked label/value layout, or a message asking for a wider window?

## What the handoff should contain

For the handoff to be implemented correctly as drawn:

- **Each new or changed screen as a text frame** at 120×36 and at 80×24, in the same form as `internal/tui/testdata/*.txt`: exact characters, one terminal row per line. Name it after the existing frame it changes (`05a-dashboard-all`), or with a new name for a new screen. These become golden frames, and the test then holds the code to them.
- **Styling per element**, in this brief's vocabulary: bold, faint, reverse, or a named colour from the table, never a new hex value without its light-terminal pair.
- **The key hint row** for each focus state, and what each key does: going deeper, coming back and Esc included.
- **States:** empty, loading or unreachable, one item, too many to fit, and a long value.
- **What it leaves out on purpose**, and any deviation from the rules above, with its reason.
