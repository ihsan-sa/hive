# Uninstall the Hive on Windows. One line, in PowerShell:
#   irm https://hivelab.ca/uninstall.ps1 | iex
# Deletes one Hive (the hive user, or $env:HIVE_USER = 'name' for another), after you type delete. Its last Hive takes the Hive WSL system with it; WSL itself and your other Linux systems stay.
# Your projects, notes and board stay unless you say to delete them: they're copied out to $HOME\Hive-kept-DATE first. $env:HIVE_KEEP = 'projects,notes,board' (any of them, or 'none') answers without asking.
function Uninstall-Hive {
  $ErrorActionPreference = 'Stop'
  $env:WSL_UTF8 = '1'
  $distro = 'Hive'
  $base = Join-Path $env:LOCALAPPDATA 'Hive'
  $bin = Join-Path $base 'bin'
  $hu = if ($env:HIVE_USER) { "$env:HIVE_USER" } else { 'hive' }
  $name = '^[a-z0-9_][a-z0-9_-]{0,31}$'
  if ($hu -cnotmatch $name -or $hu -eq 'root') { Write-Host "HIVE_USER must be a plain user name in small letters, other than root, not $hu. Nothing changed."; return }
  $cmdName = if ($hu -eq 'hive' -or $hu.StartsWith('hive-')) { $hu } else { "hive-$hu" }
  $have = @()
  try { $have = @(& wsl.exe -l -q) | ForEach-Object { ($_ -replace "`0", '').Trim() } } catch { }
  if ($have -notcontains $distro) { Write-Host 'No Hive system here, so nothing to remove.'; return }
  # Base64, so neither Windows nor wsl.exe can mangle its quotes.
  function As-Root($s) { $b = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($s)); & wsl.exe -d $distro -u root -- sh -c "echo $b | base64 -d | sh" }
  # The Hives in the system: its users with an installed Hive. If it won't answer, the commands this installer made.
  $hives = @()
  try { $hives = @(As-Root 'getent passwd | while IFS=: read -r u _ _ _ _ h _; do [ ! -e "$h/.hive-installed" ] || echo "$u"; done' 2>$null | ForEach-Object { "$_".Trim() } | Where-Object { $_ -cmatch $name }) } catch { }
  if (-not $hives.Count) { $hives = @(Get-ChildItem -ErrorAction SilentlyContinue (Join-Path $bin 'hive*.cmd') | ForEach-Object { if ((Get-Content -Raw $_.FullName) -match ' -u ([a-z0-9_-]+) ') { $Matches[1] } } | Sort-Object -Unique) }
  if ($hives.Count -and $hives -cnotcontains $hu) { Write-Host "There's no Hive $hu in the Hive system (it holds $($hives -join ', ')), so nothing to remove. HIVE_USER picks which one."; return }
  $others = @($hives | Where-Object { $_ -cne $hu })
  $all = if ($hives.Count) { $hives } else { @($hu) }
  $n = if ($all.Count -eq 1) { '1 Hive' } else { "$($all.Count) Hives" }
  Write-Host "The Hive system holds $n`: $($all -join ', ')."
  # What can stay: your projects, notes (the knowledge base) and board (your chats, with their files). Each stays unless you say to delete it.
  $where = [ordered]@{ projects = @('projects'); notes = @('kb'); board = @('board.jsonl', 'files') }
  $keep = @()
  if ($null -ne $env:HIVE_KEEP) {
    foreach ($k in ($env:HIVE_KEEP -split ',')) {
      $k = $k.Trim()
      if ($where.Contains($k)) { $keep += $k } elseif ($k -and $k -ne 'none') { Write-Host "HIVE_KEEP takes projects, notes, board or none, not $k. Nothing changed."; return }
    }
  } else {
    Write-Host 'Your projects, notes and board can stay, copied to a folder of their own. Press Enter to keep each one, or type y to delete it too.'
    $ask = [ordered]@{ projects = 'your projects (projects)'; notes = "the Hive's notes (kb)"; board = 'the board: your chats and their files (board.jsonl, files)' }
    foreach ($k in $ask.Keys) { if ((Read-Host "Delete $($ask[$k]) too? [y/N]") -notmatch '^\s*(y|yes)\s*$') { $keep += $k } }
  }
  $words = if ($keep.Count -eq 0) { 'nothing' } elseif ($keep.Count -eq 1) { $keep[0] } else { ($keep[0..($keep.Count - 2)] -join ', ') + ' and ' + $keep[-1] }
  $kept = $null
  if ($keep.Count) {
    $b = Join-Path $HOME ('Hive-kept-' + (Get-Date -Format 'yyyyMMdd')); $kept = $b; $n = 2
    while (Test-Path $kept) { $kept = "$b-$n"; $n++ }
  }
  $to = if ($kept) { ", copied to $kept" } else { '' }
  if ($others.Count) { Write-Host "This deletes the Hive $hu, with its Claude sign-in. The system and $($others -join ', ') stay. Kept: $words$to." }
  else { Write-Host "This deletes the Hive system and everything else in it, with its Claude sign-in. Kept: $words$to." }
  if ((Read-Host 'Type delete to go ahead') -ne 'delete') { Write-Host 'Nothing changed.'; return }

  # Copy out what stays, before the system goes. If that fails, nothing is deleted.
  # The Hive system can't see Windows' drives, so it sends a tar on its output. Start-Process writes that to the file byte for byte.
  if ($kept) {
    New-Item -ItemType Directory -Force $kept | Out-Null
    $tar = Join-Path $kept 'kept.tar'
    $paths = ($keep | ForEach-Object { $where[$_] }) -join ' '
    $script = 'cd || exit 1; set --; for p in ' + $paths + '; do [ ! -e "$p" ] || set -- "$@" "$p"; done; [ $# = 0 ] || exec tar -cf - "$@"'
    $b64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($script))
    $p = Start-Process wsl.exe -ArgumentList "-d $distro -u $hu -- sh -c `"echo $b64 | base64 -d | sh`"" -RedirectStandardOutput $tar -NoNewWindow -Wait -PassThru
    if ($p.ExitCode -ne 0) { Write-Host "Couldn't copy $words out of the Hive system (code $($p.ExitCode)), so nothing was deleted."; Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $kept; return }
    if ((Get-Item $tar).Length -gt 0) {
      try { $ErrorActionPreference = 'Continue'; & tar.exe -xf $tar -C $kept; $x = $LASTEXITCODE } catch { $x = 1 } finally { $ErrorActionPreference = 'Stop' }
      if ($x -eq 0) { Remove-Item -Force $tar } else { Write-Host "Some files couldn't be unpacked here; all of them are in $tar." }
    } else { Remove-Item -Force $tar }
  }

  # Others stay: end this one's processes, delete its user and home, and take off its own command (and, for hive, its icons).
  if ($others.Count) {
    As-Root "u=$hu; loginctl disable-linger `$u 2>/dev/null; loginctl terminate-user `$u 2>/dev/null; pkill -KILL -u `$u 2>/dev/null; sleep 1; userdel -r `$u 2>/dev/null; ! id `$u >/dev/null 2>&1" | Out-Null
    if ($LASTEXITCODE -ne 0) { Write-Host "Couldn't delete the Hive $hu (code $LASTEXITCODE). Its processes may still run; try again in a minute."; return }
    Remove-Item -Force -ErrorAction SilentlyContinue (Join-Path $bin "$cmdName.cmd")
    if ($hu -eq 'hive') {
      foreach ($d in 'Programs', 'DesktopDirectory') { $f = [Environment]::GetFolderPath($d); if ($f) { Remove-Item -Force -ErrorAction SilentlyContinue (Join-Path $f 'Hive.lnk') } }
      Remove-Item -Force -ErrorAction SilentlyContinue (Join-Path $bin 'hive-open.ps1'), (Join-Path $bin 'Hive.ico')
    }
    Write-Host "Removed the Hive $hu. The Hive system stays, with $($others -join ', ')."
    if ($kept) { Write-Host "Kept in ${kept}: $words." }
    return
  }
  & wsl.exe --unregister $distro
  if ($LASTEXITCODE -ne 0) { Write-Host "WSL couldn't delete it (code $LASTEXITCODE)."; return }
  foreach ($d in 'Startup', 'Programs', 'DesktopDirectory') { $f = [Environment]::GetFolderPath($d); if ($f) { Remove-Item -Force -ErrorAction SilentlyContinue (Join-Path $f 'Hive.lnk') } }
  try {
    $k = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment')
    $raw = [string]$k.GetValue('Path', '', 'DoNotExpandEnvironmentNames')
    if (($raw -split ';') -contains $bin) {
      $k.SetValue('Path', (($raw -split ';' | Where-Object { $_ -and $_ -ne $bin }) -join ';'), $k.GetValueKind('Path'))
      [Environment]::SetEnvironmentVariable('HIVE_PATH_CHANGED', $null, 'User')
    }
    $k.Close()
  } catch { Write-Host "Couldn't take the hive command off your PATH: $_" }
  Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $base
  Write-Host 'Removed.'
  if ($kept) { Write-Host "Kept in ${kept}: $words." }
}
Uninstall-Hive
