param([switch]$Restart,[switch]$Check)
$ErrorActionPreference = 'Stop'
$port = 19222
try {
    $main = @(Get-CimInstance Win32_Process | Where-Object { $_.Name -eq 'ChatGPT.exe' -and $_.CommandLine -notmatch '--type=' -and $_.ExecutablePath -match '[\\/]OpenAI\.[^\\/]+[\\/]app[\\/]ChatGPT\.exe$' })
    $executable = if ($main.Count) { $main[0].ExecutablePath } else {
        $packages = @(Get-AppxPackage | Where-Object Name -like 'OpenAI.*' | Sort-Object {[Version]$_.Version} -Descending)
        foreach ($package in $packages) {
            $candidate = Join-Path $package.InstallLocation 'app\ChatGPT.exe'
            if (Test-Path $candidate) { $candidate; break }
        }
    }
    if (!$executable) { throw 'ChatGPT Desktop installation was not found.' }
    $listeners = @(Get-NetTCPConnection -State Listen -LocalPort $port -ErrorAction SilentlyContinue)
    $ready = $false
    if ($listeners.Count) {
        if (@($listeners | Where-Object LocalAddress -notin @('127.0.0.1','::1')).Count) { throw 'The capture port must listen only on localhost.' }
        $owner = Get-CimInstance Win32_Process | Where-Object ProcessId -eq $listeners[0].OwningProcess
        if ($owner.ExecutablePath -ne $executable) { throw 'The capture port is occupied by another application.' }
        $version = Invoke-RestMethod -Uri "http://127.0.0.1:$port/json/version" -TimeoutSec 3
        $ready = !!$version.webSocketDebuggerUrl
    }
    if ($Check) { @{installed=$true;running=($main.Count -gt 0);ready=$ready;port=$port} | ConvertTo-Json -Compress;exit 0 }
    if ($ready) { Write-Host 'ChatGPT Desktop enhanced sync is already available.'; exit 0 }
    if ($main.Count) {
        if (!$Restart) { throw 'Exit ChatGPT Desktop from its tray icon first, or run this script with -Restart.' }
        Write-Host 'Restarting ChatGPT Desktop for enhanced sync...'
        foreach ($process in $main) {
            $running = Get-Process -Id $process.ProcessId -ErrorAction SilentlyContinue
            if ($running) { [void]$running.CloseMainWindow() }
        }
        Start-Sleep -Seconds 2
        foreach ($process in $main) { Stop-Process -Id $process.ProcessId -ErrorAction SilentlyContinue }
        Start-Sleep -Seconds 2
    }
    Start-Process -FilePath $executable -WorkingDirectory (Split-Path -Parent $executable) -ArgumentList @('--remote-debugging-address=127.0.0.1',"--remote-debugging-port=$port")
    $deadline = [DateTime]::UtcNow.AddSeconds(30)
    do {
        try { $version = Invoke-RestMethod -Uri "http://127.0.0.1:$port/json/version" -TimeoutSec 1; if ($version.webSocketDebuggerUrl) { $ready=$true;break } } catch {}
        Start-Sleep -Milliseconds 500
    } while ([DateTime]::UtcNow -lt $deadline)
    if (!$ready) { throw 'Desktop did not open the local capture interface. Regular GPT Vault cache sync is still available.' }
    $listeners = @(Get-NetTCPConnection -State Listen -LocalPort $port -ErrorAction SilentlyContinue)
    if (@($listeners | Where-Object LocalAddress -notin @('127.0.0.1','::1')).Count) {
        Get-CimInstance Win32_Process | Where-Object { $_.Name -eq 'ChatGPT.exe' -and $_.CommandLine -match "--remote-debugging-port=$port" -and $_.CommandLine -notmatch '--type=' } | ForEach-Object { Stop-Process -Id $_.ProcessId }
        Start-Process -FilePath $executable -WorkingDirectory (Split-Path -Parent $executable)
        throw 'Desktop was restored to regular mode because the interface was not bound only to localhost.'
    }
    Write-Host 'Ready. Enable Desktop enhanced sync in GPT Vault > Data and sources.'
    Write-Host 'GPT Vault never restarts Desktop during automatic sync.'
} catch { [Console]::Error.WriteLine($_.Exception.Message);exit 1 }
