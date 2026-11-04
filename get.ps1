# Install the Hive, or update it, on Windows 10 or 11. One line, in PowerShell:
#   irm https://hivelab.ca/install.ps1 | iex
# The Hive runs on Linux, so this makes a separate WSL 2 system called Hive. Agents work inside it as an ordinary user, so
# its Windows drives and Windows programs are switched off for its users. Root inside could turn them back on.
# Everything is inside one function, so a download cut short runs nothing, and "exit" would close your window: it returns instead.
function Install-Hive {
  $ErrorActionPreference = 'Stop'
  $ProgressPreference = 'SilentlyContinue'
  $env:WSL_UTF8 = '1'
  [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
  $site = 'https://hivelab.ca'
  $distro = 'Hive'
  $base = Join-Path $env:LOCALAPPDATA 'Hive'
  $bin = Join-Path $base 'bin'
  New-Item -ItemType Directory -Force $base | Out-Null
  # What the commands print goes here, and shows only if one fails. The address never does: it's the key.
  $log = Join-Path $base 'install.log'
  [IO.File]::WriteAllText($log, '')

  # Runs a script as root in the Hive system. Base64, so neither Windows nor wsl.exe can mangle its quotes or line ends.
  function Invoke-InHive([string]$Script, [string]$User = 'root') {
    $b64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($Script.Replace("`r", '')))
    return Quietly { & wsl.exe -d $distro -u $User -- sh -c "echo $b64 | base64 -d | sh" }
  }

  $arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }

  # 1. WSL itself. Installing it needs an administrator and usually a restart.
  # Windows PowerShell turns a program's redirected stderr into an error under 'Stop', and old Windows has no wsl.exe at all.
  $wsl = $false
  try { $ErrorActionPreference = 'Continue'; & wsl.exe --status *> $null; $wsl = ($LASTEXITCODE -eq 0) } catch { } finally { $ErrorActionPreference = 'Stop' }
  if (-not $wsl) {
    Write-Host 'The Hive needs WSL 2, which Windows is about to install. Say yes to the prompt.'
    $manual = 'In an administrator PowerShell, run: wsl --install --no-distribution'
    try { $p = Start-Process wsl.exe -ArgumentList '--install', '--no-distribution' -Verb RunAs -Wait -PassThru }
    catch { Write-Host "WSL didn't install: $_"; Write-Host $manual; return }
    if ($p.ExitCode -ne 0) { Write-Host "WSL didn't install (code $($p.ExitCode))."; Write-Host $manual; return }
    Write-Host 'WSL is installed. Restart Windows, then run the same line again.'
    return
  }

  # The old Hive (v0.x), a program of its own under Programs. It's stopped and taken off first, or its hive would answer before this one.
  # Its folder is the backup: nothing is deleted from it. .hive\v2-import in it says how far this got: "pending", then "done".
  $oldDirs = @(foreach ($n in 'hive', 'hive-v0') {
    $d = Join-Path $env:LOCALAPPDATA "Programs\$n"
    $e = Join-Path $d "$n.exe"
    if ((Test-Path $e) -and ([Text.Encoding]::GetEncoding(28591).GetString([IO.File]::ReadAllBytes($e)).Contains('hive-standalone-build'))) { $d }
  })
  $oldMark = Join-Path $base 'old-hive-root'
  $oldRoot = $null; $oldState = 'new'
  $oldFound = @()
  if (Test-Path $oldMark) { $oldFound += (Get-Content -Raw $oldMark).Trim() }
  $pointer = if ($env:APPDATA) { Join-Path $env:APPDATA 'hive\workspace' } else { '' }
  if ($pointer -and (Test-Path $pointer)) { $oldFound += (Get-Content -Raw $pointer).Trim() }
  $oldFound += (Join-Path $env:USERPROFILE 'hive')
  foreach ($r in $oldFound) {
    if (-not $r -or -not ((Test-Path (Join-Path $r '.hive\events.jsonl')) -or (Test-Path (Join-Path $r '.hive\config.json')))) { continue }
    $f = Join-Path $r '.hive\v2-import'
    $st = if (Test-Path $f) { (Get-Content -Raw $f).Trim() } else { 'new' }
    if ($st -eq 'done') { continue }
    $oldRoot = $r; $oldState = $st; break
  }
  if ($oldDirs.Count -or $oldRoot) {
    if ($oldState -eq 'pending') { Write-Host "Carrying on with the old Hive from the last run. Its folder is at $oldRoot." }
    else {
      Write-Host ''
      Write-Host 'Found an old Hive (v0.x):'
      foreach ($d in $oldDirs) { Write-Host "  program: $d" }
      if ($oldRoot) { Write-Host "  folder: $oldRoot" }
      Write-Host ''
      Write-Host "It will be stopped and removed first, so both don't run side by side. Agents in the middle of a turn may finish that turn."
      if ($oldRoot) {
        Write-Host 'Then these are copied into the new Hive: projects with their git history, the knowledge base, goals, and chats as pages to read.'
        Write-Host "They don't come: sign-ins, tokens and keys (you sign in again), past runs, task lists and schedules, and agents' checkouts."
        Write-Host "The old folder is your backup and stays where it is. Delete it when you're happy."
      }
      Write-Host ''
      if ($env:HIVE_ACCEPT -ne '1') {
        $answer = try { Read-Host 'Type yes to remove the old Hive and install the new one' } catch { '' }
        if ($answer -ne 'yes') { Write-Host 'Nothing changed. To accept with no question, set HIVE_ACCEPT=1'; return }
      }
    }
  }

  # 2. Our own Linux system, from Ubuntu's published image, checked against Ubuntu's published sums.
  $have = @(& wsl.exe -l -q) | ForEach-Object { ($_ -replace "`0", '').Trim() }
  $needUbuntu = $have -notcontains $distro
  $ui.total = 4 + [int][bool]$needUbuntu + [int][bool]$oldDirs.Count + [int][bool]$oldRoot
  if ($needUbuntu) {
    $name = "ubuntu-noble-wsl-$arch-wsl.rootfs.tar.gz"
    $url = "https://cloud-images.ubuntu.com/wsl/releases/24.04/current"
    $image = Join-Path $base $name
    Step 'Getting Ubuntu (about 350 MB)' -NoTick  # curl draws its own bar
    & curl.exe -fL --progress-bar -o $image "$url/$name"
    if ($LASTEXITCODE -ne 0) { Say "The download failed. Check your connection and run this again."; return }
    $sums = @(& curl.exe -fsSL "$url/SHA256SUMS")
    $want = ($sums | Where-Object { $_ -match [regex]::Escape($name) + '$' } | Select-Object -First 1) -replace '\s.*', ''
    $got = (Get-FileHash -Algorithm SHA256 $image).Hash.ToLower()
    if (-not $want -or $want.ToLower() -ne $got) { Remove-Item $image; Say "The download doesn't match Ubuntu's checksum, so it was deleted. Run this again."; return }
    Show-Line '  Unpacking it'
    $code = Quietly { & wsl.exe --import $distro (Join-Path $base 'system') $image --version 2 }
    Remove-Item -ErrorAction SilentlyContinue $image
    if ($code -ne 0) { Fail "WSL couldn't create the system (code $code). If you've just installed WSL, restart Windows and run this again. If the message below mentions virtualization, turn it on in your PC's BIOS or UEFI settings. Otherwise try: wsl --update"; return }
  }

  # 3. Cut it off from Windows and turn systemd on. wsl.conf is read when the system starts, so restart it.
  $conf = @'
cat > /etc/wsl.conf <<'CONF'
[boot]
systemd=true
[automount]
enabled=false
mountFsTab=false
[interop]
enabled=false
appendWindowsPath=false
CONF
'@
  # wsl.conf leaves other ways to the Windows side open to ordinary users: the GPU device, the desktop's sockets, and running .exe files. Root still has them. /mnt/wsl stays open: /etc/resolv.conf lives there, and closing it stops DNS.
  $block = @'
cat > /etc/udev/rules.d/99-hive.rules <<'CONF'
KERNEL=="dxg", MODE="0600"
CONF
cat > /etc/systemd/system/hive-no-windows.service <<'CONF'
[Unit]
Description=Keep the users of this system away from Windows
After=systemd-udev-trigger.service
[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/bin/sh -c 'chmod 600 /dev/dxg; chmod 700 /run/WSL /mnt/wslg; umount -l /tmp/.X11-unix; echo 0 > /proc/sys/fs/binfmt_misc/WSLInterop; true'
[Install]
WantedBy=multi-user.target
CONF
ln -sf /etc/systemd/system/hive-no-windows.service /etc/systemd/system/multi-user.target.wants/hive-no-windows.service
'@
  Step 'Preparing the Hive system'
  # A rerun that finds this already in place leaves the system up, so agents at work aren't killed.
  $set = (Invoke-InHive "grep -qx 'enabled=false' /etc/wsl.conf && grep -q '^\[interop\]' /etc/wsl.conf && test -d /run/systemd/system") -eq 0
  if (-not $set) {
    if ((Invoke-InHive ($conf + "`n" + $block)) -ne 0) { Fail "Couldn't configure the Hive system."; return }
    & wsl.exe --terminate $distro | Out-Null
  } elseif ((Invoke-InHive "test -e /etc/systemd/system/hive-no-windows.service") -ne 0) {
    # A Hive from before this block: put it in with no restart, since starting again after a terminate can fail until wsl --shutdown.
    if ((Invoke-InHive ($block + "`nudevadm control --reload; systemctl daemon-reload; systemctl start hive-no-windows")) -ne 0) { Fail "Couldn't configure the Hive system."; return }
  }
  $up = $false
  foreach ($i in 1..30) {
    & wsl.exe -d $distro -u root -- test -d /run/systemd/system | Out-Null
    if ($LASTEXITCODE -eq 0) { $up = $true; break }
    Start-Sleep -Seconds 1
  }
  if (-not $up) { Say "The Hive system didn't start. Run: wsl --shutdown   then run this again. If it still fails, update WSL with: wsl --update"; return }

  if ($oldDirs.Count -or $oldRoot) {
    if ($oldRoot) {
      [IO.File]::WriteAllText($oldMark, $oldRoot)
      [IO.File]::WriteAllText((Join-Path $oldRoot '.hive\v2-import'), 'pending')
    }
    if ($oldDirs.Count) { Step 'Stopping the old Hive' }
    foreach ($d in $oldDirs) {
      foreach ($e in @(Get-ChildItem $d -Filter 'hive*.exe')) {
        $ErrorActionPreference = 'Continue'
        try { & $e.FullName uninstall *> $null } catch { } finally { $ErrorActionPreference = 'Stop' }
        Get-Process -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq $e.FullName } | Stop-Process -Force -ErrorAction SilentlyContinue
      }
      foreach ($i in 1..10) { try { Remove-Item -Recurse -Force $d; break } catch { Start-Sleep -Seconds 1 } }
      if (Test-Path $d) { Say "Couldn't remove $d, because something is still using it. Close it, restart Windows if you need to, and run this again."; return }
      # Off the Path, keeping its kind, so %VARS% in the other entries still expand.
      try {
        $k = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment')
        $raw = [string]$k.GetValue('Path', '', 'DoNotExpandEnvironmentNames')
        $keep = @($raw -split ';' | Where-Object { $_ -and ([Environment]::ExpandEnvironmentVariables($_).TrimEnd('\') -ne $d) })
        if ($keep.Count -ne @($raw -split ';' | Where-Object { $_ }).Count) { $k.SetValue('Path', ($keep -join ';'), $k.GetValueKind('Path')) }
        $k.Close()
      } catch { Note "Couldn't take $d off your PATH: $_" }
      $env:Path = (@($env:Path -split ';' | Where-Object { $_.TrimEnd('\') -ne $d }) -join ';')
    }
  }

  # 4. The Linux installer does the rest. It updates a Hive that's already there.
  $halfway = if ($oldDirs.Count -or $oldRoot) { " The old Hive is stopped$(if ($oldRoot) { ", and its folder is whole at $oldRoot" }). Running this again carries on." } else { '' }
  Step 'Setting up the Hive, Claude Code and Codex'
  # To a file first, because a failed curl piped into sh looks like success. HIVE_INSIDE keeps the Linux installer from printing an ending of its own.
  # HIVE_REPORT=1 (or 0) set in PowerShell says yes (or no) to sending a failure to the Hive's makers, as on Linux.
  $report = if ($env:HIVE_REPORT -in '0', '1') { "HIVE_REPORT=$($env:HIVE_REPORT) " } else { '' }
  $getIt = 'f=$(mktemp) && curl -fsSL ' + $site + '/install.sh -o "$f" && ' + $report + 'HIVE_INSIDE=1 sh "$f"; c=$?; rm -f "$f"; exit $c'
  if ((Invoke-InHive $getIt) -ne 0) { Fail "The install inside the Hive system failed; the lines below say why. Fix that and run this again.$halfway"; return }

  # Bring its things across: to a file first, because the Hive system can't see Windows drives, and a pipe through PowerShell would corrupt it.
  if ($oldRoot) {
    Step "Bringing your old Hive's things across"
    $work = Join-Path $base 'old-hive-work'
    if (Test-Path $work) { Remove-Item -Recurse -Force $work }
    New-Item -ItemType Directory -Force $work | Out-Null
    $journal = Join-Path $oldRoot '.hive\events.jsonl'
    $lines = if (Test-Path $journal) { [IO.File]::ReadLines($journal) | Where-Object { $_.Contains('"kind":"project.') } } else { @() }
    [IO.File]::WriteAllLines((Join-Path $work 'projects.jsonl'), [string[]]@($lines))
    $items = @(foreach ($p in 'projects', '.hive/kb', '.hive/chats', '.hive/config.json') { if (Test-Path (Join-Path $oldRoot $p)) { $p } })
    $tar = Join-Path $env:windir 'System32\tar.exe'
    if (-not (Test-Path $tar)) { $tar = 'tar.exe' }
    $code = Quietly { & $tar -c -f (Join-Path $work 'old.tar') --exclude 'projects/*/.checkouts' -C $oldRoot @items -C $work projects.jsonl }
    if ($code -ne 0) { Fail "Couldn't read all of the old Hive's things.$halfway"; return }
    $s = '$HOME/.old-hive-incoming'
    if ((Invoke-InHive "chmod -R u+w $s 2>/dev/null; rm -rf $s; mkdir -p $s" 'hive') -ne 0) { Fail "Couldn't get the new Hive ready for them.$halfway"; return }
    Push-Location $work
    try { $code = Quietly { & cmd.exe /c 'wsl.exe -d Hive -u hive --cd ~ -- tar x --no-same-owner -f - -C .old-hive-incoming < old.tar' } } finally { Pop-Location }
    if ($code -ne 0) { Fail "Couldn't copy the old Hive's things in.$halfway"; return }
    $back = $oldRoot.Replace("'", "'\''")
    # Its summary (counts and names, no values) is for reading, so it shows.
    $ui.show = $true
    $code = Invoke-InHive "cd && python3 -I core/import-old.py $s '$back'" 'hive'
    $ui.show = $false
    if ($code -ne 0) { Fail "Couldn't sort out the old Hive's things.$halfway"; return }
    [IO.File]::WriteAllText((Join-Path $oldRoot '.hive\v2-import'), 'done')
    Remove-Item -ErrorAction SilentlyContinue $oldMark
    Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $work
  }

  # 5. A hive command for Windows.
  Step 'Adding the hive command'
  New-Item -ItemType Directory -Force $bin | Out-Null
  Set-Content -Encoding ASCII (Join-Path $bin 'hive.cmd') "@wsl.exe -d $distro -u hive --cd ~ -- bash -lc `"hive %*`""
  try {
    $k = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment')
    $raw = [string]$k.GetValue('Path', '', 'DoNotExpandEnvironmentNames')
    if (($raw -split ';') -notcontains $bin) {
      $kind = if ($k.GetValueNames() -contains 'Path') { $k.GetValueKind('Path') } else { 'ExpandString' }
      $k.SetValue('Path', (($raw.TrimEnd(';'), $bin | Where-Object { $_ }) -join ';'), $kind)
      [Environment]::SetEnvironmentVariable('HIVE_PATH_CHANGED', $null, 'User')  # tells new windows the PATH changed
    }
    $k.Close()
    $env:Path = "$env:Path;$bin"
  } catch { Note "Couldn't add the hive command to your PATH: $_" }

  Step 'Starting the Hive'
  # 6. WSL stops a system with nothing attached, so a hidden process keeps it up from sign-in on.
  $keep = '--headless wsl.exe -d ' + $distro + ' -u hive --exec sleep infinity'
  $conhost = Join-Path $env:windir 'System32\conhost.exe'
  try {
    $link = Join-Path ([Environment]::GetFolderPath('Startup')) 'Hive.lnk'
    $shell = New-Object -ComObject WScript.Shell
    $s = $shell.CreateShortcut($link); $s.TargetPath = $conhost; $s.Arguments = $keep; $s.Save()
    Start-Process $conhost -ArgumentList $keep
  } catch { Note "Couldn't set the Hive to start when you sign in: $_. It runs while a hive window is open." }

  # 7. Open the app.
  $address = (& wsl.exe -d $distro -u hive --cd ~ -- bash -lc 'hive web 2>/dev/null' | Select-Object -First 1)
  End-Line
  Write-Host ''
  if ($oldRoot) {
    Write-Host "What came and what didn't: kb/old-hive/README.md in the new Hive."
    Write-Host "Your old folder is still at $oldRoot. Delete it when you're happy."
  }
  Write-Host 'The Hive is installed. This address is the key to it, so keep it to yourself:'
  if ($address) { Write-Host "  $address" } else { Write-Host "  (it didn't print just now: wait a minute, then run: hive web)" }
  Write-Host 'To see it again, open a new window and run: hive web'
  if ($address -like 'http://localhost*' -or $address -like 'http://127.0.0.1*') { try { Start-Process $address } catch { } }
}

# How it talks: numbered steps, a dot every 5 seconds while one runs, and the commands' own output in the log.
& {
  $ui = @{ n = 0; total = 0; open = $false; tick = $null; show = $false; held = @() }
  function Stop-Tick { if ($ui.tick) { try { $ui.tick.Stop(); $ui.tick.Dispose() } catch { }; $ui.tick = $null } }
  # In a runspace of its own, so it ticks while a program runs here. If Windows' policy refuses one, there are just no dots.
  function Start-Tick {
    try {
      $t = [powershell]::Create()
      [void]$t.AddScript('$i = 0; while ($true) { [Threading.Thread]::Sleep(500); if (++$i % 10 -eq 0) { [Console]::Write(".") } }')
      [void]$t.BeginInvoke()
      $ui.tick = $t
    } catch { }
  }
  function End-Line {
    Stop-Tick
    if ($ui.open) { [Console]::WriteLine(); $ui.open = $false }
    foreach ($h in $ui.held) { Write-Host $h }
    $ui.held = @()
  }
  function Show-Line([string]$Text) { End-Line; Write-Host "$Text " -NoNewline; $ui.open = $true; Start-Tick }
  function Step([string]$Text, [switch]$NoTick) {
    $ui.n++
    $t = "[$($ui.n)/$($ui.total)] $Text"
    if ($NoTick) { End-Line; Write-Host $t } else { Show-Line $t }
  }
  function Say([string]$Text) { End-Line; Write-Host $Text }
  # Something to read in the middle of a step waits under that step's line, so the dots stay on one line.
  function Note([string]$Text) { if ($ui.open) { $ui.held += $Text } else { Write-Host $Text } }
  function Fail([string]$Text) {
    Say $Text
    $last = @(Get-Content -ErrorAction SilentlyContinue -Encoding UTF8 -Tail 15 $log)
    if ($last.Count) {
      Write-Host 'The last lines of what it printed:'
      foreach ($l in $last) { Write-Host "  $l" }
      Write-Host "All of it: $log"
    }
  }
  function Write-Log {
    process {
      $line = "$_"
      [IO.File]::AppendAllText($log, "$line`n")
      if ($line.StartsWith('NOTE: ')) { Note $line.Substring(6) } elseif ($ui.show) { Note $line }
    }
  }
  # Runs a command, its output going to the log, and gives back its exit code. Windows PowerShell turns a program's redirected stderr into an error under 'Stop'.
  function Quietly([scriptblock]$Run) {
    $ErrorActionPreference = 'Continue'
    & $Run 2>&1 | Write-Log
    return $LASTEXITCODE
  }
  # Windows' console reads programs' output in the old code page unless told otherwise, and programs inside Linux write UTF-8. Your window gets its own setting back after.
  $enc = $null
  try { $enc = [Console]::OutputEncoding; [Console]::OutputEncoding = New-Object Text.UTF8Encoding $false } catch { }
  try { Install-Hive }
  finally {
    End-Line
    if ($enc) { try { [Console]::OutputEncoding = $enc } catch { } }
  }
}
