# Install the Hive, or update it, on Windows 10 or 11. One line, in PowerShell:
#   irm https://hive.ihsan.cc/install.ps1 | iex
# The Hive runs on Linux, so this makes a separate WSL 2 system called Hive. Agents have full rights inside it, so its
# Windows drives and Windows programs are switched off there: agents can't reach your files.
# Everything is inside one function, so a download cut short runs nothing, and "exit" would close your window: it returns instead.
function Install-Hive {
  $ErrorActionPreference = 'Stop'
  $ProgressPreference = 'SilentlyContinue'
  $env:WSL_UTF8 = '1'
  [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
  $site = 'https://hive.ihsan.cc'
  $distro = 'Hive'
  $base = Join-Path $env:LOCALAPPDATA 'Hive'
  $bin = Join-Path $base 'bin'

  # Runs a script as root in the Hive system. Base64, so neither Windows nor wsl.exe can mangle its quotes or line ends.
  function Invoke-InHive([string]$Script, [string]$User = 'root') {
    $b64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($Script.Replace("`r", '')))
    & wsl.exe -d $distro -u $User -- sh -c "echo $b64 | base64 -d | sh" | Out-Host
    return $LASTEXITCODE
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

  # 2. Our own Linux system, from Ubuntu's published image, checked against Ubuntu's published sums.
  $have = @(& wsl.exe -l -q) | ForEach-Object { ($_ -replace "`0", '').Trim() }
  if ($have -notcontains $distro) {
    $name = "ubuntu-noble-wsl-$arch-wsl.rootfs.tar.gz"
    $url = "https://cloud-images.ubuntu.com/wsl/releases/24.04/current"
    New-Item -ItemType Directory -Force $base | Out-Null
    $image = Join-Path $base $name
    Write-Host "Downloading Ubuntu (about 350 MB)"
    & curl.exe -fL --progress-bar -o $image "$url/$name"
    if ($LASTEXITCODE -ne 0) { Write-Host "The download failed. Check your connection and run this again."; return }
    $sums = @(& curl.exe -fsSL "$url/SHA256SUMS")
    $want = ($sums | Where-Object { $_ -match [regex]::Escape($name) + '$' } | Select-Object -First 1) -replace '\s.*', ''
    $got = (Get-FileHash -Algorithm SHA256 $image).Hash.ToLower()
    if (-not $want -or $want.ToLower() -ne $got) { Remove-Item $image; Write-Host "The download doesn't match Ubuntu's checksum, so it was deleted. Run this again."; return }
    & wsl.exe --import $distro (Join-Path $base 'system') $image --version 2 | Out-Host
    $code = $LASTEXITCODE
    Remove-Item -ErrorAction SilentlyContinue $image
    if ($code -ne 0) { Write-Host "WSL couldn't create the system (code $code). If you've just installed WSL, restart Windows and run this again. If the message above mentions virtualization, turn it on in your PC's BIOS or UEFI settings. Otherwise try: wsl --update"; return }
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
# wsl.conf leaves other ways to the Windows side open to ordinary users: the GPU device, the desktop's sockets, and running .exe files. Root still has them. /mnt/wsl stays open: /etc/resolv.conf lives there, and closing it stops DNS.
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
  # A rerun that finds this already in place leaves the system up, so agents at work aren't killed.
  $set = (Invoke-InHive "grep -qx 'enabled=false' /etc/wsl.conf && grep -q '^\[interop\]' /etc/wsl.conf && test -e /etc/systemd/system/hive-no-windows.service && test -d /run/systemd/system") -eq 0
  if (-not $set) {
    if ((Invoke-InHive $conf) -ne 0) { Write-Host "Couldn't configure the Hive system."; return }
    & wsl.exe --terminate $distro | Out-Null
  }
  $up = $false
  foreach ($i in 1..30) {
    & wsl.exe -d $distro -u root -- test -d /run/systemd/system | Out-Null
    if ($LASTEXITCODE -eq 0) { $up = $true; break }
    Start-Sleep -Seconds 1
  }
  if (-not $up) { Write-Host "systemd didn't start in the Hive system. Update WSL with: wsl --update   then run this again."; return }

  # 4. The Linux installer does the rest. It updates a Hive that's already there.
  if ((Invoke-InHive "curl -fsSL $site/install.sh | sh") -ne 0) { Write-Host 'The install inside the Hive system failed; the lines above say why. Fix that and run this again.'; return }

  # 5. A hive command for Windows.
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
  } catch { Write-Host "Couldn't add the hive command to your PATH: $_" }

  # 6. WSL stops a system with nothing attached, so a hidden process keeps it up from sign-in on.
  $keep = '--headless wsl.exe -d ' + $distro + ' -u hive --exec sleep infinity'
  $conhost = Join-Path $env:windir 'System32\conhost.exe'
  try {
    $link = Join-Path ([Environment]::GetFolderPath('Startup')) 'Hive.lnk'
    $shell = New-Object -ComObject WScript.Shell
    $s = $shell.CreateShortcut($link); $s.TargetPath = $conhost; $s.Arguments = $keep; $s.Save()
    Start-Process $conhost -ArgumentList $keep
  } catch { Write-Host "Couldn't set the Hive to start when you sign in: $_. It runs while a hive window is open." }

  # 7. Open the app.
  $address = (& wsl.exe -d $distro -u hive --cd ~ -- bash -lc 'hive web 2>/dev/null' | Select-Object -First 1)
  Write-Host ''
  Write-Host 'The Hive is installed. This address is the key to it, so keep it to yourself:'
  if ($address) { Write-Host "  $address" } else { Write-Host "  (it didn't print just now: wait a minute, then run: hive web)" }
  Write-Host 'To see it again, open a new window and run: hive web'
  if ($address -like 'http://127.0.0.1*') { try { Start-Process $address } catch { } }
}
Install-Hive
