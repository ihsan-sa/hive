# 3. Get files in and out

Agents run in a sandbox around the Hive's folder, so they start with none of your files. You give them what they need.

## Give an agent a file

Click **Attach** in the message box, or paste an image. In an open conversation you can also drag files onto the page. Each file can be up to 100 MB. Then say what you want:

> Here's last year's budget. Add a column comparing each line to this year's figures, in the second sheet.

An attached file stays in the Hive, and every agent there can read it, so think before attaching something private.

## Get a result back

Ask for it:

> Attach the finished spreadsheet here.

It appears on the agent's message. Click to download.

## Work on a whole project

For a folder of code, use Git. If it's public, just give the link:

> Clone https://github.com/you/yoursite and find out why the contact form fails.

If it's private, the Hive needs access of its own. Ask an agent to walk you through it:

> I want you to work on a private GitHub repo. Tell me exactly what to set up, and use the narrowest access that works.

Don't paste tokens into Chat, because every agent can read them and they stay there. Send a token through **Secrets** in Settings, or sign in from a terminal as the Hive's account (see [Use it from a terminal](08-terminal.md)).

## Look at what's there

- **Windows:** in Explorer, go to `\\wsl$\Hive`. Your files are under `home\hive`.
- **Linux:** in a terminal, `sudo -iu hive`, then look in `~/projects` and `~/files`. In your own account they're in `~/hive/projects` and `~/hive/files`.

Agents usually keep each project under `~/projects/<name>` (their `~` is the Hive's home). Files sent in Chat are in `~/files/`, which appears with your first attachment.

## Keep what matters somewhere else

Everything here sits on one disk, and uninstalling deletes it all. For anything you care about, ask:

> Push this project to my repo and tell me how to check it arrived.

Next: [Keep an eye on the work and your usage](04-watching.md).
