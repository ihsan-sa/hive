# Checks Set-UserPath in install.ps1 and uninstall.ps1 against the real user PATH,
# then puts it back: a PATH it has no reason to change stays as stored, and a change
# keeps %VAR% entries and the REG_EXPAND_SZ type. It also checks that retiring the
# old Programs\hive-v0 spares the install folder. Windows only: pwsh install/path_check.ps1
$ErrorActionPreference = 'Stop'
$k = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment')
$saved = $k.GetValue('Path', $null, 'DoNotExpandEnvironmentNames')
$savedKind = if ($null -ne $saved) { $k.GetValueKind('Path') }
# Each case starts from its own PATH; the trailing ';' shows an untouched value.
function Case([string]$Remove, [string]$Add, [string]$Want, [string]$Why) {
  $k.SetValue('Path', '%USERPROFILE%\bin;C:\Tools;', 'ExpandString')
  Set-UserPath -Remove $Remove -Add $Add
  $got = $k.GetValue('Path', $null, 'DoNotExpandEnvironmentNames')
  if ($got -cne $Want -or $k.GetValueKind('Path') -ne 'ExpandString') { throw "${script} ${Why}: PATH is '$got' ($($k.GetValueKind('Path')))" }
}
try {
  foreach ($script in 'install.ps1', 'uninstall.ps1') {
    $ast = [Management.Automation.Language.Parser]::ParseFile((Join-Path $PSScriptRoot $script), [ref]$null, [ref]$null)
    $fn = $ast.Find({ param($a) $a -is [Management.Automation.Language.FunctionDefinitionAst] -and $a.Name -eq 'Set-UserPath' }, $true)
    if (-not $fn) { throw "$script has no Set-UserPath" }
    Invoke-Expression $fn.Extent.Text
    Case -Remove 'C:\Not\There' -Want '%USERPROFILE%\bin;C:\Tools;' -Why 'rewrote a PATH without the folder'
    Case -Add 'C:\Tools' -Want '%USERPROFILE%\bin;C:\Tools;' -Why 'rewrote a PATH that has the folder'
    Case -Add 'C:\Hive' -Want '%USERPROFILE%\bin;C:\Tools;C:\Hive' -Why 'added the folder badly'
    Case -Remove 'C:\Tools' -Want '%USERPROFILE%\bin' -Why 'removed the folder badly'
    Write-Host "$script keeps the user PATH as stored"
  }
  # Retiring Programs\hive-v0 never removes the install folder or one above it.
  $ast = [Management.Automation.Language.Parser]::ParseFile((Join-Path $PSScriptRoot 'install.ps1'), [ref]$null, [ref]$null)
  Invoke-Expression $ast.Find({ param($a) $a -is [Management.Automation.Language.FunctionDefinitionAst] -and $a.Name -eq 'Test-Outside' }, $true).Extent.Text
  $old = Join-Path $env:LOCALAPPDATA 'Programs\hive-v0'
  foreach ($dir in "$old\", $old.ToUpper(), "$old/", "$old\bin") {
    if (Test-Outside $dir $old) { throw "retiring $old would remove the install folder $dir" }
  }
  foreach ($dir in (Join-Path $env:LOCALAPPDATA 'Programs\hive'), "$old-2", (Join-Path $env:LOCALAPPDATA 'Programs')) {
    if (-not (Test-Outside $dir $old)) { throw "an install in $dir would keep the old $old" }
  }
  Write-Host 'install.ps1 never retires its own install folder'
} finally {
  if ($null -ne $saved) { $k.SetValue('Path', $saved, $savedKind) } else { $k.DeleteValue('Path', $false) }
  $k.Close()
}
