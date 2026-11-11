# 6. Add tools with extras

Extras (modules) give agents tools and know-how for a kind of work. The Hive works without any of them. Agents can add one when a job needs it, but the tools it needs may have to be installed by you, as an admin on the machine.

## Add one

**Settings**, then **Modules**, shows three groups:

- **Public, installed:** what's on this Hive from the public list. The ones marked "comes with the Hive" are there from the start.
- **Recommended:** a few worth having, with **Add**. **Browse all** shows the whole public list, with a filter.
- **This Hive's own:** extras made on this Hive or added from a Git URL, each saying where it lives: on GitHub or only on this machine.

Click **Add** next to one. Or ask in Chat:

> Design a circuit board for this. Add whatever extra you need, and tell me what it has to download first.

| Extra | What it does | What it needs |
|---|---|---|
| `pcb-flow` | KiCad circuit board design, from a written brief to files ready to order | KiCad 10, freerouting, Java, xvfb, uv |
| `chip-flow` | Chip design with open tools | The IIC-OSIC-TOOLS image |
| `mech` | 3D-printable enclosures fitted to boards | uv |
| `fw` | ESP32 firmware: tests, builds, simulated boots | ESP-IDF, about 2 GB |
| `autoresearch` | A loop that keeps improving one measured number | Nothing extra |
| `slack-door` | Talk to the Hive from Slack ([tutorial 7](07-remote.md)) | Your own Slack app, uv |
| `cost` | What agents spent, by conversation and agent | Nothing to download |
| `extras` | Helpers such as code review prompts | Nothing to download |

## Add one from elsewhere

Paste a Git URL in **Add from a Git URL**, under **This Hive's own**. A module's code runs in your Hive, so only add ones you trust.

## Make your own

Ask for it:

> Make this into an extra, so future agents know how to use it.

Or write one: a folder with a `module.json`. The [README](https://github.com/ihsan-sa/hive/tree/v2#modules) shows the format.

From a terminal: `hive module list`, `hive module add NAME` and `hive module remove NAME`.

Next: [Reach your Hive from elsewhere](07-remote.md).
