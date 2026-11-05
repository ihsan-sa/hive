---
name: server
description: Use on a Hive on a server, when a job may use a lot of memory (a build, a simulation, a big test run), or when asked about backups, memory limits, or reaching the app from outside.
---

Run a memory-heavy job through `heavy`, one step at a time, with any time limit inside: `heavy timeout 3600 make`. `heavy --quick cmd` is for a job under 5 minutes, and it's stopped at 5 minutes or 1500 MB. `heavy --status` shows what's running. Jobs wait their turn. If memory runs out, the job is killed first, not the agents.

`server check` reports the state of the server. `server backup status` says when the last backup ran. `server reach` shows how to get to the app.

Never open a port to reach the app. Never ask for or print the backup key. Setting up a backup sends the Hive's data to a destination, so that's the owner's choice: ask them, don't pick one. Anything that needs sudo is for the owner to run: point them to the line in the guide.
