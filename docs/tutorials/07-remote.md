# 7. Reach your Hive from elsewhere

The app only answers on the machine it runs on, which is the safe default. There are three ways to reach it from somewhere else. **Take the first that does the job.**

Agents have full rights on their account, so whoever reaches the Hive with full access has those rights too. Treat each of these as a door to that account.

## Option A: an SSH tunnel

Good for a server, or a second computer of your own. It opens nothing new if you already use SSH. From your own computer:
```sh
ssh -L PORT:127.0.0.1:PORT you@server
```
Use the port from your Hive's address, and open that address in your browser. You still need the secret in it.

## Option B: Slack

Good for sending jobs and getting answers from your phone. Slack sees what you send and what comes back. There's one bot, and each channel is one conversation.

This door is new: it has mostly been tested against a stand-in for Slack, so tell us if it goes wrong.

In a terminal as the Hive's account (see [tutorial 8](08-terminal.md)):

1. Install [uv](https://docs.astral.sh/uv/), which runs the door. Then `hive module add slack-door`, then `slack-door install`.
2. At [api.slack.com/apps](https://api.slack.com/apps), create an app from a manifest, and paste `~/modules/slack-door/slack-app.json`. Install it to your workspace.
3. Save the two tokens, each as a file with mode 600: the Bot token (`xoxb-`) as `~/.config/hive/slack-door-bot-token`, and an App token with `connections:write` (`xapp-`) as `~/.config/hive/slack-door-app-token`.
4. Add your Slack member ID under `owners` in `~/.config/hive/slack-door.json`. **Everyone you list directs agents with full rights.**
5. `systemctl --user daemon-reload && systemctl --user enable --now slack-door.path`.
6. Invite the bot to a channel, then run `slack-door channels` to see which ones it isn't in yet. Or DM it. Say something. 👀 appears within seconds, and the answer follows.

`status` and `stop` work as messages. The module's README has the details.

## Option C: the full app, through Cloudflare

Good for the whole app from anywhere, including your phone. It's the most work, and it puts your Hive on a public name, protected by your sign-in. You'll need a domain on Cloudflare.

1. Set up a Cloudflare Tunnel from the machine to `http://127.0.0.1:PORT`, on a hostname such as `hive.example.com`. An agent can walk you through this, but do the sign-ins yourself.
2. Create a Cloudflare Access application on that hostname, and allow only your own email. Prefer a Google or similar sign-in with 2-step verification over emailed codes, because the login is only as strong as the mailbox.
3. On the machine itself, open **Settings**, then **Access from outside**. Enter the hostname, the Access team domain, the application's audience tag and your email.
4. Tick only what you need. It's read-only until you tick more. **Post as you** lets you give agents orders from outside, and that means anyone with that sign-in can too.

A request from outside must carry a token signed by Cloudflare Access. The secret in your address stays for the machine itself. Only the machine itself can change these settings.

The module's documentation is in `~/modules/dash/README.md`.

Next: [Use it from a terminal](08-terminal.md).
