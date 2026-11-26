# Starts the three InsightFlow AI dev runtimes in separate windows.
# Prerequisites: run scripts/check-env.ps1 first.
# Usage: powershell -ExecutionPolicy Bypass -File scripts/dev.ps1
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot

Write-Host "Starting InsightFlow AI dev environment..." -ForegroundColor Cyan

Start-Process powershell -ArgumentList @(
    "-NoExit", "-Command",
    ". '$root\scripts\load-env.ps1'; Set-Location '$root\apps\api'; `$env:APP_ENV='development'; go run ./cmd/api"
) -WindowStyle Normal

Start-Process powershell -ArgumentList @(
    "-NoExit", "-Command",
    ". '$root\scripts\load-env.ps1'; Set-Location '$root\apps\engine'; if (Test-Path .venv\Scripts\Activate.ps1) { . .venv\Scripts\Activate.ps1 }; python -m app.main"
) -WindowStyle Normal

Start-Process powershell -ArgumentList @(
    "-NoExit", "-Command",
    "Set-Location '$root\apps\web'; npm run dev"
) -WindowStyle Normal

Write-Host "Launched API (8080), Engine (8000), Web (5173)." -ForegroundColor Green
