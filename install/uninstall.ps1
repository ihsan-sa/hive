# Uninstall the Hive (your folder, ~\hive by default, is left alone):
#   irm https://hive.ihsan.cc/uninstall.ps1 | iex
# It removes hive and hive-v0, its name up to v0.1.2.
$cmd = if ($env:HIVE_CMD) { $env:HIVE_CMD } else { 'hive' }
foreach ($c in @($cmd, 'hive-v0')) {
  $dir = if ($env:HIVE_BIN_DIR) { $env:HIVE_BIN_DIR } else { Join-Path $env:LOCALAPPDATA "Programs\$c" }
  $exe = Get-ChildItem $dir -Filter 'hive*.exe' -ErrorAction SilentlyContinue | Select-Object -First 1
  if ($exe) { & $exe.FullName uninstall; Start-Sleep -Seconds 1 }
  Remove-Item $dir -Recurse -Force -ErrorAction SilentlyContinue
  $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
  [Environment]::SetEnvironmentVariable('Path', (($userPath -split ';') | Where-Object { $_ -ne $dir }) -join ';', 'User')
}
Write-Host 'The Hive is uninstalled. Your folder and its projects are untouched.'
