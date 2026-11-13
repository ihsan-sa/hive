#!/bin/bash
# A one-shot an agent scheduled with systemd-run failed: say so on the board, in the thread its script posts to.
# Wired for every user service by ~/.config/systemd/user/service.d/10-hive-oneshot-failed.conf; acts only on systemd-run units, so the
# standing services keep their own handling. Why: the 12:05am resume timer of 2026-11-12 died with exit 127 and nobody was told
# (person, post 11121012cbf5).
u="$1"; export PATH="$HOME/.local/bin:$PATH"; cd ~ || exit 0
d=$(systemctl --user show "$u" -p Description --value)
case "$d" in "[systemd-run]"*) ;; *) exit 0;; esac
cmd=${d#"[systemd-run] "}; f=${cmd%% *}
th=$( { [ -f "$f" ] && grep -o -m1 -- '--thread [0-9a-z-]\+' "$f"; } | cut -d' ' -f2)
r=$(systemctl --user show "$u" -p Result -p ExecMainStatus --value | tr '\n' ' ')
hive post --thread "${th:-timers}" <<X
A scheduled job failed and did not finish: \`${cmd:0:160}\` (unit \`$u\`, result $r). What it was meant to do has not happened. \`journalctl --user -u $u\` shows its output.
X
