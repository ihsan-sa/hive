# Several Hives in the one Hive WSL system on a real Windows, in Windows PowerShell 5.1 (step 7 of hive-app DESIGN-many-hives.md).
# Throwaway runner, no logins, public log: addresses are cut before they print.
$env:WSL_UTF8 = '1'
$env:HIVE_ACCEPT = '1'
$script:bad = 0
$ErrorActionPreference = 'Continue'
function Head($t) { Write-Host ""; Write-Host "=== $t" }
function Check($name, $ok) { if ($ok) { Write-Host "ok   $name" } else { Write-Host "FAIL $name"; $script:bad++ } }
function B64($s) { [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($s)) }
function InU($u, $c) { $b = B64 $c; & wsl.exe -d Hive -u $u --cd ~ -- bash -lc "echo $b | base64 -d | bash -l 2>&1" }
function Port($u) { $j = (& wsl.exe -d Hive -u root -- cat "/home/$u/.hive/web.json" 2>$null) -join ''; try { [string](($j | ConvertFrom-Json).port) } catch { '' } }
function Code($p) { $c = '000'; foreach ($i in 1..20) { $c = & curl.exe -s -m 10 -o NUL -w '%{http_code}' "http://localhost:$p/login"; if ($c -match '^[234]') { break }; Start-Sleep 3 }; $c }
function Mask($s) { ($s -replace 'login#\S*', 'login#...') }
$raw = 'https://raw.githubusercontent.com/ihsan-sa/hive/train-candidate'
$real = irm "$raw/get.ps1"
function Run-Candidate($user) {
  if ($user) { $env:HIVE_USER = $user } else { Remove-Item Env:HIVE_USER -ErrorAction SilentlyContinue }
  $c = $real.Replace("'/install.sh -o", "'/get.sh -o").Replace('HIVE_INSIDE=1 sh', 'HIVE_BRANCH=train-candidate HIVE_INSIDE=1 sh')
  $c = $c -replace "\`$site = '[^']*'", "`$site = '$raw'"
  $log = Join-Path $env:TEMP ("run" + (Get-Random) + ".txt")
  Start-Transcript -Path $log | Out-Null
  $c | iex
  Stop-Transcript | Out-Null
  Remove-Item Env:HIVE_USER -ErrorAction SilentlyContinue
  return (Get-Content -Raw $log)
}
$bin = Join-Path $env:LOCALAPPDATA 'Hive\bin'

Head 'the installers name no user of their own'
$n = 0
foreach ($f in 'get.ps1', 'remove.ps1') { $t = irm "$raw/$f"; $m = [regex]::Matches($t, '-u hive([^-a-z0-9_]|$)'); $n += $m.Count; foreach ($x in $m) { "  ${f}: $($x.Value)" } }
Check 'no literal -u hive in get.ps1 or remove.ps1' ($n -eq 0)

Head 'the first Hive'
$t1 = Run-Candidate $null
Check 'hive installed' ($t1 -match 'http://localhost:\d+/login#')
$p1 = Port 'hive'; "hive on port $p1"
Check 'hive answers' ((Code $p1) -match '^[234]')
InU 'hive' 'mkdir -p ~/projects/demo && cd ~/projects/demo && git init -q -b main && echo one > a.txt && git add -A && git -c user.name=t -c user.email=t@t commit -qm first && echo two >> a.txt && git -c user.name=t -c user.email=t@t commit -qam second' | Out-Null
$b1 = ((& wsl.exe -d Hive -u root -- stat -c %s /home/hive/board.jsonl) -join '').Trim()
$s1 = ((& wsl.exe -d Hive -u root -- sha256sum /home/hive/board.jsonl) -join '') -replace ' .*', ''

Head 'a second Hive beside it'
$t2 = Run-Candidate 'hive-work'
Check 'hive-work installed' ($t2 -match 'http://localhost:\d+/login#')
Check 'hive-work.cmd made' (Test-Path (Join-Path $bin 'hive-work.cmd'))
Check 'hive.cmd still opens hive' (Select-String -Quiet -SimpleMatch -Pattern ' -u hive ' -Path (Join-Path $bin 'hive.cmd'))
$p2 = Port 'hive-work'; "ports: hive $p1, hive-work $p2"
Check 'hive-work has its own port' ($p2 -and $p2 -ne $p1)
Check 'first Hive port unchanged' ((Port 'hive') -eq $p1)
# The first Hive is live and may post on its own board meanwhile: the lines it had stay as they were.
Check 'first Hive board unchanged' ((((InU 'root' "head -c $b1 /home/hive/board.jsonl | sha256sum") -join '') -replace ' .*', '') -eq $s1)
Check 'hive-work answers' ((Code $p2) -match '^[234]')
Check 'hive still answers' ((Code $p1) -match '^[234]')
Check 'one Linux system' (@((& wsl.exe -l -q) -replace "`0", '' | Where-Object { $_ -match '^Hive' }).Count -eq 1)
$t2 -split "`n" | Select-Object -Last 12 | ForEach-Object { Mask $_ } | Out-Host

Head 'the same line again'
# git as the Hive's user: root's git refuses another user's repo and prints nothing. rig/pin-release.sh says why the newest release is recorded first.
$st = 'cat /home/hive-work/.hive/web.json; git -C /home/hive-work/core rev-parse HEAD; git -C /home/hive-work/core status --porcelain'
$pin = 'IyBSdW4gYXMgYSBIaXZlJ3Mgb3duIHVzZXIgYmVmb3JlIHRoZSByZXJ1biBjaGVja3MuIEEgcmlnIGluc3RhbGxzIHRoZSB0ZXN0IGNhbmRpZGF0ZSAoSElWRV9CUkFOQ0gpLCB3aGljaCBpc24ndCBidWlsdCBvbiB0aGUKIyBwdWJsaXNoZWQgcmVsZWFzZSwgc28gdGhlIHJlcnVuJ3MgYGhpdmUgdXBkYXRlYCB3b3VsZCBzd2FwIGl0IGZvciB0aGF0IG9sZGVyIHJlbGVhc2UgKCJ0aGUgcmVsZWFzZSB3aW5zIikgYW5kIHRoZSBjaGVja3MgYWZ0ZXIKIyB3b3VsZCB0ZXN0IHRoZSByZWxlYXNlLCBub3QgdGhlIGNhbmRpZGF0ZS4gUmVjb3JkaW5nIHRoZSBuZXdlc3QgcHVibGlzaGVkIHRhZyBhcyBpbnN0YWxsZWQgcHV0cyB0aGUgSGl2ZSB3aGVyZSBhIHJlYWwgaW5zdGFsbCBvZgojIHRoZSBuZXdlc3QgcmVsZWFzZSBpczogdGhlIHJlcnVuIHRoZW4gZmluZHMgbm90aGluZyBuZXdlciwgd2hpY2ggaXMgdGhlIGNhc2UgdGhlIGNoZWNrIGlzIGFib3V0LgpzZXQgLWUKdD0kKGdpdCAtQyAiJEhPTUUvY29yZSIgbHMtcmVtb3RlIC0tdGFncyAtLXJlZnMgb3JpZ2luICd2Mi4qJyB8IHNlZCAncyMuKnJlZnMvdGFncy8jIycgfCBncmVwIC1FeCAndjJcLlswLTldK1wuWzAtOV0rJyB8IHNvcnQgLVYgfCB0YWlsIC1uIDEpClsgLW4gIiR0IiBdCm1rZGlyIC1wICIkSE9NRS8uaGl2ZSIgICMgd2hlcmUgdXBkYXRlLnB5IGtlZXBzIHVwZGF0ZS5qc29uIGluIGEgSGl2ZSAoU1RBVEUpCnB5dGhvbjMgLWMgImltcG9ydCBqc29uLHN5cyxwYXRobGliIGFzIFA7cD1QLlBhdGguaG9tZSgpLycuaGl2ZS91cGRhdGUuanNvbic7ZD1qc29uLmxvYWRzKHAucmVhZF90ZXh0KCkpIGlmIHAuZXhpc3RzKCkgZWxzZSB7fTtkWyd0YWcnXT1zeXMuYXJndlsxXTtwLndyaXRlX3RleHQoanNvbi5kdW1wcyhkKSkiICIkdCIKZWNobyAiJHQiCg=='
$t = (& wsl.exe -d Hive -u hive-work -- sh -c "cd; echo $pin | base64 -d | sh") -join ''
Check "hive-work recorded as on the newest release ($t)" ($LASTEXITCODE -eq 0 -and $t -match '^v2\.')
"hive-work core before: " + ((& wsl.exe -d Hive -u hive-work -- git -C /home/hive-work/core rev-parse --short HEAD) -join '')
$h0 = (& wsl.exe -d Hive -u hive-work -- sh -c $st) -join "`n"
$t3 = Run-Candidate 'hive-work'
$t3 -split "`n" | Select-Object -Last 8 | ForEach-Object { Mask $_ } | Out-Host
$il = Get-ChildItem (Join-Path $env:LOCALAPPDATA 'Hive') -Filter *.log -ErrorAction SilentlyContinue | Sort-Object LastWriteTime | Select-Object -Last 1
# What the person sees: the Linux installer's "already here" comes up as a note (core 2026-11-27), and nothing is set up again.
Check 'second run: says the Hive was already here' ($t3 -match 'already here')
if ($il) { $it = Get-Content -Raw $il.FullName; Check "second run: the inner install didn't set it up again" ($it -notmatch 'Setting up the Hive and Codex') } else { "  (no inner install log under LOCALAPPDATA\Hive)" }
"hive-work core after: " + ((& wsl.exe -d Hive -u hive-work -- git -C /home/hive-work/core rev-parse --short HEAD) -join '')
Check 'second run changed nothing (port, core)' (((& wsl.exe -d Hive -u hive-work -- sh -c $st) -join "`n") -eq $h0)

Head 'a project from one Hive to the other'
$z = @(InU 'hive' 'hive export demo 2>/dev/null')[-1]; "export: $z"
InU 'root' "install -o hive-work -g hive-work -m 600 '$z' /home/hive-work/demo.zip" | Out-Host
InU 'hive-work' 'hive import ~/demo.zip' | Out-Host
$l1 = (InU 'hive' 'git -C ~/projects/demo log --format="%H %s"') -join "`n"; $l2 = (InU 'hive-work' 'git -C ~/projects/demo log --format="%H %s"') -join "`n"
Check 'same git log in both' ($l1 -and $l1 -eq $l2)

Head 'hive shell in a real cmd.exe'
$env:Path = [Environment]::GetEnvironmentVariable('Path', 'User') + ";$env:windir\System32"
$in = Join-Path $env:TEMP 'shell-in.txt'
[IO.File]::WriteAllText($in, "pwd`nid -un`nexit`n", [Text.Encoding]::ASCII)
$o = @(& cmd.exe /c "hive shell hive-work < `"$in`"" 2>&1) | ForEach-Object { "$_".Trim() } | Where-Object { $_ }
"hive shell hive-work: $($o -join ' | ')"
Check 'hive shell hive-work lands in its home as hive-work' (($o -join ' ') -match '/home/hive-work hive-work')
$o = @(& cmd.exe /c "hive-work shell < `"$in`"" 2>&1) | ForEach-Object { "$_".Trim() } | Where-Object { $_ }
Check 'hive-work shell: its own home' (($o -join ' ') -match '/home/hive-work hive-work')
$o = @(& cmd.exe /c "hive shell daemon < `"$in`"" 2>&1) | ForEach-Object { "$_".Trim() } | Where-Object { $_ }
"hive shell daemon: $($o -join ' | ')"
Check 'hive shell daemon is harmless (no shell as daemon)' (-not ($o -contains 'daemon'))
$o = @(& cmd.exe /c "hive shell root < `"$in`"" 2>&1) | ForEach-Object { "$_".Trim() } | Where-Object { $_ }
Check 'hive shell root refused' (-not ($o -contains 'root'))

Head 'remove the first Hive'
$rm = irm "$raw/remove.ps1"
$env:HIVE_USER = 'hive'; $env:HIVE_KEEP = 'none'
function Read-Host { 'delete' }
$rm | iex
Remove-Item Function:Read-Host; Remove-Item Env:HIVE_USER, Env:HIVE_KEEP
Check 'hive user gone' (-not ((& wsl.exe -d Hive -u root -- id hive 2>&1) -match 'uid='))
Check 'hive.cmd gone' (-not (Test-Path (Join-Path $bin 'hive.cmd')))
Check 'hive-work.cmd stays' (Test-Path (Join-Path $bin 'hive-work.cmd'))
Check 'hive-work port unchanged' ((Port 'hive-work') -eq $p2)
Check 'hive-work answers right after' ((Code $p2) -match '^[234]')

Head 'ten idle minutes, then hive-work still answers'
Start-Sleep 600
$c = Code $p2
"after 10 minutes: $c"
Check 'hive-work answers after 10 idle minutes' ($c -match '^[234]')
Write-Host ""
if ($script:bad) { Write-Host "FAILED $script:bad"; exit 1 }
Write-Host 'PASS'; exit 0
