# Loads KEY=VALUE pairs from the repository root .env into this process.
# dot-source it: . scripts/load-env.ps1
# The Go API has no dotenv loader, so this is how .env reaches it.
$envFile = Join-Path (Split-Path -Parent $PSScriptRoot) ".env"
if (-not (Test-Path $envFile)) { return }

Get-Content $envFile | ForEach-Object {
    $line = $_.Trim()
    if ($line -eq "" -or $line.StartsWith("#")) { return }
    $idx = $line.IndexOf("=")
    if ($idx -lt 1) { return }
    $key = $line.Substring(0, $idx).Trim()
    $val = $line.Substring($idx + 1).Trim()
    if ($val.Length -ge 2 -and $val[0] -eq $val[$val.Length - 1] -and ($val[0] -eq '"' -or $val[0] -eq "'")) {
        $val = $val.Substring(1, $val.Length - 2)
    }
    [Environment]::SetEnvironmentVariable($key, $val, "Process")
}
