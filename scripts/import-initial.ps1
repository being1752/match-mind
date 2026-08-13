param(
    [string]$ApiBaseUrl = "http://127.0.0.1:8080/api/v1",
    [string[]]$Files = @("buy.txt", "sell.txt")
)

$ErrorActionPreference = "Stop"
$projectRoot = Split-Path -Parent $PSScriptRoot

foreach ($file in $Files) {
    $path = Join-Path $projectRoot $file
    if (-not (Test-Path -LiteralPath $path)) {
        throw "Initial data file not found: $path"
    }

    $content = Get-Content -LiteralPath $path -Encoding UTF8 -Raw
    $payload = @{
        content = $content
        source_name = "初始数据导入"
        source_file = $file
    } | ConvertTo-Json -Compress

    # Windows PowerShell may otherwise send Chinese JSON using an incompatible encoding.
    $utf8Body = [System.Text.Encoding]::UTF8.GetBytes($payload)
    $result = Invoke-RestMethod `
        -Method Post `
        -Uri "$ApiBaseUrl/ingestion-batches" `
        -ContentType "application/json; charset=utf-8" `
        -Body $utf8Body

    Write-Output ([pscustomobject]@{
        file = $file
        batch_id = $result.batch_id
        status = $result.status
    })
}

