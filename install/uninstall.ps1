# Uninstall the Hive (your folder, ~\hive by default, is left alone):
#   irm https://hive.ihsan.cc/uninstall.ps1 | iex
$cmd = if ($env:HIVE_CMD) { $env:HIVE_CMD } else { 'hive-v0' }
$dir = if ($env:HIVE_BIN_DIR) { $env:HIVE_BIN_DIR } else { Join-Path $env:LOCALAPPDATA "Programs\$cmd" }
$exe = Join-Path $dir "$cmd.exe"
if (Test-Path $exe) { & $exe uninstall }
Start-Sleep -Seconds 1
Remove-Item $dir -Recurse -Force -ErrorAction SilentlyContinue
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
[Environment]::SetEnvironmentVariable('Path', (($userPath -split ';') | Where-Object { $_ -ne $dir }) -join ';', 'User')
Write-Host 'The Hive is uninstalled. Your folder and its projects are untouched.'
