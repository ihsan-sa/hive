# Server

For a Hive on a server that stays on. It holds no root, opens nothing, and runs nothing until you ask. The installer adds it when it finds a server; anywhere else, `hive module add server`. Linux with systemd only.

```sh
server check             # what is safe and what isn't; exits 1 if something fails
server reach             # how to get to the app without opening a port
server backup setup DEST # encrypted daily copies, with restic, to a place of yours
server backup now | status | restore [FOLDER]
server limits on | off | status
server updates           # how this machine stays up to date
heavy [--quick] cmd...   # run a memory-heavy job in turn; heavy --status shows them
```

## Check

It fails if the app's port faces a network, if this user has a way to root (sudo, docker and the like), or if the Hive won't survive logout and reboot. It warns on other open ports, SSH password login, no swap on a small machine, temporary files held in memory, a nearly full disk, no memory limits, no recent backup, and updates that aren't automatic. It only reads, and anything needing sudo is printed for the owner to run. Agents can edit this tool, so read a sudo line first and compare it with the [guide](../../docs/guide.md#the-server-module).

## Backups

`DEST` is any [restic](https://restic.net) destination: `sftp:you@host:/backups`, `s3:...`, or a folder. For one that needs a login, put its settings in `~/.hive/server/backup.env`, one `NAME=value` per line. Backups run daily and cover projects, notes, the board and settings. Logins, tokens, key files and build folders are left out, but a secret saved under another name in a project is included.

- Keep a copy of `~/.hive/server/backup.key` off the server. Without it the backups can't be read.
- Agents run as this same user, so they can read the key and reach the destination. If they must not be able to delete old backups, use an append-only destination.
- A restore only goes into an empty folder, so it never writes over live files.

## Limits

`server limits on` sizes the limits from the machine and needs no root. Part of the memory stays with the system. Everything the Hive runs slows down near its limit and stops at it. Heavy jobs have a lower limit of their own, so when they run over, the kernel kills a job and the agents carry on.

An agent in the sandbox can't reach systemd, so `heavy` there gives the job no cgroup of its own. The job still waits its turn with the others and is still first to be killed, and the limit on the whole Hive still holds.

## Remove

`server limits off`, `systemctl --user disable --now hive-server-backup.timer`, then `hive module remove server`. Your backups stay where they are.
