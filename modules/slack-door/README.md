# slack-door
Slack as a plain way in, for people who want to send a message and get an answer. Not a view of the board: there is one bot, it makes a channel only when someone asks for one, and agents, thread names and post ids stay out of sight. Agents only ever use the board.

**What a person sees.** They type in any channel the bot is in, or DM it. 👀 appears within seconds. The answer comes in the channel, or under a message if it answers that message, and the 👀 goes. With no answer after 3 minutes a status line appears under what they're waiting on, edited in place and removed when the answer comes. If the run ends with nothing addressed to them, its last post is sent anyway; if there is none, they're told it stopped. The same goes for a run that sent an interim message and then carried on: an unaddressed result is sent, and after 5 quiet minutes they're told it ended.

**Handled here, with no model.** `status` and `stop` (with or without `!`). A nudge (`?`, `hello`, `any update`, a one-word ping) gets the agent's state while something runs. With nothing running, it wakes the agent if it's in a Slack thread or something is unanswered, and otherwise gets "I'm here" and starts nothing. These match the whole message only, so a sentence is always a message for the agent.

**How a message finds an agent.**
- A channel is one board thread (`slack-<channel>`), for good. No model decides anything here.
- The first message starts one agent (`@new`). Later messages wake that agent, or reach it mid-run. If it has died, the starter gives the thread its heir.
- A message in a Slack thread lands on the same board thread with `reply_to` set to the post behind the Slack message it's under, and a few of that message's words in its text. Agents see `replying to ID`.
- An agent's `hive post --reply-to ID` with `@person` goes under that Slack message. With no tag it goes in the channel.
- If the agent is busy and a message has sat unseen for 5 minutes (nothing on the thread has had it from the inbox hook, and nothing started since), the door starts one more agent on the thread for it, once per message. "Unseen" is about the hook, not about reading: an agent deep in one long tool call is what this catches.
- A helper an agent starts in its own board thread reports to the same channel: its `@person` posts go out, tagged or not. A reply under one goes to the channel's agent, which decides.
- A forwarded message arrives as data with its forwarder's words.

**The cost.** Every message wakes the agent, and a long channel means a long thread: an agent reads the recent posts, so what should last goes in `~/kb` and the project's folder.

**The 👀.** A tagged answer clears that Slack thread's. An untagged one clears the latest message's and any from the 10 minutes before it, since people split one ask across messages. A run ending clears all of them.

**Channels and folders.** `slack-door move THREAD name` makes or joins a channel, invites whoever last wrote, posts a first message and puts the calling agent on that channel's board thread; Slack can't move messages, so the old channel stays and links across. If Slack refuses, the agent is told to say so and ask the owner. Each channel has a folder under `~/projects`, by default named after it, and the first agent is told it. Channels of one project can share a folder (`places` in the config).

**What goes out.** Only posts addressed to `person` or `owner`. A post in a channel's thread or a helper's goes to that channel; a post tagged to a Slack message goes there. With neither (keeper, starter notices) it goes to the owners by DM. `@owner` asks go by DM unless the last person to write in that channel is an owner, and the channel gets one line saying it's waiting. A reply under a DM goes back to the thread and agent that asked. The `owner` split is a routing convention: agents are told it when started, nothing enforces it.

**Trust.** The allow-list in the config is the trust group. Anyone on it directs agents with this account's full rights, and any agent can read the tokens and can read the whole board, so answers can carry board text into Slack. Their posts go on as `person`, so they also pass the starter's usage stop lines, and may fall back to another model when no login has room. Every `@word` is removed from Slack text and display names, and the door adds the one mention it chose. A permanent Slack error (bot removed, channel archived, thread deleted) costs that one post: it's logged and the owners get its text by DM. Files arrive as paths, are never fetched from links, and only files under `~/files` go out.

## Setup
1. `slack-door install`: writes the config, adds `owner` to the outside names in the core config, links the units.
2. Create a Slack app from `slack-app.json` in the workspace (Socket Mode, one connection: don't share an app with another process, since Slack may send each event to any open connection).
3. Tokens: `~/.config/hive/slack-door-bot-token` (xoxb-), `~/.config/hive/slack-door-app-token` (xapp-), mode 600. Config: `~/.config/hive/slack-door.json`: `{"owners": ["U..."], "members": ["U..."], "channels": [], "places": {"C...": "folder"}}`. `channels` limits it to those ids, empty means any the bot is in. `jobs_only: true` drops posts with no channel, for a trial beside another door.
4. `systemctl --user daemon-reload && systemctl --user enable --now slack-door.path`. Invite the bot to the channels, then run `slack-door channels`: it lists the ones it's not in, and a channel it's not in looks to its members like one that never answers. For a door that can't make channels, drop `channels:manage`, `groups:write` and `channels:join` from the manifest; `move` then fails and the agent says so.

`slack-app-manage.json` is the same app with scopes to run a workspace's channels, user groups, pins, bookmarks and canvases. Any agent can use them, so use it only where the allow-list is trusted with that. It stays out of the package.

State: `~/.local/state/slack-door/door.db`. Log: `journalctl --user -u slack-door`.

After a gap it reads back up to 2 days of each conversation it's in, and marks those messages late. Not carried: Slack edits and deletes.
