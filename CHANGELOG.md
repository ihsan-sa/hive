# Changelog

Release notes are on the [releases page](https://github.com/ihsan-sa/hive/releases). Each one says what's new and what to do if you run an older Hive, and links to the guide for limits and tests.

A release can ship several times a day. A new Hive installs at most one a day, once it has been out for at least 12 hours; **How often to update** in Settings changes that. The latest is at [releases/latest](https://github.com/ihsan-sa/hive/releases/latest).

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
