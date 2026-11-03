# What the public Windows install does on a real Windows, in Windows PowerShell 5.1. Throwaway runner, no logins.
$env:WSL_UTF8 = '1'
function Step($t) { Write-Host ""; Write-Host "=== $t" }
function B64($s) { [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($s)) }
function InU { $b = B64 $args[0]; $ErrorActionPreference='Continue'; & wsl.exe -d Hive -u hive --cd ~ -- bash -lc "echo $b | base64 -d | bash -l 2>&1" | ForEach-Object { "  $_" } | Out-Host; "  rc=$LASTEXITCODE" | Out-Host }
function InR { $b = B64 $args[0]; $ErrorActionPreference='Continue'; & wsl.exe -d Hive -u root -- sh -c "echo $b | base64 -d | sh 2>&1" | ForEach-Object { "  $_" } | Out-Host; "  rc=$LASTEXITCODE" | Out-Host }
Step 'machine'
$PSVersionTable.PSVersion.ToString()
(Get-CimInstance Win32_OperatingSystem).Caption
(Get-CimInstance Win32_Processor | Select-Object -First 1).VirtualizationFirmwareEnabled
(Get-CimInstance Win32_ComputerSystem).HypervisorPresent
try { & wsl.exe --status 2>&1 | Out-Host; "status rc=$LASTEXITCODE" } catch { "status threw: $_" }
try { & wsl.exe --version 2>&1 | Out-Host; "version rc=$LASTEXITCODE" } catch { "version threw: $_" }

Step 'first run of the candidate get.ps1'
"blocks: $env:BLOCK"
if ($env:BLOCK -eq 'off') {
  $c = Get-Content -Raw .\get-candidate.ps1
  $c = $c -replace "(?m)^ExecStart=/bin/sh -c 'chmod.*$", 'ExecStart=/bin/true' -replace '(?m)^KERNEL==.*$', ''
  Set-Content -Path .\get-candidate.ps1 -Value $c -Encoding UTF8
  Select-String -Path .\get-candidate.ps1 -Pattern 'ExecStart' | Out-Host
}
$t = Get-Date
Get-Content -Raw .\get-candidate.ps1 | iex
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
InR 'cat /etc/wsl.conf; uname -r; systemctl is-system-running; systemctl --failed --no-legend | head -20; cat /proc/sys/kernel/apparmor_restrict_unprivileged_userns /proc/sys/user/max_user_namespaces; which bwrap'
InU 'id; echo "$XDG_RUNTIME_DIR"; which hive; hive --help | head -3; systemctl --user is-active hive-starter hive-dash hive-update.timer; systemctl --user list-timers --no-pager | head; loginctl show-user $(id -un) -p Linger'
Step 'cut off from Windows, as the hive user'
InU 'ls /mnt /mnt/c 2>&1 | head; ls /init; which powershell.exe cmd.exe; /mnt/c/Windows/System32/cmd.exe /c ver 2>&1 | head -2; mount -t drvfs C: /tmp/x 2>&1 | head -2; sudo -n true 2>&1 | head -1; ls /proc/sys/fs/binfmt_misc 2>&1'
Step 'as root, what could an escalation reach'
InR 'mkdir -p /tmp/x; mount -t drvfs C: /tmp/x 2>&1 | head -2; ls /tmp/x 2>/dev/null | head -3; umount /tmp/x 2>/dev/null; true'
Step 'boundary: can the hive user reach Windows at all'
InR 'systemctl is-active hive-no-windows; systemctl --failed --no-legend; cat /proc/sys/fs/binfmt_misc/WSLInterop | head -1; ls -l /dev/vsock /dev/dxg; ls -ld /run/WSL /mnt/wslg /mnt/wsl; mountpoint /tmp/.X11-unix; mkdir -p /tmp/x /home/hive/pe; mount -t drvfs C: /tmp/x; cp /tmp/x/Windows/System32/whoami.exe /tmp/x/Windows/System32/cmd.exe /home/hive/pe/; umount /tmp/x; chown -R hive /home/hive/pe; chmod +x /home/hive/pe/*; rmdir /tmp/x'
$probe = 'ls -l ~/pe | wc -l; ~/pe/whoami.exe; echo "whoami.exe rc=$?"; ~/pe/cmd.exe /c ver; echo "cmd.exe rc=$?"; ls /run/WSL /mnt/wslg /mnt/wsl /tmp/.X11-unix 2>&1 | head -8; for s in /run/WSL/*; do WSL_INTEROP=$s ~/pe/whoami.exe; echo "via $s rc=$?"; done; unshare -Urm sh -c "mkdir -p /tmp/y; mount -t drvfs C: /tmp/y; echo userns-drvfs rc=\$?"; python3 -c "
import socket
for port in (50000, 1, 12345):
    try:
        s = socket.socket(40, 1); s.settimeout(3); print(\"vsock\", port, s.connect_ex((2, port)))
    except Exception as e: print(\"vsock\", port, e)
" 2>&1'
InU $probe
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
InU 'uptime; systemctl --user is-active hive-starter'
Step 'sandbox parts, if this release has them'
InU 'hive sandbox check 2>&1 | head -5; which bwrap'
InR 'export DEBIAN_FRONTEND=noninteractive; apt-get -o DPkg::Lock::Timeout=120 -qq update >/dev/null 2>&1; apt-get -o DPkg::Lock::Timeout=120 -qq install -y bubblewrap >/dev/null 2>&1; echo apt rc=$?; which bwrap'
InU 'bwrap --unshare-user --unshare-all --new-session --ro-bind / / /bin/true; echo bwrap rc=$?; bwrap --unshare-user --disable-userns --ro-bind / / /bin/true; echo nouserns rc=$?'
Step 'restart with the keep-alive running: terminate, as a rerun that changes wsl.conf would'
function Back($what) {
  $t = Get-Date
  & wsl.exe -l -v 2>&1 | Out-Host
  $o = & wsl.exe -d Hive -u root -- sh -c "for i in 1 2 3 4 5 6 7 8 9 10 11 12; do test -d /run/systemd/system && break; sleep 5; done; sleep 10; systemctl is-system-running; systemctl is-active hive-no-windows; systemctl --failed --no-legend; ls -l /dev/vsock; cat /proc/sys/fs/binfmt_misc/WSLInterop 2>&1 | head -1" 2>&1
  "$what rc=$LASTEXITCODE after $([int]((Get-Date) - $t).TotalSeconds) s"; $o | Out-Host
  & wsl.exe -d Hive -u hive --cd ~ -- bash -lc "systemctl --user is-active hive-starter hive-dash; hive web" 2>&1 | Out-Host
}
& wsl.exe --terminate Hive 2>&1 | Out-Host; "terminate rc=$LASTEXITCODE"
Back 'after terminate'
& wsl.exe --terminate Hive 2>&1 | Out-Host; "terminate rc=$LASTEXITCODE"
Back 'after second terminate'
Step 'restart of the whole WSL machine: shutdown, as a Windows restart would'
& wsl.exe --shutdown 2>&1 | Out-Host; "shutdown rc=$LASTEXITCODE"
Start-Sleep 10
Back 'after shutdown'
Start-Process (Join-Path $env:WINDIR 'System32\conhost.exe') -ArgumentList '--headless wsl.exe -d Hive -u hive --exec sleep infinity'
Start-Sleep 5

Step 'rerun with the candidate get.ps1: updates, keeps the system'
InU 'date > ~/mark; mkdir -p ~/keepme'
$t = Get-Date
Get-Content -Raw .\get-candidate.ps1 | iex
"took $([int]((Get-Date) - $t).TotalSeconds) s"
InU 'uptime; cat ~/mark; systemctl --user is-active hive-starter'
Step 'remove'
# Answer the typed prompt the way a person does, by overriding Read-Host for this one call.
function global:Read-Host { 'delete' }
Get-Content -Raw .\remove-candidate.ps1 | iex
Remove-Item function:global:Read-Host
& wsl.exe -l -q 2>&1 | Out-Host
"lnk: " + (Test-Path (Join-Path ([Environment]::GetFolderPath('Startup')) 'Hive.lnk'))
"folder: " + (Test-Path (Join-Path $env:LOCALAPPDATA 'Hive'))
"path has it: " + ([Environment]::GetEnvironmentVariable('Path','User') -like '*Hive*')
exit 0
