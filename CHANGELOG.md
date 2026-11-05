# Changelog

Release notes are on the [releases page](https://github.com/ihsan-sa/hive/releases). Each one says what's new and what to do if you run an older Hive, and links to the guide for limits and tests.

A release can ship several times a day. A new Hive installs at most one a day, once it has been out for at least 12 hours; **How often to update** in Settings changes that. The latest is at [releases/latest](https://github.com/ihsan-sa/hive/releases/latest).

## Calmer updates: about once a day

A new Hive now installs at most one release a day, and only one that has been out for at least 12 hours. Settings has a new choice, **How often to update**: about once a day, or every release. A Hive already running stays on every release until you change it there. **Update now** and `hive update` still take the newest, and every release is checked the same way at either pace.

## Daily check-in: one new yes/no

The daily check-in now also says whether an agent has been started in your Hive yet (yes or no, nothing else about it). It is only in the check-in, so turning off failure reports and the daily check-in in Settings stops it, and then the Hive's log isn't read for it either. It lets the makers tell installs that were used from installs that were not. If you have turned the check-in off, nothing changes for you.

## Installs that stop partway on Windows or a Mac

If the installer stops on Windows or a Mac before Linux starts, it can now offer to send that failure to the Hive's makers: only the step, a short error label, the installer version and your OS and chip type, with no log. Nothing goes unless you type `y` (or set `HIVE_REPORT=1` where nothing can ask), and `HIVE_REPORT=0` stops the question. The daily check-in's OS now says `windows` or `mac` for those Hives, not `linux`; nothing else in it changes. If you turned reports off, nothing changes for you.
