# Per-user, versioned WXAgent installation. Compatible with Windows PowerShell 5.1.
[CmdletBinding()]
param(
    [string]$PackageDir = '',
    [string]$InstallRoot = (Join-Path $env:LOCALAPPDATA 'Programs\WXAgent'),
    [string]$DataRoot = (Join-Path $env:LOCALAPPDATA 'WXAgent'),
    [switch]$SkipRegistration,
    [switch]$NoShortcuts
)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
# Windows PowerShell 5.1 does not populate PSScriptRoot while evaluating param
# defaults under -File. Resolve it in the script body for the Install.cmd entry.
if ([string]::IsNullOrWhiteSpace($PackageDir)) { $PackageDir = $PSScriptRoot }

function Get-FullPath([string]$Path) {
    return [IO.Path]::GetFullPath($Path).TrimEnd([IO.Path]::DirectorySeparatorChar)
}
function Test-Within([string]$Path, [string]$Parent) {
    return $Path.Equals($Parent, [StringComparison]::OrdinalIgnoreCase) -or $Path.StartsWith($Parent + '\', [StringComparison]::OrdinalIgnoreCase)
}
function Assert-PlainPath([string]$Path) {
    $cursor = $Path
    while ($cursor) {
        if (Test-Path -LiteralPath $cursor) {
            if ((Get-Item -LiteralPath $cursor -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) {
                throw "Links/junctions are not allowed in installation paths: $cursor"
            }
        }
        $parent = Split-Path -Parent $cursor
        if ($parent -eq $cursor) { break }
        $cursor = $parent
    }
}
function Read-Package([string]$Directory) {
    Assert-PlainPath $Directory
    foreach ($item in Get-ChildItem -LiteralPath $Directory -Recurse -Force) {
        if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw "Links/junctions are not allowed in release packages: $($item.FullName)" }
    }
    $manifestPath = Join-Path $Directory 'release-manifest.json'
    $manifest = Get-Content -LiteralPath $manifestPath -Raw -Encoding UTF8 | ConvertFrom-Json
    if ($manifest.version -notmatch '^\d+\.\d+\.\d+(?:-[A-Za-z0-9.-]+)?$' -or $manifest.platform -ne 'windows' -or $manifest.arch -ne 'x64') {
        throw 'Invalid release version or unsupported platform in release-manifest.json.'
    }
    $listed = @{}
    foreach ($entry in $manifest.files) {
        $relative = [string]$entry.path
        if ([IO.Path]::IsPathRooted($relative) -or $relative -match '(^|[/\\])\.\.([/\\]|$)' -or $relative -match '[:\r\n]') {
            throw "Unsafe manifest path: $relative"
        }
        $full = Get-FullPath (Join-Path $Directory $relative)
        if (-not (Test-Within $full $Directory) -or $listed.ContainsKey($full)) { throw "Invalid or duplicate manifest path: $relative" }
        Assert-PlainPath $full
        $file = Get-Item -LiteralPath $full -Force
        if ($file.PSIsContainer -or $file.Length -ne $entry.size -or (Get-FileHash -LiteralPath $full -Algorithm SHA256).Hash -ne $entry.sha256) {
            throw "Release checksum mismatch: $relative"
        }
        $listed[$full] = $true
    }
    foreach ($file in Get-ChildItem -LiteralPath $Directory -File -Recurse -Force) {
        if ($file.FullName -ne $manifestPath -and -not $listed.ContainsKey($file.FullName)) { throw "Unlisted package file: $($file.FullName)" }
    }
    foreach ($required in @('WXAgent.exe', 'backend\wxagent.exe', 'frontend\wx_agent_app.exe', 'config.json', 'install_windows.ps1', 'uninstall_windows.ps1')) {
        if (-not $listed.ContainsKey((Join-Path $Directory $required))) { throw "Release is missing $required" }
    }
    return $manifest
}
function Write-Utf8([string]$Path, [string]$Content) {
    $temporary = $Path + '.' + [guid]::NewGuid().ToString('N') + '.tmp'
    $backup = $temporary + '.bak'
    try {
        [IO.File]::WriteAllText($temporary, $Content, (New-Object Text.UTF8Encoding($false)))
        if (Test-Path -LiteralPath $Path) { [IO.File]::Replace($temporary, $Path, $backup) }
        else { [IO.File]::Move($temporary, $Path) }
    } finally {
        foreach ($leftover in @($temporary, $backup)) { if (Test-Path -LiteralPath $leftover) { Remove-Item -LiteralPath $leftover -Force } }
    }
}
function Send-EnvironmentChange {
    try {
        if (-not ('WXAgentEnvironmentNotification' -as [type])) {
            Add-Type -TypeDefinition 'using System; using System.Runtime.InteropServices; public static class WXAgentEnvironmentNotification { [DllImport("user32.dll", CharSet=CharSet.Auto, SetLastError=true)] public static extern IntPtr SendMessageTimeout(IntPtr hwnd, uint msg, UIntPtr wparam, string lparam, uint flags, uint timeout, out UIntPtr result); }'
        }
        $result = [UIntPtr]::Zero
        [void][WXAgentEnvironmentNotification]::SendMessageTimeout([IntPtr]0xffff, 0x001a, [UIntPtr]::Zero, 'Environment', 2, 3000, [ref]$result)
    } catch { Write-Warning 'Environment change notification failed. Sign out and sign in if a new terminal cannot find wxagent.' }
}
function Save-FileSnapshot([string]$Path) {
    if (Test-Path -LiteralPath $Path) { return ,([IO.File]::ReadAllBytes($Path)) }
    return $null
}
function Restore-File([string]$Path, $Bytes) {
    if ($null -eq $Bytes) {
        if (Test-Path -LiteralPath $Path) { Remove-Item -LiteralPath $Path -Force }
    } else { [IO.File]::WriteAllBytes($Path, $Bytes) }
}
function Save-Registry([string]$Path) {
    if (-not (Test-Path -LiteralPath $Path)) { return $null }
    $key = Get-Item -LiteralPath $Path
    if (@($key.GetSubKeyNames()).Count -gt 0) { throw "Unexpected subkeys in application registration: $Path" }
    $values = @{}
    foreach ($name in $key.GetValueNames()) {
        $values[$name] = @{ Value = $key.GetValue($name, $null, [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames); Kind = $key.GetValueKind($name) }
    }
    return $values
}
function Restore-Registry([string]$Path, $Snapshot) {
    if (Test-Path -LiteralPath $Path) { Remove-Item -LiteralPath $Path -Force }
    if ($null -ne $Snapshot) {
        $key = New-Item -Path $Path -Force
        foreach ($name in $Snapshot.Keys) { $key.SetValue($name, $Snapshot[$name].Value, $Snapshot[$name].Kind) }
    }
}

$PackageDir = Get-FullPath $PackageDir
$InstallRoot = Get-FullPath $InstallRoot
$DataRoot = Get-FullPath $DataRoot
if ($InstallRoot -eq [IO.Path]::GetPathRoot($InstallRoot).TrimEnd('\') -or (Test-Within $DataRoot $InstallRoot) -or (Test-Within $InstallRoot $DataRoot)) {
    throw 'InstallRoot must be a dedicated directory, separate from DataRoot.'
}
Assert-PlainPath $InstallRoot
Assert-PlainPath $DataRoot
$manifest = Read-Package $PackageDir
$version = [string]$manifest.version
$versionRoot = Join-Path $InstallRoot "versions\$version"
$binRoot = Join-Path $InstallRoot 'bin'
$configPath = Join-Path $DataRoot 'config.json'
$markerPath = Join-Path $InstallRoot 'current.json'
$wrapperPath = Join-Path $binRoot 'wxagent.cmd'
$uninstallerPath = Join-Path $InstallRoot 'uninstall_windows.ps1'
$uninstallKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\WXAgent'
$appPathKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\App Paths\WXAgent.exe'
$hadManagedInstall = Test-Path -LiteralPath $markerPath
if (Test-Path -LiteralPath $markerPath) {
    $previous = Get-Content -LiteralPath $markerPath -Raw -Encoding UTF8 | ConvertFrom-Json
    if ($previous.product -ne 'WXAgent' -or (Get-FullPath $previous.install_root) -ne $InstallRoot -or (Get-FullPath $previous.data_root) -ne $DataRoot) {
        throw 'An existing installation uses a different data directory. Reuse its InstallRoot and DataRoot.'
    }
    if ($previous.registered -and $SkipRegistration) { throw 'SkipRegistration cannot update an already registered installation.' }
} elseif (Test-Path -LiteralPath $InstallRoot) {
    if (@(Get-ChildItem -LiteralPath $InstallRoot -Force).Count -gt 0) { throw 'InstallRoot is not empty and is not a managed WXAgent installation.' }
}

$staging = Join-Path $InstallRoot ('.staging-' + [guid]::NewGuid().ToString('N'))
$createdVersion = $false
$registrationChanged = $false
$snapshots = @{}
$registrySnapshots = @{}
$shortcutSnapshots = @{}
$userPathBefore = [Environment]::GetEnvironmentVariable('Path', 'User')
$pathChanged = $false
$completed = $false
try {
    New-Item -ItemType Directory -Path $InstallRoot -Force | Out-Null
    if (Test-Path -LiteralPath $versionRoot) {
        $existing = Read-Package $versionRoot
        if ($existing.version -ne $version -or (Get-FileHash -LiteralPath (Join-Path $versionRoot 'release-manifest.json')).Hash -ne (Get-FileHash -LiteralPath (Join-Path $PackageDir 'release-manifest.json')).Hash) {
            throw 'This version is already installed with different contents. It will not be overwritten.'
        }
    } else {
        New-Item -ItemType Directory -Path $staging | Out-Null
        foreach ($item in Get-ChildItem -LiteralPath $PackageDir -Force) { Copy-Item -LiteralPath $item.FullName -Destination $staging -Recurse -Force }
        $null = Read-Package $staging
        $cliVersion = & (Join-Path $staging 'backend\wxagent.exe') version
        if ($LASTEXITCODE -ne 0) { throw 'The staged CLI failed its version check.' }
        if (($cliVersion | ConvertFrom-Json).version -ne $version) { throw 'CLI version does not match the release manifest.' }
        New-Item -ItemType Directory -Path (Split-Path -Parent $versionRoot) -Force | Out-Null
        if (-not (Test-Within (Get-FullPath $staging) $InstallRoot) -or -not (Test-Within (Get-FullPath $versionRoot) $InstallRoot) -or (Get-FullPath $staging) -eq $InstallRoot -or (Get-FullPath $versionRoot) -eq $InstallRoot) {
            throw 'Unsafe installation staging or destination path.'
        }
        Move-Item -LiteralPath $staging -Destination $versionRoot
        $createdVersion = $true
    }
    New-Item -ItemType Directory -Path $DataRoot -Force | Out-Null
    if (-not (Test-Path -LiteralPath $configPath)) {
        $template = Get-Content -LiteralPath (Join-Path $versionRoot 'config.json') -Raw -Encoding UTF8 | ConvertFrom-Json
        $configuration = [ordered]@{
            database_path = (Join-Path $DataRoot 'data\wxagent.db')
            export_dir = (Join-Path $DataRoot 'exports')
            state_file = (Join-Path $DataRoot '.wxagent\state.json')
            wechat_data_path = ''
            wx_key_shared_prefs = [string]$template.wx_key_shared_prefs
            wx_key_command = @{
                path = (Join-Path $versionRoot 'modules\wx_key\wx_key.exe')
                cwd = (Join-Path $versionRoot 'modules\wx_key')
            }
        }
        foreach ($directory in @('data', 'exports', '.wxagent')) { New-Item -ItemType Directory -Path (Join-Path $DataRoot $directory) -Force | Out-Null }
        $stream = [IO.File]::Open($configPath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
        try {
            $bytes = (New-Object Text.UTF8Encoding($false)).GetBytes(($configuration | ConvertTo-Json -Depth 8))
            $stream.Write($bytes, 0, $bytes.Length)
        } finally { $stream.Dispose() }
    } else {
        $preservedConfig = Get-Content -LiteralPath $configPath -Raw -Encoding UTF8 | ConvertFrom-Json
        if ($null -eq $preservedConfig -or $preservedConfig -isnot [pscustomobject]) { throw 'Existing config.json must contain a JSON object; it has been preserved.' }
    }
    New-Item -ItemType Directory -Path $binRoot -Force | Out-Null
    foreach ($path in @($wrapperPath, $markerPath, $uninstallerPath)) { $snapshots[$path] = Save-FileSnapshot $path }
    $exe = (Join-Path $versionRoot 'backend\wxagent.exe').Replace('%', '%%')
    $cfg = $configPath.Replace('%', '%%')
    # The ASCII prefix switches cmd.exe to UTF-8 before it reads Unicode paths.
    # Restore the caller's code page without losing the native CLI exit status.
    Write-Utf8 $wrapperPath "@echo off`r`nsetlocal DisableDelayedExpansion`r`nfor /f `"tokens=2 delims=:`" %%a in ('chcp') do set `"WXAGENT_ORIGINAL_CP=%%a`"`r`nchcp 65001 >nul`r`n`"$exe`" --config `"$cfg`" %*`r`nset `"WXAGENT_EXIT_CODE=%errorlevel%`"`r`nif defined WXAGENT_ORIGINAL_CP chcp %WXAGENT_ORIGINAL_CP% >nul`r`nexit /b %WXAGENT_EXIT_CODE%`r`n"
    Copy-Item -LiteralPath (Join-Path $versionRoot 'uninstall_windows.ps1') -Destination $uninstallerPath -Force

    $shortcutPaths = @()
    if ($NoShortcuts -and (Test-Path -LiteralPath $markerPath)) { $shortcutPaths = @($previous.shortcuts) }
    if (-not $SkipRegistration) {
        if (Test-Path -LiteralPath $uninstallKey) {
            $oldLocation = (Get-Item -LiteralPath $uninstallKey).GetValue('InstallLocation')
            if (-not $oldLocation -or (Get-FullPath $oldLocation) -ne $InstallRoot) { throw 'Another WXAgent installation already owns the uninstall registration.' }
        }
        if (Test-Path -LiteralPath $appPathKey) {
            $oldAppPath = (Get-Item -LiteralPath $appPathKey).GetValue('')
            if (-not $oldAppPath -or -not (Test-Within (Get-FullPath $oldAppPath) $InstallRoot)) { throw 'Another application already owns the WXAgent.exe registration.' }
        }
        foreach ($key in @($uninstallKey, $appPathKey)) { $registrySnapshots[$key] = Save-Registry $key }
        $registrationChanged = $true
        $key = New-Item -Path $uninstallKey -Force
        $values = @{
            DisplayName = 'WXAgent'; DisplayVersion = $version; Publisher = 'WXAgent'
            InstallLocation = $InstallRoot; DisplayIcon = (Join-Path $versionRoot 'frontend\wx_agent_app.exe')
            UninstallString = "powershell.exe -NoProfile -ExecutionPolicy Bypass -File `"$uninstallerPath`" -InstallRoot `"$InstallRoot`""
            URLInfoAbout = 'https://github.com/angelo-xuang/WXAgent'; InstallDate = (Get-Date -Format 'yyyyMMdd')
        }
        foreach ($name in $values.Keys) { $key.SetValue($name, $values[$name], [Microsoft.Win32.RegistryValueKind]::String) }
        $key.SetValue('NoModify', 1, [Microsoft.Win32.RegistryValueKind]::DWord)
        $key.SetValue('NoRepair', 1, [Microsoft.Win32.RegistryValueKind]::DWord)
        $appKey = New-Item -Path $appPathKey -Force
        $appKey.SetValue('', (Join-Path $versionRoot 'WXAgent.exe'))
        $appKey.SetValue('Path', $versionRoot)
        $pathEntries = @($userPathBefore -split ';' | Where-Object { $_ })
        if (-not ($pathEntries | Where-Object { $_.TrimEnd('\') -ieq $binRoot })) {
            [Environment]::SetEnvironmentVariable('Path', (($pathEntries + $binRoot) -join ';'), 'User')
            $pathChanged = $true
        }
        if (-not $NoShortcuts) {
            $shell = New-Object -ComObject WScript.Shell
            foreach ($folder in @([Environment]::GetFolderPath('Desktop'), [Environment]::GetFolderPath('Programs'))) {
                if (-not $folder) { throw 'Unable to locate the current user shortcut folders.' }
                $path = Join-Path $folder 'WXAgent.lnk'
                $shortcut = $shell.CreateShortcut($path)
                if ((Test-Path -LiteralPath $path) -and -not (Test-Within (Get-FullPath $shortcut.TargetPath) $InstallRoot)) { throw "An unrelated shortcut already exists: $path" }
                $shortcutSnapshots[$path] = Save-FileSnapshot $path
                $shortcut.TargetPath = Join-Path $versionRoot 'WXAgent.exe'
                $shortcut.Arguments = "--config `"$configPath`""
                $shortcut.WorkingDirectory = $versionRoot
                $shortcut.Description = 'WXAgent local chat record index'
                $shortcut.IconLocation = (Join-Path $versionRoot 'frontend\wx_agent_app.exe') + ',0'
                $shortcut.Save()
                $shortcutPaths += $path
            }
        }
    }
    $marker = [ordered]@{ product = 'WXAgent'; version = $version; install_root = $InstallRoot; data_root = $DataRoot; version_root = $versionRoot; registered = (-not $SkipRegistration); shortcuts = $shortcutPaths; installed_at_utc = [DateTime]::UtcNow.ToString('o') }
    Write-Utf8 $markerPath ($marker | ConvertTo-Json -Depth 5)
    $completed = $true
    if ($pathChanged) { Send-EnvironmentChange }
    Write-Host "Installed WXAgent $version."
    Write-Host "Application: $versionRoot"
    Write-Host "User data:   $DataRoot"
    if (-not $SkipRegistration) { Write-Host 'Open a new terminal to use: wxagent status' }
} catch {
    $failure = $_
    foreach ($path in $shortcutSnapshots.Keys) { Restore-File $path $shortcutSnapshots[$path] }
    if ($pathChanged) { [Environment]::SetEnvironmentVariable('Path', $userPathBefore, 'User') }
    if ($registrationChanged) { foreach ($key in $registrySnapshots.Keys) { Restore-Registry $key $registrySnapshots[$key] } }
    foreach ($path in $snapshots.Keys) { Restore-File $path $snapshots[$path] }
    if ($createdVersion -and (Test-Within (Get-FullPath $versionRoot) $InstallRoot)) { Remove-Item -LiteralPath $versionRoot -Recurse -Force }
    throw $failure
} finally {
    if ((Test-Path -LiteralPath $staging) -and (Test-Within (Get-FullPath $staging) $InstallRoot)) { Remove-Item -LiteralPath $staging -Recurse -Force }
    # A failed first installation must be retryable. Remove only empty folders
    # created by the installer; never remove preserved user data or other files.
    if (-not $completed -and -not $hadManagedInstall) {
        foreach ($directory in @($binRoot, (Join-Path $InstallRoot 'versions'), $InstallRoot)) {
            if ((Test-Path -LiteralPath $directory -PathType Container) -and @(Get-ChildItem -LiteralPath $directory -Force).Count -eq 0) {
                Remove-Item -LiteralPath $directory -Force
            }
        }
    }
}
