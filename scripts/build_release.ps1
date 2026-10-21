# Build a Windows x64 release without replacing existing packages or user data.
[CmdletBinding()]
param(
    [ValidatePattern('^\d+\.\d+\.\d+$')]
    [string]$Version = '1.1.0',
    [ValidateRange(1, 65535)]
    [int]$BuildNumber = 2,
    [string]$OutputDir = ''
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$RepoRoot = [IO.Path]::GetFullPath((Split-Path $PSScriptRoot -Parent))
$PackageName = "WXAgent-v$Version-windows-x64"
$Utf8 = New-Object Text.UTF8Encoding($false)

function Invoke-Checked {
    param([string]$Command, [string[]]$Arguments)
    & $Command @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$Command $($Arguments -join ' ') failed (exit $LASTEXITCODE)."
    }
}

function Assert-SafeDestination {
    param([string]$Path)
    $absolute = [IO.Path]::GetFullPath($Path)
    $allowed = $false
    foreach ($name in @('dist', 'release')) {
        $parent = Join-Path $RepoRoot $name
        if ($absolute.StartsWith($parent + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
            $allowed = $true
        }
    }
    if (-not $allowed) {
        throw "OutputDir must be a new child directory inside $RepoRoot\dist or $RepoRoot\release."
    }
    # Reject junctions and symbolic links, including existing parent directories.
    $cursor = $absolute
    while ($cursor -and $cursor -ne $RepoRoot) {
        if (Test-Path -LiteralPath $cursor) {
            $item = Get-Item -LiteralPath $cursor -Force
            if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) {
                throw "Output paths must not traverse links: $cursor"
            }
        }
        $cursor = Split-Path $cursor -Parent
    }
    return $absolute
}

function Get-VCTools {
    $vswhere = Join-Path ${env:ProgramFiles(x86)} 'Microsoft Visual Studio\Installer\vswhere.exe'
    if (-not (Test-Path -LiteralPath $vswhere -PathType Leaf)) {
        throw 'Visual Studio 2022 C++ build tools are required (vswhere.exe was not found).'
    }
    $installation = & $vswhere -latest -products '*' -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 -property installationPath
    if ($LASTEXITCODE -ne 0 -or -not $installation) { throw 'Visual Studio C++ tools were not found.' }
    $installation = $installation.Trim()
    $runtime = Get-ChildItem -LiteralPath (Join-Path $installation 'VC\Redist\MSVC') -Directory |
        Where-Object { $_.Name -match '^\d+(\.\d+){1,3}$' } |
        Sort-Object { [version]$_.Name } -Descending |
        ForEach-Object { Join-Path $_.FullName 'x64\Microsoft.VC143.CRT' } |
        Where-Object { Test-Path -LiteralPath $_ -PathType Container } |
        Select-Object -First 1
    $dumpbin = Get-ChildItem -LiteralPath (Join-Path $installation 'VC\Tools\MSVC') -Directory |
        Where-Object { $_.Name -match '^\d+(\.\d+){1,3}$' } |
        Sort-Object { [version]$_.Name } -Descending |
        ForEach-Object { Join-Path $_.FullName 'bin\Hostx64\x64\dumpbin.exe' } |
        Where-Object { Test-Path -LiteralPath $_ -PathType Leaf } |
        Select-Object -First 1
    if (-not $runtime -or -not $dumpbin) { throw 'The x64 VC143 redistributable DLLs or dumpbin.exe are missing.' }
    return @{ Runtime = $runtime; Dumpbin = $dumpbin }
}

function Copy-FlutterRuntime {
    param([string]$Project, [string]$Destination, [string[]]$VersionArguments = @())
    $lockPath = Join-Path $Project 'pubspec.lock'
    $lockHash = (Get-FileHash -LiteralPath $lockPath -Algorithm SHA256).Hash
    Push-Location $Project
    try {
        Invoke-Checked flutter @('pub', 'get', '--enforce-lockfile')
        if ((Get-FileHash -LiteralPath $lockPath -Algorithm SHA256).Hash -ne $lockHash) {
            throw "Dependency lockfile changed: $lockPath"
        }
        $buildArguments = @('build', 'windows', '--release', '--no-pub') + $VersionArguments
        Invoke-Checked flutter $buildArguments
    } finally { Pop-Location }
    $built = Join-Path $Project 'build\windows\x64\runner\Release'
    if (-not (Test-Path -LiteralPath $built -PathType Container)) {
        throw "Flutter release output missing: $built"
    }
    New-Item -ItemType Directory -Path $Destination | Out-Null
    # Copy only installed runtime output, never project source or existing exports.
    Get-ChildItem -LiteralPath $built -Force |
        Where-Object { $_.Extension -notin @('.pdb', '.ilk', '.exp', '.lib') } |
        ForEach-Object { Copy-Item -LiteralPath $_.FullName -Destination $Destination -Recurse }
    Get-ChildItem -LiteralPath $VCTools.Runtime -File -Filter '*.dll' |
        ForEach-Object { Copy-Item -LiteralPath $_.FullName -Destination $Destination }
}

function Assert-RuntimeDependencies {
    param([string]$Directory)
    $reports = @()
    foreach ($binary in Get-ChildItem -LiteralPath $Directory -Recurse -File |
        Where-Object { $_.Extension -in @('.exe', '.dll') }) {
        $output = & $VCTools.Dumpbin /nologo /dependents $binary.FullName
        if ($LASTEXITCODE -ne 0) { throw "Cannot inspect binary: $($binary.FullName)" }
        $imports = @($output | ForEach-Object {
            if ($_ -match '^\s+([^\s]+\.dll)\s*$') { $Matches[1] }
        } | Sort-Object -Unique)
        $appDir = $binary.DirectoryName
        if ($binary.FullName.StartsWith((Join-Path $Directory 'frontend') + '\', [StringComparison]::OrdinalIgnoreCase)) {
            $appDir = Join-Path $Directory 'frontend'
        } elseif ($binary.FullName.StartsWith((Join-Path $Directory 'modules\wx_key') + '\', [StringComparison]::OrdinalIgnoreCase)) {
            $appDir = Join-Path $Directory 'modules\wx_key'
        }
        foreach ($dependency in $imports) {
            if ($dependency -match '^(api-ms-|ext-ms-)') { continue }
            $local = (Test-Path -LiteralPath (Join-Path $binary.DirectoryName $dependency) -PathType Leaf) -or
                (Test-Path -LiteralPath (Join-Path $appDir $dependency) -PathType Leaf)
            if ($dependency -match '^(vcruntime|msvcp|concrt)\d' -and -not $local) {
                throw "Missing app-local Visual C++ runtime: $dependency ($($binary.Name))"
            }
            if (-not $local -and -not (Test-Path -LiteralPath (Join-Path "$env:SystemRoot\System32" $dependency) -PathType Leaf)) {
                throw "Unresolved runtime dependency: $dependency ($($binary.Name))"
            }
        }
        $relative = $binary.FullName.Substring($Directory.Length + 1).Replace('\', '/')
        $reports += [ordered]@{ path = $relative; imports = $imports }
    }
    return $reports
}

if ($env:OS -ne 'Windows_NT') { throw 'This script builds the Windows x64 package and must run on Windows.' }
if ([string]::IsNullOrWhiteSpace($OutputDir)) { $OutputDir = Join-Path 'dist' $PackageName }
if (-not [IO.Path]::IsPathRooted($OutputDir)) { $OutputDir = Join-Path $RepoRoot $OutputDir }
$Destination = Assert-SafeDestination $OutputDir
$Archive = $Destination + '.zip'
$Checksum = $Archive + '.sha256'
foreach ($path in @($Destination, $Archive, $Checksum)) {
    if (Test-Path -LiteralPath $path) { throw "Refusing to overwrite an existing release: $path" }
}

Get-Command go, flutter, git -ErrorAction Stop | Out-Null
$requiredFiles = @(
    'backend\config.release.json', 'frontend\pubspec.lock', 'modules\wx_key\pubspec.lock',
    'scripts\install_windows.ps1', 'scripts\uninstall_windows.ps1',
    'README.md', 'QUICKSTART.md', 'CHANGELOG.md', 'skills\wxagent-index\SKILL.md'
)
foreach ($path in $requiredFiles) {
    if (-not (Test-Path -LiteralPath (Join-Path $RepoRoot $path) -PathType Leaf)) {
        throw "Required package input missing: $path"
    }
}
$VCTools = Get-VCTools
Push-Location $RepoRoot
try {
    $sourceCommit = & git rev-parse HEAD
    if ($LASTEXITCODE -ne 0) { throw 'Cannot read the source commit.' }
    $dirty = & git status --porcelain --untracked-files=no
    if ($LASTEXITCODE -ne 0 -or $dirty) { throw 'Commit tracked source changes before creating a release package.' }
} finally { Pop-Location }

$Parent = Split-Path $Destination -Parent
New-Item -ItemType Directory -Path $Parent -Force | Out-Null
$Staging = Assert-SafeDestination (Join-Path $Parent (".$PackageName.staging-" + [guid]::NewGuid().ToString('N')))
$StagedArchive = $Staging + '.zip'
New-Item -ItemType Directory -Path $Staging | Out-Null
$OldGOOS, $OldGOARCH, $OldCGO = $env:GOOS, $env:GOARCH, $env:CGO_ENABLED

try {
    $env:GOOS = 'windows'
    $env:GOARCH = 'amd64'
    $env:CGO_ENABLED = '0'
    Write-Host "[1/5] Building CLI $Version" -ForegroundColor Cyan
    $cliOutput = Join-Path $Staging 'backend\wxagent.exe'
    New-Item -ItemType Directory -Path (Split-Path $cliOutput -Parent) | Out-Null
    Push-Location (Join-Path $RepoRoot 'backend')
    try {
        $flags = "-s -w -X github.com/billyoftea/wxagent/go_backend/internal/cli.Version=$Version"
        Invoke-Checked go @('build', '-mod=readonly', '-trimpath', "-ldflags=$flags", '-o', $cliOutput, './cmd/wxagent')
    } finally { Pop-Location }
    $cliVersion = & $cliOutput version | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0 -or $cliVersion.version -ne $Version -or $cliVersion.platform -ne 'windows' -or $cliVersion.arch -ne 'amd64') {
        throw 'Built CLI version or architecture does not match the package.'
    }

    Write-Host '[2/5] Building optional desktop interface' -ForegroundColor Cyan
    Copy-FlutterRuntime (Join-Path $RepoRoot 'frontend') (Join-Path $Staging 'frontend') @("--build-name=$Version", "--build-number=$BuildNumber")
    Write-Host '[3/5] Building launcher' -ForegroundColor Cyan
    Push-Location (Join-Path $RepoRoot 'launcher')
    try {
        Invoke-Checked go @('build', '-mod=readonly', '-trimpath', '-ldflags=-s -w -H=windowsgui', '-o', (Join-Path $Staging 'WXAgent.exe'), '.')
    } finally { Pop-Location }

    Write-Host '[4/5] Building Windows WeChat connection helper' -ForegroundColor Cyan
    New-Item -ItemType Directory -Path (Join-Path $Staging 'modules') | Out-Null
    Copy-FlutterRuntime (Join-Path $RepoRoot 'modules\wx_key') (Join-Path $Staging 'modules\wx_key')
    New-Item -ItemType Directory -Path (Join-Path $Staging 'licenses') | Out-Null
    Copy-Item -LiteralPath (Join-Path $RepoRoot 'modules\wx_key\LICENSE') -Destination (Join-Path $Staging 'licenses\wx_key.txt')
    Copy-Item -LiteralPath (Join-Path $RepoRoot 'backend\config.release.json') -Destination (Join-Path $Staging 'config.json')
    foreach ($name in @('README.md', 'QUICKSTART.md', 'CHANGELOG.md')) {
        Copy-Item -LiteralPath (Join-Path $RepoRoot $name) -Destination $Staging
    }
    foreach ($name in @('install_windows.ps1', 'uninstall_windows.ps1')) {
        Copy-Item -LiteralPath (Join-Path $RepoRoot "scripts\$name") -Destination $Staging
    }
    # Copy only tracked documentation and the portable skill instructions.
    foreach ($tree in @('docs', 'skills')) {
        $files = & git -C $RepoRoot ls-files -- $tree
        if ($LASTEXITCODE -ne 0) { throw "Cannot enumerate package $tree." }
        foreach ($relative in $files) {
            $target = Join-Path $Staging $relative
            New-Item -ItemType Directory -Path (Split-Path $target -Parent) -Force | Out-Null
            Copy-Item -LiteralPath (Join-Path $RepoRoot $relative) -Destination $target
        }
    }
    $installLauncher = "@echo off\r\npowershell.exe -NoProfile -ExecutionPolicy Bypass -File `"%~dp0install_windows.ps1`" %*\r\nif errorlevel 1 (\r\n  pause\r\n  exit /b 1\r\n)\r\npause\r\n"
    [IO.File]::WriteAllText((Join-Path $Staging 'Install.cmd'), $installLauncher.Replace('\r\n', "`r`n"), [Text.Encoding]::ASCII)

    Write-Host '[5/5] Verifying dependencies, hashing files and creating ZIP' -ForegroundColor Cyan
    $dependencies = @(Assert-RuntimeDependencies $Staging)
    [IO.File]::WriteAllText((Join-Path $Staging 'runtime-dependencies.json'), ($dependencies | ConvertTo-Json -Depth 5), $Utf8)
    $dirty = & git -C $RepoRoot status --porcelain --untracked-files=no
    if ($LASTEXITCODE -ne 0 -or $dirty) {
        throw 'A build tool changed tracked source files. Review and commit those changes, then build into a new directory.'
    }
    $currentCommit = & git -C $RepoRoot rev-parse HEAD
    if ($LASTEXITCODE -ne 0 -or $currentCommit -ne $sourceCommit) {
        throw 'The source commit changed during the build. Build again from a stable checkout.'
    }
    $files = @(Get-ChildItem -LiteralPath $Staging -Recurse -File | Sort-Object FullName | ForEach-Object {
        [ordered]@{
            path = $_.FullName.Substring($Staging.Length + 1).Replace('\', '/')
            sha256 = (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
            size = $_.Length
        }
    })
    $manifest = [ordered]@{
        name = 'WXAgent'
        version = $Version
        build_number = $BuildNumber
        platform = 'windows'
        arch = 'x64'
        source_commit = $sourceCommit.Trim()
        built_at_utc = [DateTimeOffset]::UtcNow.ToString('o')
        vc_runtime_version = (Split-Path (Split-Path $VCTools.Runtime -Parent) -Parent | Split-Path -Leaf)
        files = $files
    }
    [IO.File]::WriteAllText((Join-Path $Staging 'release-manifest.json'), ($manifest | ConvertTo-Json -Depth 6), $Utf8)
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    [IO.Compression.ZipFile]::CreateFromDirectory($Staging, $StagedArchive, [IO.Compression.CompressionLevel]::Optimal, $false)
    $zipHash = (Get-FileHash -LiteralPath $StagedArchive -Algorithm SHA256).Hash.ToLowerInvariant()
    # Recheck after compilation in case another build created a target meanwhile.
    foreach ($path in @($Destination, $Archive, $Checksum)) {
        if (Test-Path -LiteralPath $path) { throw "Refusing to overwrite an existing release: $path" }
    }
    # No existing release directory is removed or replaced.
    Move-Item -LiteralPath $Staging -Destination $Destination
    Move-Item -LiteralPath $StagedArchive -Destination $Archive
    [IO.File]::WriteAllText($Checksum, "$zipHash  $([IO.Path]::GetFileName($Archive))`n", $Utf8)
    Write-Host "Package: $Destination" -ForegroundColor Green
    Write-Host "Archive: $Archive" -ForegroundColor Green
    Write-Host "SHA256:  $zipHash" -ForegroundColor Green
} catch {
    Write-Warning "Build stopped. Diagnostic files remain at $Staging. Existing releases and user data were not removed."
    throw
} finally {
    $env:GOOS = $OldGOOS
    $env:GOARCH = $OldGOARCH
    $env:CGO_ENABLED = $OldCGO
}
