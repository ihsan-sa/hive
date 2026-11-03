# The candidate get.ps1 over a real v0 Hive on a real Windows, in Windows PowerShell 5.1. Throwaway runner, no logins.
$env:WSL_UTF8 = '1'
$env:HIVE_ACCEPT = '1'
$script:bad = 0
$ErrorActionPreference = 'Continue'  # the runner sets Stop, and a program's stderr then ends the script under 5.1
function Head($t) { Write-Host ""; Write-Host "=== $t" }
function Check($name, $ok) { if ($ok) { Write-Host "ok   $name" } else { Write-Host "FAIL $name"; $script:bad++ } }
function B64($s) { [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($s)) }
function InU($c) { $b = B64 $c; $ErrorActionPreference='Continue'; & wsl.exe -d Hive -u hive --cd ~ -- bash -lc "echo $b | base64 -d | bash -l 2>&1" }
$raw = 'https://raw.githubusercontent.com/ihsan-sa/hive/candidate-v2'
$real = irm "$raw/get.ps1"
function Run-Candidate($site) {
  $c = $real -replace "\`$site = '[^']*'", "`$site = '$site'"
  $c = $c.Replace("'/install.sh -o", "'/get.sh -o").Replace('HIVE_INSIDE=1 sh', 'HIVE_BRANCH=candidate-v2 HIVE_INSIDE=1 sh')
  $t = Get-Date
  $log = Join-Path $env:TEMP ("run" + (Get-Random) + ".txt")
  Start-Transcript -Path $log | Out-Null
  $c | iex
  Stop-Transcript | Out-Null
  "took $([int]((Get-Date) - $t).TotalSeconds) s" | Write-Host
  return $log
}

Head 'a real v0.1.50, with data'
$prog = Join-Path $env:LOCALAPPDATA 'Programs\hive'
New-Item -ItemType Directory -Force $prog | Out-Null
$exe = Join-Path $prog 'hive.exe'
Invoke-WebRequest -UseBasicParsing 'https://github.com/ihsan-sa/hive/releases/download/v0.1.50/hive-windows-amd64.exe' -OutFile $exe
& $exe version 2>&1 | Select-Object -First 2 | Out-Host
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
[Environment]::SetEnvironmentVariable('Path', ($userPath.TrimEnd(';') + ";$prog"), 'User')
$env:Path = "$prog;$env:Path"
$r = Join-Path $env:USERPROFILE 'hive'
foreach ($d in 'projects\one\.checkouts\x', 'projects\two', '.hive\kb\isolated\one', '.hive\chats', '.hive\logins', '.hive\private') { New-Item -ItemType Directory -Force (Join-Path $r $d) | Out-Null }
New-Item -ItemType Directory -Force (Join-Path $env:APPDATA 'hive') | Out-Null
function W($p, $v) { [IO.File]::WriteAllText((Join-Path $r $p), $v) }
[IO.File]::WriteAllText((Join-Path $env:APPDATA 'hive\workspace'), $r)
W '.hive\logins\x' 'SEEDSECRET-LOGIN'; W '.hive\private\key' 'SEEDSECRET-KEY'; W '.hive\kb\a.md' "a note`n"; W '.hive\kb\isolated\one\n.md' "walled`n"
W '.hive\chats\main.jsonl' "{`"id`":`"1`",`"from`":`"p`",`"name`":`"Pat`",`"text`":`"hello`",`"at`":`"2026-01-01T00:00:00Z`"}`n"
W '.hive\events.jsonl' ("{`"seq`":1,`"at`":`"2026-01-01T00:00:00Z`",`"kind`":`"project.created`",`"data`":{`"name`":`"one`",`"goals`":`"do one`"}}`n" + "{`"seq`":2,`"at`":`"2026-01-01T00:00:00Z`",`"kind`":`"project.created`",`"data`":{`"name`":`"two`",`"goals`":`"do two`"}}`n")
W 'projects\one\f' "work`n"; W 'projects\one\.checkouts\x\junk' "junk`n"; W 'projects\two\g' "more`n"
$ErrorActionPreference = 'Continue'
Push-Location (Join-Path $r 'projects\one')
git init -q -b main 2>&1 | Out-Null; git add f 2>&1 | Out-Null; git -c user.name=t -c user.email=t@t commit -qm one 2>&1 | Out-Null; git remote add origin https://x:SEEDSECRET-TOK@example.com/a/one.git 2>&1 | Out-Null
Pop-Location
$ErrorActionPreference = 'Stop'
$running = Start-Process $exe -ArgumentList 'daemon' -WindowStyle Hidden -PassThru
Start-Sleep 5
"old daemon running: $(-not $running.HasExited)"
$seeded = '.hive\kb\a.md', '.hive\kb\isolated\one\n.md', '.hive\logins\x', '.hive\private\key', '.hive\chats\main.jsonl', 'projects\one\f', 'projects\one\.checkouts\x\junk', 'projects\two\g'
$sum = { $seeded | ForEach-Object { (Get-FileHash (Join-Path $r $_)).Hash } }
$before = (& $sum) -join ','

Head 'first run, failing halfway: the site is wrong once Linux is up'
$log1 = Run-Candidate "$raw/nope"
$t1 = Get-Content -Raw $log1
Check 'says what failed' ($t1 -match 'install inside the Hive system failed')
Check 'says running again carries on' ($t1 -match 'Running this again carries on')
Check 'old program gone' (-not (Test-Path $prog))
Check 'old daemon stopped' ($running.HasExited -or -not (Get-Process -Id $running.Id -ErrorAction SilentlyContinue))
Check 'old folder marked pending' ((Get-Content -Raw (Join-Path $r '.hive\v2-import')).Trim() -eq 'pending')
Check 'old folder whole' (((& $sum) -join ',') -eq $before)
Check 'old hive off the user Path' (-not (([Environment]::GetEnvironmentVariable('Path', 'User') -split ';') -contains $prog))

Head 'second run: carries on'
$log2 = Run-Candidate $raw
$t2 = Get-Content -Raw $log2
Check 'says it carries on' ($t2 -match 'Carrying on with the old Hive')
Check 'summary' ($t2 -match 'Brought across from the old Hive: 2 projects')
Check 'one address, on localhost' (([regex]::Matches($t2, 'http://localhost:\d+/login#')).Count -eq 1)
Check 'no 127.0.0.1 address' ($t2 -notmatch '127\.0\.0\.1')
Check 'no sudo line' ($t2 -notmatch 'sudo -iu')
Check 'no secrets shown' ($t2 -notmatch 'SEEDSECRET')
$broken = ([string][char]0xE2 + [char]0x20AC) + '|' + ([string][char]0x393 + [char]0xC7)
Check 'no broken characters' ($t2 -notmatch $broken)
Check 'marked done' ((Get-Content -Raw (Join-Path $r '.hive\v2-import')).Trim() -eq 'done')
Check 'old folder still whole' (((& $sum) -join ',') -eq $before)
foreach ($l in Get-ChildItem (Join-Path $env:LOCALAPPDATA 'Hive') -Filter '*.log' -Force -ErrorAction SilentlyContinue) {
  "log: $($l.FullName)"
  Check "$($l.Name) holds no key" (-not (Select-String -Path $l.FullName -Pattern 'login#|SEEDSECRET' -Quiet))
}

Head 'what came across'
$o = InU 'cd; ls projects projects/one; cat projects/one/goals.md; git -C projects/one log --oneline | wc -l; git -C projects/one config --get remote.origin.url; ls kb/old-hive kb/old-hive/chats; grep -rl SEEDSECRET kb projects .hive --exclude-dir=.git 2>/dev/null | wc -l; ls -d projects/one/.checkouts 2>&1; ls -l projects/one/f; systemctl --user is-active hive-starter hive-dash; hive post --help 2>&1 | head -1'
$o | Out-Host
$oj = ($o -join "`n")
Check 'projects moved' ($oj -match '(?m)^one$' -and $oj -match '(?m)^two$')
Check 'goals moved' ($oj -match 'do one')
Check 'token out of the remote' ($oj -notmatch 'SEEDSECRET')
Check 'checkouts left behind' ($oj -match 'No such file')
Check 'owned by hive' ($oj -match 'hive hive .* projects/one/f')

Head 'the hive command in a new cmd'
$p = [Environment]::GetEnvironmentVariable('Path','User')
$env:Path = "$p;$env:windir\System32"
$w = & cmd.exe /c 'where hive' 2>&1
$w | Out-Host
Check 'old hive not found first' ($w -notmatch 'Programs\\hive')
Check 'hive is the new one' ("$($w | Select-Object -First 1)" -match 'Hive\\bin\\hive.cmd')
$a = & cmd.exe /c 'hive web' 2>$null | Select-Object -First 1
"address shape: " + ($a -replace '#.*', '#...')
Check 'hive web says localhost' ("$a" -match '^http://localhost:\d+/login#')

Head 'localhost from Windows'
$base = ("$a" -replace '/login.*', '')
foreach ($f in @(), @('-4'), @('-6')) {
  $code = ''
  foreach ($i in 1..12) { $code = & curl.exe -s -m 10 -o NUL -w '%{http_code}' @f "$base/login"; if ($code -ne '000') { break }; Start-Sleep 5 }
  "curl $f /login: $code after try $i"
}
$code = & curl.exe -s -m 20 -o NUL -w '%{http_code}' "$base/login"
Check 'the app answers on localhost' ($code -match '^[234]')
try { $w = Invoke-WebRequest -UseBasicParsing "$base/login" -TimeoutSec 20; "Invoke-WebRequest: $($w.StatusCode)" } catch { "Invoke-WebRequest: $($_.Exception.Message)" }

Head 'third run: brings nothing again'
$log3 = Run-Candidate $raw
$t3 = Get-Content -Raw $log3
Check 'no second bring-across' ($t3 -notmatch 'Brought across')
Check 'one address' (([regex]::Matches($t3, 'http://localhost:\d+/login#')).Count -eq 1)

Head 'the screen, first and second run'
Get-Content $log1 | Select-Object -Last 25 | ForEach-Object { ($_ -replace "login#.*", "login#...") } | Out-Host
Get-Content $log2 | Select-Object -Last 40 | ForEach-Object { ($_ -replace 'login#.*', 'login#...') } | Out-Host
Write-Host ""
if ($script:bad) { Write-Host "FAILED $script:bad"; exit 1 }
Write-Host 'PASS'; exit 0
