param([switch]$Check)
$ErrorActionPreference = 'Stop'
$project = Split-Path -Parent $PSScriptRoot

try {
    if ($project -match '^\\\\wsl(?:\.localhost|\$)\\([^\\]+)\\(.*)$') {
        $distribution = $Matches[1]
        $linuxProject = '/' + $Matches[2].Replace('\', '/')
        if ($Check) {
            @{ mode = 'wsl'; distribution = $distribution; project = $linuxProject; binaryExists = (Test-Path (Join-Path $project 'bin\echo-vault')) } | ConvertTo-Json -Compress
            exit 0
        }
        & wsl.exe --distribution $distribution --cd $linuxProject --exec bash ./scripts/start-echo-vault.sh
        exit $LASTEXITCODE
    }

    $binary = Join-Path $project 'bin\echo-vault.exe'
    if ($Check) {
        @{ mode = 'windows'; project = $project; binaryExists = (Test-Path $binary) } | ConvertTo-Json -Compress
        exit 0
    }
    if (!(Test-Path $binary)) { throw 'Build bin\echo-vault.exe first. See docs\guide.md.' }
    Push-Location $project
    try { & $binary --open-browser; $result = $LASTEXITCODE } finally { Pop-Location }
    exit $result
} catch {
    [Console]::Error.WriteLine($_.Exception.Message)
    exit 1
}
