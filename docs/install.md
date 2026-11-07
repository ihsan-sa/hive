# Install

You need a Claude account. The [guide](guide.md#before-you-install) has the full picture; this page has the commands and what each one does.

**Linux with systemd:**
```sh
curl -fsSL https://hivelab.ca/install.sh | sh
```
It asks for your password once, makes a Linux user called `hive`, installs git, Python and Claude Code if they're missing, and prints an address. Open that in a browser on the same machine, sign in to Claude, and say what you want.
- To open it again, click the Hive icon in your app menu (it opens the address your browser already knows). To see the address: `sudo -iu hive hive web`.
- On a server, forward the port: `ssh -L PORT:127.0.0.1:PORT server`, then open the address on your own machine. The install adds the [server module](guide.md#the-server-module), which checks the setup and gives you backups and memory limits.
- On Ubuntu 24.04 it adds an AppArmor profile so that bubblewrap can make the sandbox. It applies to every user of the machine and widens the kernel's attack surface for them. Uninstalling doesn't remove it, but prints the command that does.

**Windows 10 or 11**, in PowerShell:
```powershell
irm https://hivelab.ca/install.ps1 | iex
```
It sets up WSL 2 if needed (restart, then run the line again) and makes a separate Linux system called `Hive`, with your Windows drives and programs switched off for its users. Agents aren't root there, but a flaw that gave one root could turn them back on. `hive web` shows the address.

**In your own account, without sudo:**
```sh
curl -fsSL https://hivelab.ca/install.sh | sh -s -- --this-account
```
It runs as you from `~/hive` (`HIVE_DIR=/path` picks another folder) and needs git and Python already there. Agents run in a sandbox inside your account. **If you choose to run without it, or this machine can't run it, they have full rights to your account: all your files, `~/.ssh` and your logins.** No agent starts until the sandbox works or you choose. You type `yes` to this first; `HIVE_ACCEPT=1` accepts it where there's no terminal.

**Coming from a v0.x Hive?** The install finds it and asks, then removes the old program and brings your projects, notes and chats across. Logins and keys stay behind, so you sign in again. The old folder stays whole as your backup. If a run fails halfway, run the line again. `HIVE_ACCEPT=1` skips the question.

**macOS** has a test build that hasn't run on a real Mac yet. See the guide's [list of what isn't tested](guide.md#not-tested-yet).

**To read the script first,** download it, read it, then run it:
```sh
curl -fsSL https://hivelab.ca/install.sh -o install.sh
less install.sh
sh install.sh
```
The `install.sh` address on the site sends you to the file `get.sh` in `https://github.com/ihsan-sa/hive` (branch `v2`); on Windows `install.ps1` is `get.ps1` (the same way). It clones that branch, and the repo's own `install.sh` is the second script it then runs, as root for the main install. The script clones that branch over HTTPS, and runs Anthropic's own installer if Claude Code is missing. This first download isn't checked against a signature: only updates are. Or clone to `~/core` in a spare account, install Claude Code, and run `~/core/install.sh`.

If the install stops partway, it says which step and why, and where the log is. Fix what it names and run the same line again. It carries on from where it stopped.

**"Failed to connect to bus" from `systemctl --user`.** The shell has no systemd user session (the full message ends "$DBUS_SESSION_BUS_ADDRESS and $XDG_RUNTIME_DIR not defined"). `sudo` without `-i`, or a plain `su`, doesn't set it up. The install adds a line setting `XDG_RUNTIME_DIR` to the login profile of the account the Hive runs as (`hive`, or your own account if you installed with `--this-account`). Open a fresh login shell for it (`sudo -iu hive`, or log in again), check that `echo $XDG_RUNTIME_DIR` prints `/run/user/` and a number, then run the command there. In a shell you can't restart, `export XDG_RUNTIME_DIR=/run/user/$(id -u)` does the same for that shell. On a server, run `sudo loginctl enable-linger hive` once so its services keep running after you log out. If it still fails, open an issue with your Linux version and the last line the installer printed.
