---
title: Bubblewrap needs user namespaces
tags: sandbox
project: other
date: 2026-10-21
run: w-b
---

On hosts without unprivileged user namespaces bubblewrap fails, so the sandbox falls back and the agent never gets its tmpfs over the private folder.

Check that bubblewrap mounts work before trusting the sandbox to hide the private folder.
