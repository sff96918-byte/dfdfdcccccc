# Capture tool one-click setup.
# Goal: after running this once, run.bat must work. Multiple fallbacks:
#   Node.js : winget -> official MSI download -> manual instructions
#   Browser : existing Chrome/Edge -> winget Google Chrome -> manual link
# Finishes with tool\doctor.cjs, which actually launches the browser headless
# and connects via CDP as a smoke test. DOCTOR PASS means run.bat will work.
$ErrorActionPreference = "Continue"
Set-Location -Path $PSScriptRoot

function Refresh-Path {
    $machine = [Environment]::GetEnvironmentVariable("Path", "Machine")
    $user = [Environment]::GetEnvironmentVariable("Path", "User")
    $env:Path = "$machine;$user"
}

function Test-Node {
    Refresh-Path
    $cmd = Get-Command node -ErrorAction SilentlyContinue
    if ($cmd) {
        $v = (& node -v) -replace "v", ""
        $major = [int]($v.Split(".")[0])
        return $major -ge 20
    }
    return $false
}

function Install-NodeWinget {
    $winget = Get-Command winget -ErrorAction SilentlyContinue
    if (-not $winget) { return $false }
    Write-Host "[setup] Node.js pachchina - winget diye install korchi..."
    & winget install -e --id OpenJS.NodeJS.LTS --silent --accept-package-agreements --accept-source-agreements | Out-Host
    return (Test-Node)
}

function Install-NodeMsi {
    Write-Host "[setup] winget diye holona - official Node.js installer download korchi..."
    $msi = Join-Path $env:TEMP "node-v22-lts-x64.msi"
    try {
        Invoke-WebRequest -Uri "https://nodejs.org/dist/v22.14.0/node-v22.14.0-x64.msi" -OutFile $msi -UseBasicParsing
    } catch {
        Write-Host "[setup] Download failed: $($_.Exception.Message)"
        return $false
    }
    Write-Host "[setup] Installer chalaachhi (Windows permission chaile Yes din)..."
    try {
        Start-Process msiexec.exe -ArgumentList "/i `"$msi`" /passive" -Verb RunAs -Wait
    } catch {
        Write-Host "[setup] Installer launch failed: $($_.Exception.Message)"
        return $false
    }
    return (Test-Node)
}

function Find-BrowserExe {
    $paths = @(
        "$env:ProgramFiles\Google\Chrome\Application\chrome.exe",
        "${env:ProgramFiles(x86)}\Google\Chrome\Application\chrome.exe",
        "$env:LOCALAPPDATA\Google\Chrome\Application\chrome.exe",
        "$env:ProgramFiles\Microsoft\Edge\Application\msedge.exe",
        "${env:ProgramFiles(x86)}\Microsoft\Edge\Application\msedge.exe",
        "$env:ProgramFiles\Chromium\Application\chrome.exe",
        "$env:LOCALAPPDATA\BraveSoftware\Brave-Browser\Application\brave.exe"
    )
    foreach ($p in $paths) { if (Test-Path $p) { return $p } }
    return $null
}

Write-Host "============================================================"
Write-Host " Capture tool - one-click setup (auto-install + test)"
Write-Host "============================================================"

# --- Node.js ---
if (Test-Node) {
    Write-Host "[OK] Node.js ache: $(& node -v)"
} elseif ((Install-NodeWinget) -or (Install-NodeMsi)) {
    Write-Host "[OK] Node.js install hoye geche: $(& node -v)"
} else {
    Write-Host "[FAIL] Node.js auto-install kora jayni."
    Write-Host "       Nijer haate install korun: https://nodejs.org (22 LTS)"
    Write-Host "       Tarpor abar install.bat chalaben."
    exit 1
}

# --- Browser ---
$browser = Find-BrowserExe
if ($browser) {
    Write-Host "[OK] Browser ache: $browser"
} else {
    Write-Host "[setup] Chrome/Edge pachchina - winget diye Google Chrome install korchi..."
    $winget = Get-Command winget -ErrorAction SilentlyContinue
    if ($winget) {
        & winget install -e --id Google.Chrome --silent --accept-package-agreements --accept-source-agreements | Out-Host
        $browser = Find-BrowserExe
    }
    if ($browser) {
        Write-Host "[OK] Browser install hoye geche: $browser"
    } else {
        Write-Host "[FAIL] Browser paoya jayni. Google Chrome install kore abar install.bat chalaben:"
        Write-Host "       https://www.google.com/chrome/"
        exit 1
    }
}

# --- Folders ---
New-Item -ItemType Directory -Force -Path "capture-sessions" | Out-Null
New-Item -ItemType Directory -Force -Path "profiles" | Out-Null
Write-Host "[OK] Folders ready: capture-sessions\ and profiles\"

# --- Smoke test: prove run.bat will work on this PC ---
Write-Host ""
Write-Host "[test] Asol browser chaliye CDP connect kore dekhchi (doctor)..."
& node "tool\doctor.cjs"
if ($LASTEXITCODE -eq 0) {
    Write-Host ""
    Write-Host "============================================================"
    Write-Host " READY! Ekhon run.bat double-click korlei capture suru hobe."
    Write-Host "============================================================"
    exit 0
} else {
    Write-Host ""
    Write-Host "[FAIL] Doctor test pass hoyni - uporer [FAIL] line gulo dekhun."
    Write-Host "       Screenshot niye pathalei fix kore deya hobe."
    exit 1
}
