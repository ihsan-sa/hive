# Steering the control center: /api/ui

A client the person holds (the deck, a script, a keyboard macro) can drive
every running control center (`hive`, or `hive remote <host>` on another
machine) through the daemon, and read back where it is to light its own
controls. The person's token only: `daemon.json`'s `token`, as
`Authorization: Bearer <token>`. A module's token is refused on all three
endpoints, as on `/api/hold`. Commands live in the daemon's memory (the last
64), not the journal: they move a screen and change nothing in the Hive, and
a restart drops them. An `answer` goes through `/api/answer` as the person's
own Enter on Waiting does.

## POST /api/ui: send a command

```json
{"action": "down", "n": 3, "client": "deck", "control": "knob"}
{"action": "view", "view": "waiting", "client": "deck", "control": "pad 3"}
{"action": "answer", "answer": "approve", "client": "deck"}
```

| action | what it does |
|---|---|
| `up`, `down` | moves the selection (or scrolls an open item), `n` steps (1 if unset, at most 100) |
| `left`, `right` | as the arrow keys: in Waiting, picks among the selected item's answers; elsewhere, back and into |
| `pageup`, `pagedown` | a page, `n` times |
| `view` | switches to `view`: `chats` (the sidebar on the open chat), `threads` (the open chat's thread list), `thread` (the open thread, with its reply box), `dashboard` (the live feed), `agents` (the status tree of projects, tasks and runs), `waiting`, `feedback`, `knowledge`, `modules`, `settings` |
| `open` | Enter on the selection: opens a chat, thread, task, run or diff |
| `back` | Esc: one level up |
| `answer` | answers the selected Waiting item with `answer` (one of its options, any case), or with the answer picked if unset; refused on the control center's hint line when Waiting isn't showing |

`client` and `control` (at most 40 bytes each) name who sent it; the
control center's hint line shows `⌁ deck pad 3: view waiting` for a few
seconds. The reply is the command as queued, with its `seq`; a bad action or
view is a 400 with `{"error": ...}`.

Each following control center acts on a command as the keys it stands for,
so it does what the person's keys would there: `down` at the end of a
chat's threads lands in the reply box, as ↓ does. Closing the help or
"while you were away" overlay comes first, so no command is eaten by one.
Commands sent before a control center opened are not replayed to it.

## GET /api/ui?after=SEQ&wait=SECONDS: follow, and read the view

Returns the commands after `SEQ`, waiting up to `wait` seconds (at most 30)
for one to come, the newest `seq`, and the view last reported:

```json
{"seq": 12, "commands": [{"seq": 12, "action": "down", ...}],
 "view": {"by": "tower pid 4242", "view": "waiting", "focus": "main",
          "sel": 1, "rows": 3, "item": "a-17", "title": "Run the installer?",
          "options": ["run", "decline"], "option": 0, "applied": 12, "at": "..."}}
```

With no `after` (or a negative one) it returns at once with no commands,
which is how a follower learns where to start. An `after` past the newest
seq means the daemon restarted: start again from `seq`.

`view` fields: `view` (one of the views above), `focus` (`sidebar`, `main`,
`thread`), `chat` (the open chat), `detail` (the kind of an item opened on
top: `task`, `run`, `diff`, `module`...), `sel` and `rows` (the selection in
the focused list, -1 for none; in an open item, its scroll and length),
`item` and `title` (the selection's id and text), `options` and `option`
(a Waiting item's answers and the one picked), `applied` (the last command
the control center acted on). A control center reports its view when it
moves, by keys or commands, within about 1.5 seconds; with several open, the
last to move is the one shown.

## POST /api/ui/view

The control center's own report of its view (the body is the `view` object
above). Clients only read it.
