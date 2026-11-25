# Verifies local development prerequisites for InsightFlow AI (Windows native).
# Usage: powershell -ExecutionPolicy Bypass -File scripts/check-env.ps1
$ErrorActionPreference = "Stop"
$failed = 0

function Test-Runtime([string]$Name, [string]$MinHint) {
    if (Get-Command $Name -ErrorAction SilentlyContinue) {
        Write-Host ("  OK      {0} ({1})" -f $Name, $MinHint) -ForegroundColor Green
    } else {
        Write-Host ("  MISSING {0} - need {1}" -f $Name, $MinHint) -ForegroundColor Red
        $script:failed++
    }
}

function Test-Port([int]$Port, [string]$Label) {
    $client = New-Object System.Net.Sockets.TcpClient
    try {
        $client.Connect("127.0.0.1", $Port)
        Write-Host ("  UP      port {0} ({1})" -f $Port, $Label) -ForegroundColor Green
    } catch {
        Write-Host ("  DOWN    port {0} ({1})" -f $Port, $Label) -ForegroundColor Yellow
    } finally {
        $client.Dispose()
    }
}

Write-Host "Runtimes:" -ForegroundColor Cyan
Test-Runtime "go" ">= 1.23"
Test-Runtime "python" "3.12"
Test-Runtime "node" ">= 20"

Write-Host "Local services:" -ForegroundColor Cyan
Test-Port 5432 "PostgreSQL"
Test-Port 6379 "Redis/Memurai"
Test-Port 1025 "Mailpit SMTP"
Test-Port 8025 "Mailpit UI"

if ($failed -gt 0) {
    Write-Host ("`n{0} required runtime(s) missing." -f $failed) -ForegroundColor Red
    exit 1
}
Write-Host "`nAll required runtimes present." -ForegroundColor Green
