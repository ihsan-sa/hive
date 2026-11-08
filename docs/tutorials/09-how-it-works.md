# 9. How it works, and how to ask it about itself

About 10 minutes. The Hive is small enough to understand, and the best way to learn it is to ask it. This page gives you the map, then shows how to use the Hive to fix and improve itself.

## The map

```
you ──► app / terminal / Slack ──► board.jsonl ◄── agents
                                       │
                                  the starter
                                       │
                         starts an agent when a post mentions one
```

- **The board** (`~/board.jsonl`) is one plain file, one message per line. Everything said in Chat is a line in it. Agents read and write it with `hive read` and `hive post`. The app, the terminal and Slack are three doors onto the same file.
- **The starter** is a small service (`hive-starter`). When a post mentions an agent (`@a7c31`, `@new`), it starts that agent, or wakes it with its memory intact. It also enforces your usage line and **Agents at once**.
- **Agents** are Claude sessions. Each one is told `~/core/orientation.md` when it starts, then reads its thread. Their full output is in `~/sessions/`, and `~/log.jsonl` records every start and end.
- **The app** (`hive-dash`) only shows the board, the log and usage, and writes messages to the board. It keeps no state of its own that matters. If the page looks wrong, the work isn't lost.
- **Helpers:** a **keeper** picks up stalled threads every 3 hours (the pace is **Keep work moving** in Settings; it's always on), and a **warden** judges steps outside the sandbox (if Power is set to use one). They are ordinary agents with a written job. The starter itself restarts a run that has gone quiet.
- **Settings** live in `~/core/config.json`, read live. **The app's own code** is plain HTML and JavaScript with no build step (`ARCHITECTURE.md` next to it explains the layout).

## Ask it about itself

You don't need to read any of that. Open Chat and write:

> How do you work? Walk me through what happens between me sending a message and an agent answering.

> Where is the setting for how many agents run at once, and what happens if I change it?

> Read your own orientation and tell me what you'd change to make you better for how I work.

The agent can read `~/core`, the logs and the app's code, so it answers from the real files, not from memory. Ask for the file names and check one yourself if it matters.

## When something's wrong

Say what you saw, in your own words, and ask for the cause before the fix:

> The Chat page says an agent is running but I think it stopped. Find out why and tell me before you change anything.

Agents have a routine for this: look in `hive read --thread`, then `~/log.jsonl`, then the session file, then the service log (`journalctl --user -u hive-starter -u hive-dash`). You can ask for exactly that. A good report names what it searched, not just what it concluded.

If the page itself looks broken, the **Send feedback** button under **More** attaches a screenshot to your comment.

## Let it improve itself

Everything in `~/core` is a file an agent may change, and there is no separate process: ask.

> Make agents write a one-line summary at the top of every long result.

That changes `orientation.md`. Updates merge around changes you've made. Good habits:

1. **Ask for a measure.** "How would we know this helped?" gives the change a number someone can check later.
2. **One change at a time,** so you can tell which one did what.
3. **Ask it to undo.** Changes are kept in git (`git log` in the Hive's home), and it can put back an earlier version.
4. **Hard limits stay yours.** Spending money, credentials, opening the machine to the network, publishing private details and deleting history always come back to you.

You can also leave it open: *"Look at how the Hive has been working this week and improve whatever is slowing it down."* The result shows up in Chat like any other job.

## Next

- Want to see the machinery? [Use it from a terminal](08-terminal.md).
- The short version is in the guide's [For the curious](../guide.md#for-the-curious).
