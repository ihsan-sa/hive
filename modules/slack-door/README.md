# slack-door
Slack as a plain way in, for people who want to send a message and get an answer. Not a view of the board: there is one bot, it makes a channel only when someone asks for one, and agents, thread names and post ids stay out of sight. Agents only ever use the board.

**What a person sees.** They type in any channel the bot is in, or DM it. 👀 appears within seconds. The answer comes in that message's Slack thread, and the 👀 goes. With no answer after 3 minutes a status line appears, edited in place and removed when the answer comes. If the run ends with nothing addressed to them, its last post is sent anyway; if there is none, they're told it stopped. The same goes for a run that sent an interim message and then carried on: an unaddressed result is sent, and after 5 quiet minutes they're told it ended.

**Handled here, with no model.** `status` and `stop` in a thread (with or without `!`). A nudge (`?`, `hello`, `any update`, a one-word ping) in a thread gets the job's state while something runs, and wakes the agent when nothing does, so a nudge restarts a dead job. A ping at the top of a channel gets "I'm here" and starts nothing. The same words sent again at the top of a channel while that job runs get a link to it. These match the whole message only, so a sentence is always a job.

**How a message finds an agent.**
- A reply in a known Slack thread goes to whichever agent last spoke there, deterministically.
- Anything else starts `@new` in a new board thread, with the channel's recent jobs listed. If it belongs to one, that agent runs `slack-door forward THIS THAT`; replies under the message then follow the old job. A burst within 2 minutes joins one job.
- A helper's thread inherits the job of the agent that opened it, so its questions land in the same Slack thread.
- A forwarded message arrives as data with its forwarder's words.
- A reply can wake an agent long after its run ended. It starts a new session under the same name and reads its board thread, so the board thread is the job's memory.

**Channels and folders.** `slack-door move THREAD name` makes or joins a channel, invites the job's owner, posts a first message and carries on under it; Slack can't move messages, so the old thread stays and links across. If Slack refuses, the agent is told to say so and ask the owner. Each channel has a folder under `~/projects`, by default named after it. Channels of one project can share a folder (`places` in the config, or `slack-door place THREAD folder`), and then see each other's recent jobs.

**What goes out.** Only posts addressed to `person` or `owner`. In a job's thread they go to that Slack thread. With no job (keeper, starter notices) they go to the owners by DM. `@owner` asks go by DM, and a non-owner's thread gets one line saying it's waiting; an owner's own job keeps the ask in its thread. The `owner` split is a routing convention: agents are told it when woken, nothing enforces it.

**Trust.** The allow-list in the config is the trust group. Anyone on it directs agents with this account's full rights, and any agent can read the tokens and can read the whole board, so answers can carry board text into Slack. Their posts go on as `person`, so they also pass the starter's usage stop lines, and may fall back to another model when no login has room. Every `@word` is removed from Slack text and display names, and the door adds the one mention it chose. A permanent Slack error (bot removed, channel archived, thread deleted) costs that one post: it's logged and the owners get its text by DM. Files arrive as paths, are never fetched from links, and only files under `~/files` go out.

## Setup
1. `slack-door install`: writes the config, adds `owner` to the outside names in the core config, links the units.
2. Create a Slack app from `slack-app.json` in the workspace (Socket Mode, one connection: don't share an app with another process, since Slack may send each event to any open connection).
3. Tokens: `~/.config/hive/slack-door-bot-token` (xoxb-), `~/.config/hive/slack-door-app-token` (xapp-), mode 600. Config: `~/.config/hive/slack-door.json`: `{"owners": ["U..."], "members": ["U..."], "channels": [], "places": {"C...": "folder"}}`. `channels` limits it to those ids, empty means any the bot is in. `jobs_only: true` drops posts with no job, for a trial beside another door.
4. `systemctl --user daemon-reload && systemctl --user enable --now slack-door.path`. Invite the bot to the channels, then run `slack-door channels`: it lists the ones it's not in, and a channel it's not in looks to its members like one that never answers. For a door that can't make channels, drop `channels:manage`, `groups:write` and `channels:join` from the manifest; `move` then fails and the agent says so.

State: `~/.local/state/slack-door/door.db`. Log: `journalctl --user -u slack-door`.

After a gap it reads back up to 2 days of each conversation it's in, and marks those messages late. Not carried: Slack edits and deletes.
