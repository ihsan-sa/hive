# Install the Hive on Windows. One line:
#   irm https://hive.ihsan.cc/install.ps1 | iex
# Running it again reinstalls; $env:HIVE_VERSION = 'v0.1.2' installs that release
# instead of the latest. Agents are sandboxed inside WSL 2, so the Linux
# build is installed next to the Windows one, and Claude Code is installed or
# updated inside WSL.
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'   # Windows PowerShell's progress bar makes downloads crawl
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
$repo = if ($env:HIVE_REPO) { $env:HIVE_REPO } else { 'ihsan-sa/hive' }
$cmd = if ($env:HIVE_CMD) { $env:HIVE_CMD } else { 'hive' }
$version = if ($env:HIVE_VERSION) { $env:HIVE_VERSION } else { 'latest' }
$dir = if ($env:HIVE_BIN_DIR) { $env:HIVE_BIN_DIR } else { Join-Path $env:LOCALAPPDATA "Programs\$cmd" }
$tmp = Join-Path $env:TEMP ('hive-install-' + [guid]::NewGuid())
New-Item -ItemType Directory -Force $dir, $tmp | Out-Null
$assets = @('checksums.txt', 'hive-windows-amd64.exe', 'hive-linux-amd64')

# Runs a program and returns its exit code. Windows PowerShell treats a
# program's stderr as an error under 'Stop'; programs are judged by exit code.
function Invoke-Native([scriptblock]$Block, [switch]$Quiet) {
  $saved = $ErrorActionPreference
  $ErrorActionPreference = 'Continue'
  try {
    if ($Quiet) { & $Block *> $null } else { & $Block 2>&1 | ForEach-Object { Write-Host $_ } }
    return $LASTEXITCODE
  } finally {
    $ErrorActionPreference = $saved
  }
}

# Takes $Remove off or puts $Add on the user PATH, writing only when that changes it.
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

# Whether $Dir lies outside $Old, so removing $Old leaves it alone. Paths are
# compared whole, so a trailing separator or another case names the same folder.
function Test-Outside([string]$Dir, [string]$Old) {
  $d = [IO.Path]::GetFullPath($Dir).TrimEnd('\', '/') + '\'
  $o = [IO.Path]::GetFullPath($Old).TrimEnd('\', '/') + '\'
  -not $d.StartsWith($o, [StringComparison]::OrdinalIgnoreCase)
}

Write-Host "Downloading the Hive ($version) from $repo ..."
$base = if ($version -ne 'latest') { "https://github.com/$repo/releases/download/$version" } else { "https://github.com/$repo/releases/latest/download" }
foreach ($a in $assets) {
  Invoke-WebRequest "$base/$a" -OutFile (Join-Path $tmp $a) -UseBasicParsing
}
foreach ($a in $assets[1..2]) {
  $line = Select-String -Path (Join-Path $tmp 'checksums.txt') -Pattern " $([regex]::Escape($a))$" | Select-Object -First 1
  if (-not $line) { throw "no checksum for $a" }
  $want = $line.Line.Split(' ')[0]
  $got = (Get-FileHash (Join-Path $tmp $a) -Algorithm SHA256).Hash.ToLower()
  if ($want -ne $got) { throw "checksum mismatch for $a; not installing" }
}

$exe = Join-Path $dir "$cmd.exe"
# Up to v0.1.2 the Hive lived in Programs\hive-v0, as hive-v0.exe or, once updated, hive.exe:
# stop it, point its login autostart here and remove that folder, so only this install runs.
$oldDir = Join-Path $env:LOCALAPPDATA 'Programs\hive-v0'
if ((Test-Outside $dir $oldDir) -and (Test-Path $oldDir)) {
  foreach ($old in Get-ChildItem $oldDir -Filter 'hive*.exe') { Invoke-Native { & $old.FullName shutdown } -Quiet | Out-Null }
  $run = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run'
  if ((Get-ItemProperty $run -Name Hive -ErrorAction SilentlyContinue).Hive -like "*$oldDir\*") {
    Set-ItemProperty $run -Name Hive -Value "`"$exe`" daemon --detach"
  }
  # Windows holds an exe a moment after its process ends (and a scanner may hold a new one), so
  # the old folder, whose exe has just run shutdown, can't always be removed at once.
  for ($i = 0; $i -lt 15 -and (Test-Path $oldDir); $i++) {
    if ($i) { Start-Sleep -Seconds 1 }
    Remove-Item $oldDir -Recurse -Force -ErrorAction SilentlyContinue
  }
  if (Test-Path $oldDir) { Write-Warning "$oldDir is still in use; remove it once nothing runs from it." }
  Set-UserPath -Remove $oldDir
  $env:Path = (($env:Path -split ';') | Where-Object { $_ -ne $oldDir }) -join ';'
}
if (Test-Path $exe) {
  # Stop a running Hive so the next start runs the new version (agents keep running).
  Invoke-Native { & $exe shutdown } -Quiet | Out-Null
  Remove-Item "$exe.old" -Force -ErrorAction SilentlyContinue
  Move-Item $exe "$exe.old" -Force   # a running exe can be renamed, not overwritten
}
Copy-Item (Join-Path $tmp 'hive-windows-amd64.exe') $exe -Force
Copy-Item (Join-Path $tmp 'hive-linux-amd64') (Join-Path $dir 'hive-linux-amd64') -Force
Remove-Item $tmp -Recurse -Force

if (-not $env:HIVE_NO_PATH) {
  Set-UserPath -Add $dir
  if (($env:Path -split ';') -notcontains $dir) { $env:Path = "$env:Path;$dir" }
}

# Agents run inside WSL 2 with Claude Code installed there.
if (-not $env:HIVE_SKIP_WSL) {
  if (Get-Command wsl.exe -ErrorAction SilentlyContinue) {
    Write-Host 'Installing or updating Claude Code inside WSL (agents run there, sandboxed)...'
    $code = Invoke-Native { wsl.exe --exec bash -lc 'if [ -x $HOME/.local/bin/claude ]; then $HOME/.local/bin/claude update; else curl -fsSL https://claude.ai/install.sh | bash; fi' }
    if ($code -ne 0) { Write-Warning "Claude Code inside WSL didn't install; '$cmd doctor' will say what to do." }
  } else {
    Write-Warning "WSL 2 isn't installed. Agents need it for their sandbox: run 'wsl --install -d Ubuntu' (then restart), or opt into unsandboxed runs with '$cmd doctor --bare'."
  }
}

Write-Host ''
# Sharing failures stays off unless the person says yes. With no console, or
# on Enter, it's no. HIVE_SHARE=yes|no answers ahead of time, and a reinstall
# that was answered before doesn't ask again.
$share = $env:HIVE_SHARE
if (-not $share -and (Invoke-Native { & $exe report asked } -Quiet) -ne 0) {
  $share = 'no'
  if ([Environment]::UserInteractive -and -not [Console]::IsInputRedirected) {
    $share = Read-Host "`nShare failures and usage with the developer so they get fixed? Secrets are scrubbed on this machine first. (y/N)"
  }
  if (-not $share) { $share = 'no' }
}
if ($share -match '^[yY]') { Invoke-Native { & $exe report on } | Out-Null }
elseif ($share) { Invoke-Native { & $exe report off } | Out-Null }

Write-Host "Installed $(& $exe --version). Run: $cmd"
