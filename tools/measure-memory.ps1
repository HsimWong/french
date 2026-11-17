# measure-memory.ps1
# Samples the working set of the process listening on a port until it answers
# a health probe, then reports the peak. Used to prove the dictionary import
# no longer balloons the heap.
#
# The process is started separately (the caller owns that); this script only
# observes. Start-Process is deliberately avoided here: it copies the current
# environment into a case-insensitive dictionary and throws on a duplicated
# http_proxy/HTTP_PROXY pair.
#
#   .\tools\measure-memory.ps1 -Port 8099 -TimeoutSec 900
param(
    [int]$Port = 8099,
    [int]$TimeoutSec = 900,
    [int]$IntervalMs = 250,
    [string]$HealthPath = "/api/health",
    [string]$ProcessName = "",
    [string]$LaunchExe = "",
    [string]$LaunchWorkDir = ""
)

$ErrorActionPreference = 'Continue'

function Get-ListenerPid([int]$p) {
    try {
        $c = Get-NetTCPConnection -State Listen -LocalPort $p -ErrorAction Stop |
             Select-Object -First 1
        return $c.OwningProcess
    } catch { return 0 }
}

$pid0 = 0

if ($LaunchExe) {
    # Launch and sample in one pass so the whole start-up ramp is captured.
    # System.Diagnostics.Process.Start inherits the parent environment
    # without materialising it, so a duplicated http_proxy/HTTP_PROXY pair
    # (which breaks Start-Process) is not a problem here.
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = (Resolve-Path $LaunchExe).Path
    if ($LaunchWorkDir) { $psi.WorkingDirectory = (Resolve-Path $LaunchWorkDir).Path }
    $psi.UseShellExecute = $false
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    $proc = [System.Diagnostics.Process]::Start($psi)
    if (-not $proc) { Write-Host "failed to launch $LaunchExe"; exit 1 }
    $pid0 = $proc.Id
    Write-Host "launched pid=$pid0"
} elseif ($ProcessName) {
    # The process binds its port only after the database is ready, so wait for
    # the process itself and sample from the very first moment it appears.
    Write-Host "waiting for process $ProcessName ..."
    $sw = [System.Diagnostics.Stopwatch]::StartNew()
    while ($sw.Elapsed.TotalSeconds -lt 120) {
        $p = Get-Process -Name $ProcessName -ErrorAction SilentlyContinue |
             Sort-Object StartTime | Select-Object -Last 1
        if ($p) { $pid0 = $p.Id; break }
        Start-Sleep -Milliseconds 100
    }
} else {
    Write-Host "waiting for a listener on port $Port ..."
    $sw = [System.Diagnostics.Stopwatch]::StartNew()
    while ($sw.Elapsed.TotalSeconds -lt 60) {
        $pid0 = Get-ListenerPid $Port
        if ($pid0 -gt 0) { break }
        Start-Sleep -Milliseconds 300
    }
}
if ($pid0 -eq 0) { Write-Host "target process never appeared"; exit 1 }
Write-Host "sampling pid=$pid0"

$proc = Get-Process -Id $pid0 -ErrorAction SilentlyContinue
$peak = 0L
$peakAt = 0
$ready = $false
$sw = [System.Diagnostics.Stopwatch]::StartNew()

while ($sw.Elapsed.TotalSeconds -lt $TimeoutSec) {
    $proc = Get-Process -Id $pid0 -ErrorAction SilentlyContinue
    if (-not $proc) { Write-Host "process disappeared at t+$([int]$sw.Elapsed.TotalSeconds)s"; break }
    if ($proc.WorkingSet64 -gt $peak) {
        $peak = $proc.WorkingSet64
        $peakAt = [int]$sw.Elapsed.TotalSeconds
    }
    try {
        $r = Invoke-WebRequest -Uri "http://127.0.0.1:$Port$HealthPath" -TimeoutSec 3 -UseBasicParsing
        if ($r.StatusCode -eq 200) { $ready = $true; break }
    } catch { }
    Start-Sleep -Milliseconds $IntervalMs
}

$proc = Get-Process -Id $pid0 -ErrorAction SilentlyContinue
if ($proc -and $proc.WorkingSet64 -gt $peak) { $peak = $proc.WorkingSet64 }

Write-Host ""
Write-Host "ready             : $ready"
Write-Host "elapsed          : $([int]$sw.Elapsed.TotalSeconds) s"
Write-Host "PEAK working set  : $([math]::Round($peak/1MB,1)) MB   (at t+${peakAt}s)"
if ($proc) {
    Write-Host "final working set : $([math]::Round($proc.WorkingSet64/1MB,1)) MB"
    Write-Host "private bytes     : $([math]::Round($proc.PrivateMemorySize64/1MB,1)) MB"
    Write-Host "threads           : $($proc.Threads.Count)"
}
if (-not $ready) { exit 1 }
