# Isolated installer regression test; never changes PATH, registry or shortcuts.
[CmdletBinding()]
param([Parameter(Mandatory=$true)][string]$CliPath)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$cliVersion = & $CliPath version | ConvertFrom-Json
if ($LASTEXITCODE -ne 0 -or $cliVersion.version -notmatch '^\d+\.\d+\.\d+(?:-[A-Za-z0-9.-]+)?$') { throw 'Use a release-versioned CLI executable for this test.' }
$version = $cliVersion.version
$testRoot = Join-Path ([IO.Path]::GetTempPath()) ('WXAgentInstallerTest-' + [guid]::NewGuid().ToString('N'))
$package = Join-Path $testRoot 'package'
$unicode = [string][char]0x6D4B + [string][char]0x8BD5
$installRoot = Join-Path $testRoot ($unicode + ' app')
$dataRoot = Join-Path $testRoot ($unicode + ' data')
$installer = Join-Path $PSScriptRoot 'install_windows.ps1'
$uninstaller = Join-Path $PSScriptRoot 'uninstall_windows.ps1'
$beforePath = [Environment]::GetEnvironmentVariable('Path', 'User')
function Write-Manifest {
    $files = @(Get-ChildItem -LiteralPath $package -Recurse -File | Where-Object Name -ne 'release-manifest.json' | ForEach-Object {
        @{ path = $_.FullName.Substring($package.Length + 1).Replace('\', '/'); sha256 = (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant(); size = $_.Length }
    })
    $manifest = @{ version = $version; platform = 'windows'; arch = 'x64'; files = $files }
    [IO.File]::WriteAllText((Join-Path $package 'release-manifest.json'), ($manifest | ConvertTo-Json -Depth 6))
}
function Assert-Fails([scriptblock]$Action, [string]$Expected) {
    $caught = $null
    try { & $Action } catch { $caught = $_ }
    if ($null -eq $caught -or $caught.ToString() -notlike "*$Expected*") { throw "Expected failure containing '$Expected'; received '$caught'" }
}
function Assert-Hash([string]$Path, [string]$Expected) {
    if ((Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash -ne $Expected) { throw "Unexpected file modification: $Path" }
}
try {
    foreach ($dir in @($package, (Join-Path $package 'backend'), (Join-Path $package 'frontend'))) { New-Item -ItemType Directory -Path $dir -Force | Out-Null }
    Copy-Item -LiteralPath $CliPath -Destination (Join-Path $package 'backend\wxagent.exe')
    Copy-Item -LiteralPath $installer, $uninstaller -Destination $package
    # GUI placeholders are checksummed but never executed in this CLI-only test.
    [IO.File]::WriteAllText((Join-Path $package 'WXAgent.exe'), 'GUI launcher fixture')
    [IO.File]::WriteAllText((Join-Path $package 'frontend\wx_agent_app.exe'), 'GUI fixture')
    [IO.File]::WriteAllText((Join-Path $package 'config.json'), '{"wx_key_shared_prefs":"~/AppData/Roaming/com.example/wx_key/shared_preferences.json"}')
    Write-Manifest
    & $installer -PackageDir $package -InstallRoot $installRoot -DataRoot $dataRoot -SkipRegistration
    $shim = Join-Path $installRoot 'bin\wxagent.cmd'
    $installedVersion = & $shim version | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0 -or $installedVersion.version -ne $version) { throw 'The installed Unicode-path CLI wrapper failed.' }
    try {
        $ErrorActionPreference = 'Continue' # PS 5.1 wraps native stderr as an ErrorRecord.
        & $shim invalid-installer-test-command 2>$null
        $invalidCommandExit = $LASTEXITCODE
    } finally { $ErrorActionPreference = 'Stop' }
    if ($invalidCommandExit -ne 2) { throw 'CLI exit code was not preserved.' }
    $config = Join-Path $dataRoot 'config.json'
    $configHash = (Get-FileHash -LiteralPath $config).Hash
    $fixture = '{"session":{"wxid":"test","displayName":"Test"},"messages":[{"localId":1,"createTime":1720000000,"content":"installer regression fixture"}]}'
    [IO.File]::WriteAllText((Join-Path $dataRoot 'exports\fixture.json'), $fixture)
    & $shim import
    if ($LASTEXITCODE -ne 0) { throw 'Import through the installed wrapper failed.' }
    $database = Join-Path $dataRoot 'data\wxagent.db'
    $databaseHash = (Get-FileHash -LiteralPath $database).Hash
    & $installer -PackageDir $package -InstallRoot $installRoot -DataRoot $dataRoot -SkipRegistration
    Assert-Hash $config $configHash
    Assert-Hash $database $databaseHash
    $result = & $shim messages | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0 -or $result.total -ne 1 -or $result.messages[0].content -ne 'installer regression fixture') { throw 'Archived record did not survive reinstall.' }
    $templatePath = Join-Path $package 'config.json'
    $templateBytes = [IO.File]::ReadAllBytes($templatePath)
    [IO.File]::AppendAllText($templatePath, 'tampered')
    Assert-Fails { & $installer -PackageDir $package -InstallRoot $installRoot -DataRoot $dataRoot -SkipRegistration } 'checksum mismatch'
    [IO.File]::WriteAllBytes($templatePath, $templateBytes)
    $manifestPath = Join-Path $package 'release-manifest.json'
    $manifestBytes = [IO.File]::ReadAllBytes($manifestPath)
    $unsafe = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
    $unsafe.files[0].path = '../outside'
    [IO.File]::WriteAllText($manifestPath, ($unsafe | ConvertTo-Json -Depth 6))
    Assert-Fails { & $installer -PackageDir $package -InstallRoot $installRoot -DataRoot $dataRoot -SkipRegistration } 'Unsafe manifest path'
    [IO.File]::WriteAllBytes($manifestPath, $manifestBytes)
    [IO.File]::AppendAllText((Join-Path $package 'WXAgent.exe'), 'changed release')
    Write-Manifest
    Assert-Fails { & $installer -PackageDir $package -InstallRoot $installRoot -DataRoot $dataRoot -SkipRegistration } 'different contents'
    Assert-Hash $config $configHash
    Assert-Hash $database $databaseHash
    & $uninstaller -InstallRoot $installRoot -SkipRegistration
    if (Test-Path -LiteralPath $installRoot) { throw 'Application directory still exists after uninstall.' }
    Assert-Hash $config $configHash
    Assert-Hash $database $databaseHash
    if ([Environment]::GetEnvironmentVariable('Path', 'User') -ne $beforePath) { throw 'Isolated test changed the user PATH.' }
    Write-Host 'PASS: Unicode paths, CLI exit codes, import/query, immutable reinstall, checksum/path rejection, data-preserving uninstall.'
    $resolved = [IO.Path]::GetFullPath($testRoot)
    $tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\') + '\'
    if (-not $resolved.StartsWith($tempRoot, [StringComparison]::OrdinalIgnoreCase) -or (Split-Path -Leaf $resolved) -notlike 'WXAgentInstallerTest-*') { throw 'Unsafe test cleanup path.' }
    Remove-Item -LiteralPath $resolved -Recurse -Force
} catch {
    Write-Warning "Test artifacts retained at: $testRoot"
    throw
}
