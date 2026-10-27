# What makes collaboration the natural path

Task 261025-223556-0aceba0dc2, issue #890. What the person said (2026-10-25, local time): "the hive behaviour needs to come naturally ... the obvious way for agents to do work, not force them into it" (18:33); "Right now its entirely top down" (18:25); don't overcomplicate, no new infra (18:27); all agents are the same (18:29); "think also of a company. while ai agents are like humans, some tools/systems companies implement facilitate collaboration" (18:35).

This page covers what companies, open source, nature and multi-agent LLM systems do to make working together the easy default, checks each against what this Hive's agents actually did, and ends with a few small changes ranked by how well they fit. It adds no code. It sits beside [forced.md](forced.md), which asks what to take away; this page asks what the Hive should put in its place.

## What the Hive's own record shows

These figures come from the journal (`$HIVE_STATE/events.jsonl`, 2026-10-24 01:55Z to 2026-10-27 05:12Z, 75 hours) and from the commands in every worker's turn logs (`runs/w-*/host/turn-*.jsonl`).

- **Messages:** 197 in all. sweep→lead 60, worker→lead 56, lead→worker 47, **worker→worker 16**, stepback→lead 12, sweep→worker 3, lead→lead 2, warden→lead 1. Peer talk is now 8% of messages. On the 2026-10-25 baseline it was 2 of 76 (docs/soft-layer.md).
- **What workers ran,** out of 683 worker runs. The share is the fraction of runs that ran the command at least once:

  | run started | runs | `hive agent search` | `hive agent status` | `hive agent message` | `hive agent remember` |
  |---|---|---|---|---|---|
  | 10-24 | 306 | 80 (26%) | 37 (12%) | 26 (8%) | 4 |
  | 10-25 | 244 | 50 (20%) | 62 (25%) | 26 (11%) | 3 |
  | 10-26 | 88 | 8 (9%) | 23 (26%) | 5 (6%) | 0 |
  | 10-27 | 45 | 2 (4%) | 14 (31%) | 7 (16%) | 1 |

  In 300 runs, 44% of the total, workers read `$HIVE_STATE` or `events.jsonl` directly. So agents read the record a lot more than they talk.

  Knowledge-base searches dropped from about a quarter of runs to about a twentieth over the same days the orientation shrank (1339 to 686 words, Simplification 8/9). The orientation used to say "search it before researching from scratch" in its own paragraph. Now that advice is one clause. Agents do what the words in front of them suggest, and little more.

- **Every one of the 16 peer messages was set off by a shared object:**

  | what they shared | messages |
  |---|---|
  | the same function or test (`pickLogin`, `TestBlockedWorkComesBack`) | 2 |
  | the same files in flight (`feed.go`/`nameStyle`; `heval/arms.py`; `frames.html`; the result-repo shape) | 7 |
  | the same scarce resource (the host for soft-layer runs; the gate as the merge bottleneck; the pace's worker slots) | 5 |
  | the same subject (the agent-cost audit going to the problems-board and prompt runs) | 2 |

  None was "just checking in". None came because a prompt said to talk: the `chatty` arm, which ordered agents to message every related run at every step, sent 0. Four of the eleven threads got a reply, and every reply was useful: "kept `nameStyle(name)` as the call"; "yes, keep `result_repo(arm, folder)`"; "slot freed". When there is something to coordinate, agents do it well. What's missing is the moment that shows them there is something to coordinate.

- **The soft-layer runs** (seven arms, from the full prompts down to one sentence) gave **0 messages in every arm**. Each arm had one worker at a time on a single task, so there was nobody to share anything with.

Taken together: wording barely changes whether agents collaborate. A shared object, seen at the moment of work, does.

## What each field knows

### Companies

- **Coase, transaction costs.** People coordinate inside a firm when that costs less than the market. The general version: collaboration happens when it is cheaper than working alone. Each tool below lowers one cost: finding who, reaching them, or getting an answer back.
- **Conway's law.** Systems copy the communication structure of the organisation that builds them, and the reverse holds too: hand out disjoint pieces through one manager and you get a hub. The Hive's lead writes every brief, and the queue holds back any two briefs that name the same file (`internal/mod/work/queue.go`, `areas`). So the Hive is built to keep workers from ever sharing a file while both are working. **The hold removes exactly the overlap that produces peer talk.** forced.md found the same hold takes 66% of queue time.
- **Code ownership and review (Google's readability and OWNERS, GitHub's CODEOWNERS).** Who to ask comes mechanically from what you touched. Nobody has to remember to ask, because the tool adds the reviewer.
- **Toyota's andon cord and kaizen.** Anyone on the line can stop it, and the fix is made at the station where the problem showed up. The Hive has the cord (`stop`, `queue`, `recheck` and "The loop is yours" in the orientation). Kaizen's other half is that the problem is posted *where the work happens*. The Hive half-builds that today: workers mostly report a problem to the lead, and the lead files it.
- **GitLab's handbook-first, Amazon's written narratives, Stripe's writing culture.** Async teams write things down where the next person will look, not in a message to a manager. That pays off only when reading is the default: GitLab's rule is to answer with a link to the handbook. The Hive's equivalent is the KB, and use of it is falling (see the table).
- **Blameless postmortems.** A failure turns into a shared lesson, not a private one. `.hive/lessons.md` is meant to be this. forced.md found it mostly holds gate rollback lines that teach nothing, which shows that a shared surface without filtering fills with noise.
- **Spotify-style guilds and chapters, two-pizza teams with single-threaded owners.** These are org-chart answers for hundreds of people. They are too heavy for a Hive of a handful of workers, and the person ruled out new structure.

### Open source

- **Linux's `MAINTAINERS` and `scripts/get_maintainer.pl`.** Give it your patch and it prints who to Cc, from the files you touched. Linux coordinates more than a thousand people with no manager by making "who should know" a lookup on the diff. This is the closest match to what the Hive lacks.
- **Apache's "if it didn't happen on the list, it didn't happen".** There is one public place, and discussion stays attached to the work: issue threads, PR comments, Wikipedia's talk page next to each article. Talk lives on the object it is about. The Hive's messages live in two runs' inboxes, so nobody else sees them, and no later run working on the same file ever finds them.
- **`git blame`, then email the author.** You reach the last person who touched the line. The Hive's conflict message (`internal/mod/work/work.go:3599`, "Main moved and your branch conflicts with it ...") doesn't name the task or run that moved main. The one moment two workers' work collides is the moment the Hive keeps the other party anonymous.
- **Reputation and reciprocity.** The commit log, the "Reviewed-by:" and "Reported-by:" tags, and a maintainer's standing all credit help in public, right where it was given. Credit drives a lot of open-source work, and it works because it costs nothing extra: it is a line in the commit.
- **Stigmergy in issue trackers.** "+1" and "me too" on an issue add up into priority without anyone deciding. A problem many contributors hit rises by itself.

### Nature

- **Stigmergy (Grassé, termites; ant pheromone trails).** Nobody directs anyone. Each agent changes a shared environment and reacts to the marks others left. Coordination costs nothing because leaving the mark is a side effect of working. **Trails evaporate:** a trail that stops being reinforced fades, so the environment shows the current picture and not every old path. Without evaporation, ants circle on stale trails.
- **Quorum sensing (bacteria) and nest-site choice (honeybees, Seeley).** The group acts when enough independent signals agree. Scouts recruit in proportion to how good a site is, and the swarm moves once a threshold of scouts is at one site. No leader decides, and one excited scout can't move the colony.
- **Local rules, global order (flocking, slime mould).** Each individual reacts only to its neighbours. What matters is who counts as a neighbour. For agents, a neighbour isn't a place, it's *who else is touching the same thing right now*.

### Multi-agent LLM systems

- **Blackboard systems (Hearsay-II, 1970s).** Specialists don't talk to each other. They read and write a shared board, and a scheduler decides who acts next. **MetaGPT** brought this back as a shared message pool where each role *subscribes* to the kinds of messages relevant to it, instead of being addressed one to one. Addressing by topic ("whoever touches `queue.go`") takes the "find the run id" step off the sender.
- **Contract Net (Smith, 1980).** A manager announces a task and agents bid. It is market allocation, and the Hive's queue already does that job.
- **Anthropic's multi-agent research system (2025).** Orchestrator and workers beat a single agent on broad research, at many times the tokens. The failures it reports are workers duplicating each other or misreading their task when the orchestrator's descriptions were vague. The fix was better task descriptions, not more chatter.
- **Cognition, "Don't build multi-agents" (2025).** Parallel agents make conflicting implicit decisions unless each sees the others' actions and context. The Hive has seen exactly this: the `standard` and `chatty` workers drifted off the brief to a nearby mechanism (docs/soft-layer-runs.md). The advice there is to share context, not messages.
- **"Why do multi-agent LLM systems fail?" (MAST, Cemri et al., 2025).** A failure taxonomy across several frameworks. Inter-agent misalignment is one of its three big groups: withheld information, ignored input from other agents, derailed conversations. More talk isn't the fix. Getting the right information to the agent that needs it, at the right time, is.
- **How LLM agents differ from people.** A person picks up the hallway: who's at the next desk, what the stand-up said. An agent's only world is its prompt plus what its tools print. It doesn't go looking for things it hasn't been told exist, which the falling search numbers show. So for an agent, "natural" means one thing: **the information is already in front of it, in the output of something it was going to run anyway.** Its tool outputs are its hallway.

## Five things all of these agree on

1. **Collaboration happens at a shared object.** That means the same file, the same test, the same scarce slot. All 16 Hive peer messages fit this, and so do every working human and natural system above. Telling agents to talk does nothing (`chatty`: 0).
2. **Who to tell must cost nothing to find out:** `get_maintainer.pl`, CODEOWNERS, `git blame`. A Hive worker today has to read `hive agent status`, guess which titles overlap, and copy a run id.
3. **The trace belongs where the next agent will be anyway:** on the object (the talk page), in the tool's output, in the brief. Not only in two inboxes.
4. **Signals decay and add up.** Old marks fade (pheromone), repeated independent marks raise priority (quorum, +1). A board without both ends up like `lessons.md`.
5. **Credit lands where the help happened,** at no extra cost: Reported-by, Reviewed-by. No separate reputation score, which would only be gamed.

## What that means for the Hive, ranked

Every item below reuses something the Hive already computes or prints. None adds a channel, a role or a daemon. They are ranked by expected effect per line changed.

1. **Turn the file-overlap hold into an introduction** (the `get_maintainer.pl` move). The queue already works out which waiting or running briefs name the same files (`queue.go` `areas`, line 611). forced.md filed the removal of the hold (261026-170804-db6e0e11dc). Keep the computation and drop only the hold: when a brief starts, its "What's already known" names the other run on the same files ("w-… is on '…' and names `queue.go`; tell it what you change"). This turns the Hive's largest wait into the condition that has produced every peer message so far. It belongs with 261026-170804 or with the natural-surfacing brief (261025-223343-11006bd8bb), not as new work.
2. **Name the other party where work collides** (the `git blame` move). The conflict message (`work.go:3599`, and the gate's version at 3605) should say which merged task and run moved main in the conflicting files, with "message it if its change and yours disagree". The same goes for a red main that improve reports: name the merge that turned it red. About a line each.
3. **Neighbours in output the agent already reads.** `hive agent finish` and the brief already go through the daemon. The reply to `finish` could list runs still working on the same files, so the account reaches them. It is the cheapest place there is to put the hallway.
4. **Make the problems board decay and add up** (pheromone and quorum). The board (261025-222214-5920e55ea5, PR #904, `hive agent problem` and `problems`) is the Hive's blackboard. Two rules from nature keep it from turning into `lessons.md`: an entry nobody reports again for a day fades from briefs, and a second run reporting the same problem ("me too") ranks it up, so that at two or more reporters a sweep files it as a brief without the lead. Fold these into #904's follow-up.
5. **Keep talk on the object.** A message about a file could also appear in the brief of the next run that names that file (from the same `areas` lookup as item 1), so that the inbox isn't the only record. This is optional and should come after items 1 to 3 show whether peer talk grows.
6. **Credit the way open source does.** The finish account (which becomes the PR description) could carry a "Helped by: w-…" line when a message changed the work. The judge and readers already read the account. It is not scored. Lowest priority.

**What not to do,** according to the evidence above:
- Don't add more "talk to each other" wording. `chatty` got 0, and every human system above is designed so that nobody needs exhorting.
- Don't add mandatory check-ins or broadcast-to-all. Broadcasting is how a board turns into noise.
- Don't add a reputation score. Goodhart's law says it would be gamed.
- Don't add new channels or roles. The person ruled out new infra, and the brief, the queue's overlap lookup, the conflict message and the board already exist.

## How to tell if it worked

The measurement already exists (`hive-soft observe`). What has been missing is a task where workers overlap:

- **Baseline (this page):** 16 peer messages in 197 over 75 hours. Peer threads with a reply: 4 of 11. Worker runs using `search`: 4% (10-27), `status`: 31%, `message`: 16%.
- **The metric that matters:** peer messages per pair of tasks that overlapped in time and named the same files, and the share answered. Total message count is the wrong target, because `chatty` shows talk can be asked for and still not happen, and its absence isn't the failure. The failure is two overlapping workers who never learn of each other.
- **The soft-layer test that can show it:** one arm, `max_workers` 2, two briefs naming the same file. Run it once before items 1 and 2 land and once after. One worker per arm, as in real2 to real4, can't show lateral talk at all.

## Sources

The Hive: `$HIVE_STATE/events.jsonl` and `runs/w-*/host/turn-*.jsonl` (counted by a short script over `msg.sent` and the `"command"` fields of tool calls). docs/soft-layer.md, docs/soft-layer-runs.md, docs/research/forced.md.

Outside: R. Coase, *The Nature of the Firm* (1937). M. Conway, *How Do Committees Invent?* (1968). Linux kernel `MAINTAINERS` and `scripts/get_maintainer.pl`. The Apache Way. The GitLab Handbook. J. Liker, *The Toyota Way*. P.-P. Grassé on stigmergy (1959). T. Seeley, *Honeybee Democracy* (2010). Erman et al., Hearsay-II (1980). R. Smith, the Contract Net Protocol (1980). Hong et al., *MetaGPT* (2023). Anthropic, "How we built our multi-agent research system" (2025). Cognition, "Don't Build Multi-Agents" (2025). Cemri et al., "Why Do Multi-Agent LLM Systems Fail?" (2025).
