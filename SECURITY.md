# Security

## Reporting a problem
Please don't open a public issue for a security problem. Use GitHub's private reporting: **Security**, then **Report a vulnerability** on this repository. Say what you did, what you saw, what you expected, and the version (Settings shows it). This is a small project and not a paid security team, so we can't promise a response time.

## What the sandbox does
Agents run in a sandbox on Linux. Inside it an agent sees the Hive's own folder and not the rest of your home, `~/.ssh` or your other logins, and it can't reach this machine's services or your private network. `hive sandbox check` tests it on your machine. Money, credentials, opening the machine to the network, publishing private details and deleting history are always your call.

## What it doesn't do
The sandbox is not a guarantee. In short:
- **An agent can send what it can read.** The sign-in a run uses and any provider key are readable inside the sandbox, and the internet is open through a proxy. If that matters, use a virtual machine or a spare computer.
- Running without the sandbox, which you can choose, gives agents everything their account can do.
- Approvals depend on a second agent reading the step. That is a check, not a lock.
- On Ubuntu 24.04 the install adds an AppArmor profile that widens the kernel's attack surface for every user of the machine.

The full list is in the guide: [what the sandbox doesn't do](https://hivelab.ca/guide/guide.html#sandbox-limits), and [what hasn't been tested](https://hivelab.ca/guide/guide.html#not-tested-yet).

## Who has looked
One independent model tried to break out of the sandbox in one pass of about 15 minutes and found no way out. The installer, the web app and the update path have had one security read, by the agents that build the Hive and not by anyone outside. We would welcome a real review.

## Updates
Releases are signed, and a Hive won't install one that isn't signed by a key it already trusts. Whoever holds that key can run code on every Hive that updates by itself. If you'd rather read each release first, turn off **Update by itself** in Settings.

## What a Hive sends
A failure report and one small check-in a day, both optional. See "What it sends" in the README.

## Supported versions
The latest release. Hives update themselves unless you turn that off.
