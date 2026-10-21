# Removes WXAgent application files and owned registrations. User data is retained.
[CmdletBinding()]
param(
    [string]$InstallRoot = (Join-Path $env:LOCALAPPDATA 'Programs\WXAgent'),
    [switch]$SkipRegistration
)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
function Get-FullPath([string]$Path) { return [IO.Path]::GetFullPath($Path).TrimEnd([IO.Path]::DirectorySeparatorChar) }
function Test-Within([string]$Path, [string]$Parent) { return $Path.Equals($Parent, [StringComparison]::OrdinalIgnoreCase) -or $Path.StartsWith($Parent + '\', [StringComparison]::OrdinalIgnoreCase) }
function Assert-PlainPath([string]$Path) {
    $cursor = $Path
    while ($cursor) {
        if ((Test-Path -LiteralPath $cursor) -and ((Get-Item -LiteralPath $cursor -Force).Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw "Links/junctions are not allowed: $cursor" }
        $parent = Split-Path -Parent $cursor
        if ($parent -eq $cursor) { break }
        $cursor = $parent
    }
}
$InstallRoot = Get-FullPath $InstallRoot
Assert-PlainPath $InstallRoot
$markerPath = Join-Path $InstallRoot 'current.json'
$marker = Get-Content -LiteralPath $markerPath -Raw -Encoding UTF8 | ConvertFrom-Json
if ($marker.product -ne 'WXAgent' -or (Get-FullPath $marker.install_root) -ne $InstallRoot -or $InstallRoot -eq [IO.Path]::GetPathRoot($InstallRoot).TrimEnd('\')) { throw 'Invalid WXAgent installation marker.' }
if ($SkipRegistration -and $marker.registered) { throw 'SkipRegistration cannot uninstall a registered installation.' }
$dataRoot = Get-FullPath $marker.data_root
if ((Test-Within $dataRoot $InstallRoot) -or (Test-Within $InstallRoot $dataRoot)) { throw 'Data and application directories overlap; nothing was removed.' }
# Refuse removal while any executable from this installation is running.
$running = @(Get-CimInstance Win32_Process | Where-Object { $_.ExecutablePath -and (Test-Within (Get-FullPath $_.ExecutablePath) $InstallRoot) })
if ($running.Count -gt 0) { throw ('Close WXAgent and its CLI/backend before uninstalling. Running process IDs: ' + (($running | ForEach-Object { $_.ProcessId }) -join ', ')) }
$removePaths = @('versions', 'bin', 'uninstall_windows.ps1', 'current.json') | ForEach-Object { Get-FullPath (Join-Path $InstallRoot $_) }
foreach ($path in $removePaths) {
    if (-not (Test-Within $path $InstallRoot) -or $path -eq $InstallRoot) { throw "Unsafe removal target: $path" }
    Assert-PlainPath $path
    if (Test-Path -LiteralPath $path -PathType Container) {
        foreach ($item in Get-ChildItem -LiteralPath $path -Recurse -Force) {
            if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw "Unexpected link in installation: $($item.FullName)" }
        }
    }
}
if (-not $SkipRegistration -and $marker.registered) {
    $uninstallKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\WXAgent'
    $appPathKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\App Paths\WXAgent.exe'
    if (Test-Path -LiteralPath $uninstallKey) {
        $location = (Get-Item -LiteralPath $uninstallKey).GetValue('InstallLocation')
        if ($location -and (Get-FullPath $location) -eq $InstallRoot) { Remove-Item -LiteralPath $uninstallKey -Force }
    }
    if (Test-Path -LiteralPath $appPathKey) {
        $appPath = (Get-Item -LiteralPath $appPathKey).GetValue('')
        if ($appPath -and (Test-Within (Get-FullPath $appPath) $InstallRoot)) { Remove-Item -LiteralPath $appPathKey -Force }
    }
    $binRoot = Join-Path $InstallRoot 'bin'
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $entries = @($userPath -split ';' | Where-Object { $_ -and $_.TrimEnd('\') -ine $binRoot })
    [Environment]::SetEnvironmentVariable('Path', ($entries -join ';'), 'User')
    try {
        if (-not ('WXAgentEnvironmentNotification' -as [type])) {
            Add-Type -TypeDefinition 'using System; using System.Runtime.InteropServices; public static class WXAgentEnvironmentNotification { [DllImport("user32.dll", CharSet=CharSet.Auto, SetLastError=true)] public static extern IntPtr SendMessageTimeout(IntPtr hwnd, uint msg, UIntPtr wparam, string lparam, uint flags, uint timeout, out UIntPtr result); }'
        }
        $result = [UIntPtr]::Zero
        [void][WXAgentEnvironmentNotification]::SendMessageTimeout([IntPtr]0xffff, 0x001a, [UIntPtr]::Zero, 'Environment', 2, 3000, [ref]$result)
    } catch { Write-Warning 'Environment change notification failed; restart the terminal after signing in again if needed.' }
    $shell = New-Object -ComObject WScript.Shell
    foreach ($path in $marker.shortcuts) {
        # Only remove the two documented per-user shortcuts, still pointing here.
        $allowed = @((Join-Path ([Environment]::GetFolderPath('Desktop')) 'WXAgent.lnk'), (Join-Path ([Environment]::GetFolderPath('Programs')) 'WXAgent.lnk'))
        if ($path -notin $allowed) { continue }
        if (Test-Path -LiteralPath $path) {
            $shortcut = $shell.CreateShortcut($path)
            if ($shortcut.TargetPath -and (Test-Within (Get-FullPath $shortcut.TargetPath) $InstallRoot)) { Remove-Item -LiteralPath $path -Force }
        }
    }
}
foreach ($path in $removePaths) { if (Test-Path -LiteralPath $path) { Remove-Item -LiteralPath $path -Recurse -Force } }
if (@(Get-ChildItem -LiteralPath $InstallRoot -Force).Count -eq 0) { Remove-Item -LiteralPath $InstallRoot -Force }
Write-Host 'WXAgent application and owned registrations were removed.'
Write-Host "Your configuration, exports and database were retained at: $dataRoot"
