# What the public Windows install does on a real Windows, in Windows PowerShell 5.1. Throwaway runner, no logins.
$env:WSL_UTF8 = '1'
function Step($t) { Write-Host ""; Write-Host "=== $t" }
function W { & wsl.exe -d Hive -u hive --cd ~ -- bash -lc $args[0] 2>&1 | ForEach-Object { "  $_" } | Out-Host; "  rc=$LASTEXITCODE" | Out-Host }
function R { & wsl.exe -d Hive -u root -- sh -c $args[0] 2>&1 | ForEach-Object { "  $_" } | Out-Host; "  rc=$LASTEXITCODE" | Out-Host }
Step 'machine'
$PSVersionTable.PSVersion.ToString()
(Get-CimInstance Win32_OperatingSystem).Caption
(Get-CimInstance Win32_Processor | Select-Object -First 1).VirtualizationFirmwareEnabled
(Get-CimInstance Win32_ComputerSystem).HypervisorPresent
try { & wsl.exe --status 2>&1 | Out-Host; "status rc=$LASTEXITCODE" } catch { "status threw: $_" }
try { & wsl.exe --version 2>&1 | Out-Host; "version rc=$LASTEXITCODE" } catch { "version threw: $_" }

Step 'first run of the public line'
$t = Get-Date
irm https://hive.ihsan.cc/install.ps1 | iex
"took $([int]((Get-Date) - $t).TotalSeconds) s"
$have = @(& wsl.exe -l -q) | ForEach-Object { ($_ -replace "`0", '').Trim() }
"systems: $($have -join ', ')"
if ($have -notcontains 'Hive') {
  Step 'no Hive system; second run, as after a restart'
  & wsl.exe --update 2>&1 | Out-Host; "update rc=$LASTEXITCODE"
  irm https://hive.ihsan.cc/install.ps1 | iex
  $have = @(& wsl.exe -l -q) | ForEach-Object { ($_ -replace "`0", '').Trim() }
  "systems: $($have -join ', ')"
  if ($have -notcontains 'Hive') { Write-Host 'NO HIVE SYSTEM: stop here'; exit 0 }
}
Step 'inside'
& wsl.exe -l -v 2>&1 | Out-Host
R 'cat /etc/wsl.conf; uname -r; systemctl is-system-running; systemctl --failed --no-legend | head -20; cat /proc/sys/kernel/apparmor_restrict_unprivileged_userns /proc/sys/user/max_user_namespaces; which bwrap'
W 'id; echo "$XDG_RUNTIME_DIR"; which hive; hive --help | head -3; systemctl --user is-active hive-starter hive-dash hive-update.timer; systemctl --user list-timers --no-pager | head; loginctl show-user $(id -un) -p Linger'
Step 'cut off from Windows, as the hive user'
W 'ls /mnt /mnt/c 2>&1 | head; ls /init; which powershell.exe cmd.exe; /mnt/c/Windows/System32/cmd.exe /c ver 2>&1 | head -2; mount -t drvfs C: /tmp/x 2>&1 | head -2; sudo -n true 2>&1 | head -1; ls /proc/sys/fs/binfmt_misc 2>&1'
Step 'as root, what could an escalation reach'
R 'mkdir -p /tmp/x; mount -t drvfs C: /tmp/x 2>&1 | head -2; ls /tmp/x 2>/dev/null | head -3; umount /tmp/x 2>/dev/null; true'
Step 'the app from Windows'
$a = (& wsl.exe -d Hive -u hive --cd ~ -- bash -lc 'hive web 2>/dev/null' | Select-Object -First 1)
"address shape: " + ($a -replace '#.*', '#...')
$base = ($a -replace '/login.*', '') -replace '#.*', ''
try { $r = Invoke-WebRequest -UseBasicParsing "$base/login" -TimeoutSec 20; "127.0.0.1 from Windows: $($r.StatusCode) $($r.Content.Length) bytes" } catch { "127.0.0.1 from Windows failed: $_" }
try { $r = Invoke-WebRequest -UseBasicParsing ($base -replace '127.0.0.1','localhost') -TimeoutSec 20; "localhost from Windows: $($r.StatusCode)" } catch { "localhost from Windows: $_" }
Step 'the hive command in Windows, in a new cmd'
$env:Path = [Environment]::GetEnvironmentVariable('Path','User') + ';' + $env:Path
Get-Content (Join-Path $env:LOCALAPPDATA 'Hive\bin\hive.cmd')
& cmd.exe /c 'hive who' 2>&1 | Select-Object -First 5 | Out-Host; "rc=$LASTEXITCODE"
Step 'keeps running with nothing attached'
Get-Process wsl,wslhost,conhost -ErrorAction SilentlyContinue | Select-Object Name,Id | Format-Table | Out-Host
Test-Path (Join-Path ([Environment]::GetFolderPath('Startup')) 'Hive.lnk')
Start-Sleep -Seconds 90
& wsl.exe -l -v 2>&1 | Out-Host
W 'uptime; systemctl --user is-active hive-starter'
Step 'sandbox parts, if this release has them'
W 'hive sandbox check 2>&1 | head -5; which bwrap'
R 'export DEBIAN_FRONTEND=noninteractive; apt-get -o DPkg::Lock::Timeout=120 -qq update >/dev/null 2>&1; apt-get -o DPkg::Lock::Timeout=120 -qq install -y bubblewrap >/dev/null 2>&1; echo apt rc=$?; which bwrap'
W 'bwrap --unshare-user --unshare-all --new-session --ro-bind / / /bin/true; echo bwrap rc=$?; bwrap --unshare-user --disable-userns --ro-bind / / /bin/true; echo nouserns rc=$?'
Step 'rerun: updates, keeps the system'
W 'date > ~/mark; mkdir -p ~/keepme'
$t = Get-Date
irm https://hive.ihsan.cc/install.ps1 | iex
"took $([int]((Get-Date) - $t).TotalSeconds) s"
W 'cat ~/mark; systemctl --user is-active hive-starter'
Step 'remove'
# Answer the typed prompt the way a person does, by overriding Read-Host for this one call.
function global:Read-Host { 'delete' }
irm https://hive.ihsan.cc/uninstall.ps1 | iex
Remove-Item function:global:Read-Host
& wsl.exe -l -q 2>&1 | Out-Host
"lnk: " + (Test-Path (Join-Path ([Environment]::GetFolderPath('Startup')) 'Hive.lnk'))
"folder: " + (Test-Path (Join-Path $env:LOCALAPPDATA 'Hive'))
"path has it: " + ([Environment]::GetEnvironmentVariable('Path','User') -like '*Hive*')
exit 0
