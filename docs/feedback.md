# Feedback from agents, and its tab

The person listed "feedback-from-agents-and-a-tab" with no more detail. This is the reading built here, and the questions left for them.

## What it does for the person

Agents are the ones who use the Hive most: its prompts, its tools, its sandbox, its briefs. When one of them hits friction (a prompt that sent it the wrong way, a missing tool, a brief with no base sha, a step that took three tries), that is today buried in its notes or its turn log, where the person never looks. Feedback gives that one place:

- An agent says it in a line: `hive agent feedback --text "..." [--about agents/worker.md]`. Any role may, except a one-turn experiment, which may only report on its own copy.
- The control center has a **Feedback** section, between Waiting and Modules. It lists open feedback newest first, then what was set aside. Each item shows its project, the role and name of the agent, what it is about, how long ago, and the text; the selected item shows all of it and the run it came from. The sidebar counts open items the person hasn't selected yet.
- The person can **pass** an item to the lead (`p`): it is posted in the project lead's chat (or the Hive lead's, for feedback with no project) as the person's message, quoting the feedback and asking the lead to weigh it, so the lead files work or says why not. Or **set it aside** (`x`), and bring it back the same way.
- The **improvement sweep** reads every open item in its digest, scoped like the rest of the digest, so feedback becomes asks with evidence without the person doing anything. New feedback alone is enough for a scheduled sweep to run.

## How it fits the spec

- *Judgement over rules*: no cap on how much an agent sends and no form to fill in; the sweep and the lead judge what is worth acting on.
- *Agents' own filings* (spec §10): agents are expected to raise problems with what they hit. `brief` files work and `remember` writes to the knowledge base; feedback is the third kind, a report to the person about the Hive, which neither of those was.
- *Notice* (§10): the sweep already reads notes, chats and failures; open feedback joins them.
- *Status stays out of the chats* (§11): feedback has its own section and reaches a chat only when the person passes it.
- *A small core*: it is a module (`internal/mod/feedback`) registering one op; the core is unchanged. Every change is a `feedback.saved` event, so it survives a restart, the knowledge base files it under `record/feedback/`, and a module's scoped `/api/events` reads only the feedback of projects granted to it.
- *Isolation*: an isolated project's feedback goes only into its own digest and its own lead's chat; it can't be passed to the Hive's lead.
- *The person decides*: nothing an agent sends is acted on as an instruction. A pass is the person's own message, and agents' text is shown with control characters spelled out.

## What it leaves out

- No reply to the agent. Most feedback outlives the run that sent it; the lead is the one who answers, in the chat, when it's passed.
- No upstream channel. Feedback stays in this Hive; nothing is sent to the Hive's maintainers (that would be sending something outward in the person's name).
- No categories, votes, dedup or severity. The sweep groups and weighs.
- No CLI for the person (`hive feedback`); the control center is the main way in, and `/api/feedback` and `/api/state` carry it.
- The web mirror, as for every other section.

## Open questions for the person

1. **Who is it from, and to whom?** Built: agents → the person, about the Hive itself. Did you instead mean feedback from friends' installs reaching you upstream (e.g. as GitHub issues on ihsan-sa/hive), or the person's feedback *to* agents? Either is outward or a different flow and wasn't guessed.
2. **Should feedback reach the lead without the person?** Built: only the sweep reads it on its own; the lead hears of it only when you pass it. Passing everything automatically is a one-line change.
3. **Should agents be told to send it?** Built: the orientation lists the op and what it is for, but no role is asked to send feedback at the end of every run. Asking every worker would give more signal and more noise.
4. **Placement and keys.** Feedback sits between Waiting and Modules, with `p` to pass and `x` to set aside. Say if you want it elsewhere, or folded into the Dashboard.

## Built and tested

- `internal/mod/feedback`: the op, the record, seen/close/reopen/pass, the digest; unit tests for who an item is recorded as (from the run, not the arguments), experiments refused, the lifecycle across a restart, digest scoping for isolated projects.
- `internal/mod/improve`: the digest's "Agents' feedback on the Hive, still open" section and `feedback.saved` counting as something new for a sweep.
- `/api/feedback` (`do`: seen, close, reopen, pass) and `feedback` in `/api/state` (all open items, then the last 20 set aside or passed); a daemon test passes an item to the lead's chat and refuses a second pass. A module token gets 403 there, as on any path its hooks don't name.
- The control center's Feedback section: design frames at 80×24 and 120×36, selection kept by id when new feedback arrives, control characters shown not obeyed; the tmux walk visits it.
