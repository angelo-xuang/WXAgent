param(
    [ValidateSet('check', 'format')]
    [string]$Task = 'check',
    [ValidateSet('all', 'go', 'flutter')]
    [string]$Component = 'all'
)

$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path $PSScriptRoot -Parent

function Invoke-Checked {
    param([string]$Command, [string[]]$Arguments)
    & $Command @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$Command $($Arguments -join ' ') failed (exit $LASTEXITCODE)."
    }
}

Push-Location $projectRoot
try {
    if ($Component -in @('all', 'go')) {
        Get-Command go, gofmt -ErrorAction Stop | Out-Null
        # Limit formatting to first-party Go modules; never traverse modules/.
        $goFiles = @(Get-ChildItem backend, launcher -Recurse -File -Filter '*.go' |
            Where-Object { $_.FullName -notmatch '[\\/]vendor[\\/]' })
        $unformatted = @()
        foreach ($file in $goFiles) {
            if ($Task -eq 'format') {
                Invoke-Checked gofmt @('-w', $file.FullName)
            } else {
                $result = & gofmt -l $file.FullName
                if ($LASTEXITCODE -ne 0) { throw "gofmt failed: $($file.FullName)" }
                $unformatted += $result | Where-Object { $_ }
            }
        }
        if ($unformatted.Count -gt 0) {
            throw "Go formatting differs. Run scripts/dev.ps1 format go.`n$($unformatted -join "`n")"
        }
        if ($Task -eq 'check') {
            foreach ($module in @('backend', 'launcher')) {
                Push-Location $module
                try {
                    Invoke-Checked go @('test', './...')
                    Invoke-Checked go @('vet', './...')
                } finally { Pop-Location }
            }
        }
    }

    if ($Component -in @('all', 'flutter')) {
        Get-Command flutter, dart -ErrorAction Stop | Out-Null
        Push-Location frontend
        try {
            if ($Task -eq 'format') {
                Invoke-Checked dart @('format', 'lib', 'test')
            } else {
                Invoke-Checked flutter @('pub', 'get', '--enforce-lockfile')
                Invoke-Checked dart @('format', '--output=none', '--set-exit-if-changed', 'lib', 'test')
                Invoke-Checked flutter @('analyze', '--no-pub')
                Invoke-Checked flutter @('test', '--no-pub')
            }
        } finally { Pop-Location }
    }
    Write-Host "Completed: $Task ($Component)."
} finally { Pop-Location }
