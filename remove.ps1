# Uninstall the Hive on Windows. One line, in PowerShell:
#   irm https://hivelab.ca/uninstall.ps1 | iex
# Deletes the Hive WSL system and everything in it, after you type delete. WSL itself and your other Linux systems stay.
function Uninstall-Hive {
  $ErrorActionPreference = 'Stop'
  $env:WSL_UTF8 = '1'
  $distro = 'Hive'
  $base = Join-Path $env:LOCALAPPDATA 'Hive'
  $bin = Join-Path $base 'bin'
  $have = @()
  try { $have = @(& wsl.exe -l -q) | ForEach-Object { ($_ -replace "`0", '').Trim() } } catch { }
  if ($have -notcontains $distro) { Write-Host 'No Hive system here, so nothing to remove.'; return }
  Write-Host 'This deletes the Hive system and everything in it: its projects, notes, board and Claude sign-in.'
  if ((Read-Host 'Type delete to go ahead') -ne 'delete') { Write-Host 'Nothing changed.'; return }

  & wsl.exe --unregister $distro
  if ($LASTEXITCODE -ne 0) { Write-Host "WSL couldn't delete it (code $LASTEXITCODE)."; return }
  Remove-Item -Force -ErrorAction SilentlyContinue (Join-Path ([Environment]::GetFolderPath('Startup')) 'Hive.lnk')
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
}
Uninstall-Hive
