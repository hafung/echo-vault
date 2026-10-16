# Compatibility entry for existing shortcuts.
param([switch]$Check)
& (Join-Path $PSScriptRoot 'start-gpt-vault.ps1') @PSBoundParameters
exit $LASTEXITCODE
