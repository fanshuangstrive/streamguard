# SSE realtime verification script.
# Sends a streaming request through the proxy and prints the arrival time of each chunk.
# Usage: .\scripts\test-sse.ps1 -ApiKey <key> [-Model <name>] [-Proxy http://127.0.0.1:8080]

param(
    [Parameter(Mandatory = $true)]
    [string]$ApiKey,

    [string]$Model = "DeepSeek-V4-Flash",

    [string]$Proxy = "http://127.0.0.1:8080",

    [string]$Path = "/model/v1/chat/completions",

    [string]$Prompt = "count 1 to 10"
)

$ErrorActionPreference = "Stop"

$body = @{
    model    = $Model
    messages = @(@{ role = "user"; content = $Prompt })
    stream   = $true
} | ConvertTo-Json -Compress -Depth 5

$url = "$Proxy$Path"
Write-Host "POST $url"
Write-Host "model=$Model stream=true"
Write-Host "----------------------------------------"

$sw = [System.Diagnostics.Stopwatch]::StartNew()
$chunks = 0
$firstChunkMs = -1

$psi = New-Object System.Diagnostics.ProcessStartInfo
$psi.FileName = "curl.exe"
$psi.Arguments = "-N -s -X POST `"$url`" -H `"Content-Type: application/json`" -H `"Authorization: Bearer $ApiKey`" -d `"$($body -replace '"','\"')`""
$psi.RedirectStandardOutput = $true
$psi.UseShellExecute = $false
$psi.StandardOutputEncoding = [System.Text.Encoding]::UTF8

$proc = [System.Diagnostics.Process]::Start($psi)
while (-not $proc.StandardOutput.EndOfStream) {
    $line = $proc.StandardOutput.ReadLine()
    if ($line -like 'data: *') {
        $ms = $sw.Elapsed.TotalMilliseconds
        if ($line.Contains('[DONE]')) {
            Write-Host ("[{0,7:N0} ms] DONE" -f $ms)
        }
        else {
            $chunks++
            if ($firstChunkMs -lt 0) { $firstChunkMs = $ms }
            Write-Host ("[{0,7:N0} ms] chunk #{1}" -f $ms, $chunks)
        }
    }
}
$proc.WaitForExit()

Write-Host "----------------------------------------"
Write-Host ("total chunks : {0}" -f $chunks)
Write-Host ("first chunk  : {0:N0} ms" -f $firstChunkMs)
Write-Host ("total time   : {0:N0} ms" -f $sw.Elapsed.TotalMilliseconds)
Write-Host ""
Write-Host "If chunks arrive at increasing timestamps, SSE is streamed in realtime (not buffered)."
