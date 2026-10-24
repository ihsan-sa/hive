# Uninstall the Hive (your folder, ~\hive by default, is left alone):
#   irm https://hive.ihsan.cc/uninstall.ps1 | iex
# It removes hive and hive-v0, its name up to v0.1.2.

# Takes $Remove off or puts $Add on the user PATH, writing only when that changes it
# (the same as install.ps1's; path_check.ps1 tests both).
# It edits the registry value as stored: .NET's own read expands %VAR% entries,
# and its write drops the REG_EXPAND_SZ type.
function Set-UserPath([string]$Remove, [string]$Add) {
  $k = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment')
  try {
    $raw = [string]$k.GetValue('Path', '', 'DoNotExpandEnvironmentNames')
    $parts = @($raw -split ';' | Where-Object { $_ })
    if ($Remove -and $parts -contains $Remove) { $parts = @($parts | Where-Object { $_ -ne $Remove }) }
    elseif ($Add -and $parts -notcontains $Add) { $parts += $Add }
    else { return }
    $kind = if ($k.GetValueNames() -contains 'Path') { $k.GetValueKind('Path') } else { 'ExpandString' }
    $k.SetValue('Path', ($parts -join ';'), $kind)
  } finally { $k.Close() }
  # Clearing a variable that isn't set broadcasts the change, so new terminals see the PATH.
  [Environment]::SetEnvironmentVariable('HIVE_PATH_CHANGED', $null, 'User')
}

$cmd = if ($env:HIVE_CMD) { $env:HIVE_CMD } else { 'hive' }
foreach ($c in @($cmd, 'hive-v0')) {
  $dir = if ($env:HIVE_BIN_DIR) { $env:HIVE_BIN_DIR } else { Join-Path $env:LOCALAPPDATA "Programs\$c" }
  $exe = Get-ChildItem $dir -Filter 'hive*.exe' -ErrorAction SilentlyContinue | Select-Object -First 1
  if ($exe) { & $exe.FullName uninstall; Start-Sleep -Seconds 1 }
  Remove-Item $dir -Recurse -Force -ErrorAction SilentlyContinue
  Set-UserPath -Remove $dir
}
Write-Host 'The Hive is uninstalled. Your folder and its projects are untouched.'
