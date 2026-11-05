# 11. Put a Hive on a small server in 10 minutes

A server stays on when your laptop sleeps, so agents carry on overnight. You rent one, paste one file when you create it, and it installs the Hive by itself. You need an account with a server provider, an SSH key, and a Claude subscription or API key. The server costs money every month; the Hive opens nothing to the internet.

## 1. Create the server

Pick **Ubuntu 24.04** and a plan with **4 GB of memory** (see [what size](../guide.md#on-a-small-server)). Add your SSH key. In the box called **user data** (or cloud-init, or startup script), paste the file at:

```sh
curl -fsSL https://hivelab.ca/cloud-init.yaml
```
Open that address in a browser and copy everything, or have your provider fetch it. The file only runs the same one-line install as on a laptop, and adds a small swap file on a small server. Read it first if you like; it is short.

With a command-line tool it is one line. These come from the providers' documentation and haven't been run by us:

```sh
# Hetzner
hcloud server create --name hive --type cx32 --image ubuntu-24.04 --ssh-key YOURKEY \
  --user-data-from-file <(curl -fsSL https://hivelab.ca/cloud-init.yaml)
# DigitalOcean
curl -fsSL https://hivelab.ca/cloud-init.yaml > cloud-init.yaml
doctl compute droplet create hive --image ubuntu-24-04-x64 --size s-2vcpu-4gb \
  --region fra1 --ssh-keys YOURKEY_ID --user-data-file cloud-init.yaml
```
Plan names change; pick the nearest 4 GB one.

## 2. Log in

Wait about five minutes, then `ssh root@SERVER-ADDRESS` (the login your provider says, often `root` or `ubuntu`). The welcome message says **The Hive is installed** and gives three steps. If it says it is still installing, wait. If it says it failed, it names the log and the line to run again.

## 3. Open it from your computer

1. On your own computer, in a new terminal, run the line from the welcome message: `ssh -L PORT:127.0.0.1:PORT you@SERVER-ADDRESS`. Leave it open.
2. On the server, run `sudo -iu hive hive web`. It prints an address.
3. Open that address in your computer's browser. **It is the key to your Hive: keep it to yourself.**
4. Sign in to Claude in the app, then give it a job ([tutorial 1](01-first-job.md)).

Nothing is open to the internet but SSH, and the app answers only on the server itself. Leave your provider's firewall as it is.

## 4. And from your phone

- **Slack** sends jobs and brings answers back from anywhere: [Option B](07-remote.md#option-b-slack).
- **The whole app** on a phone needs a tunnel you set up with your own Cloudflare account: [Option C](07-remote.md#option-c-the-full-app-through-cloudflare).
- **Tailscale,** if you already use it: put the server on your tailnet and run the same `ssh -L` line to its tailnet name. We haven't tried this one.

## If it goes wrong

`sudo tail -f /var/log/hive-install.log` shows the install. To run it again: `curl -fsSL https://hivelab.ca/install.sh | HIVE_ACCEPT=1 sh` as root. Servers under 4 GB of memory may stop when several agents work; set **Agents at once** in Settings.

Next: [Reach your Hive from elsewhere](07-remote.md).
