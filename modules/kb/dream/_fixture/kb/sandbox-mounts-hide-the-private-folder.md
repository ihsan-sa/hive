---
title: Sandbox mounts hide the private folder
tags: sandbox,security
project: hive
date: 2026-10-20
run: w-a
---

Bubblewrap mounts an empty tmpfs over the private folder, so a sandboxed agent never reads another agent's home or the daemon token. Bubblewrap binds the checkout read-write.
