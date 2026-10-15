param(
  [ValidateSet('x64')]
  [string]$Architecture = 'x64',
  [switch]$Clean
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$ProjectRoot = Split-Path -Parent $PSScriptRoot
$BuildDir = Join-Path $ProjectRoot 'build'
$DownloadDir = Join-Path $BuildDir 'downloads'
$TempDir = Join-Path $BuildDir 'tmp'
$BundleDir = Join-Path $ProjectRoot "dist\windows-$Architecture"
$WebView2ExtractDir = Join-Path $BundleDir 'runtime\webview2'
$ChromiumExtractDir = Join-Path $BundleDir 'runtime\chromium'

function Ensure-Directory {
  param([string]$Path)
  if (-not (Test-Path -LiteralPath $Path)) {
    New-Item -ItemType Directory -Path $Path | Out-Null
  }
}

function Reset-Directory {
  param([string]$Path)
  if (Test-Path -LiteralPath $Path) {
    Remove-Item -LiteralPath $Path -Recurse -Force
  }
  New-Item -ItemType Directory -Path $Path | Out-Null
}

function Download-File {
  param(
    [string]$Url,
    [string]$Destination
  )

  if (Test-Path -LiteralPath $Destination) {
    return
  }

  Ensure-Directory (Split-Path -Parent $Destination)
  Write-Host "Downloading $Url"
  Invoke-WebRequest -UseBasicParsing -Uri $Url -OutFile $Destination
}

function Get-WebView2FixedRuntimeUrl {
  $page = Invoke-WebRequest -UseBasicParsing 'https://developer.microsoft.com/en-us/microsoft-edge/webview2?form=MA13LH'
  $pattern = 'https://msedge\.sf\.dl\.delivery\.mp\.microsoft\.com/filestreamingservice/files/[^"]+/Microsoft\.WebView2\.FixedVersionRuntime\.[^"]+\.x64\.cab'
  $match = [regex]::Matches($page.Content, $pattern) | Select-Object -First 1
  if (-not $match) {
    throw 'Could not find the official x64 WebView2 Fixed Runtime download URL.'
  }
  return $match.Value
}

function Expand-CabArchive {
  param(
    [string]$CabPath,
    [string]$Destination
  )

  Reset-Directory $Destination
  & "$env:SystemRoot\System32\expand.exe" "-F:*" $CabPath $Destination | Out-Null
  if ($LASTEXITCODE -ne 0) {
    throw "Failed to expand CAB archive $CabPath"
  }
}

function Install-WebView2Runtime {
  $runtimeUrl = Get-WebView2FixedRuntimeUrl
  $runtimeFile = Split-Path -Leaf $runtimeUrl
  $cabPath = Join-Path $DownloadDir $runtimeFile
  $extractRoot = Join-Path $TempDir 'webview2'

  Download-File -Url $runtimeUrl -Destination $cabPath
  Expand-CabArchive -CabPath $cabPath -Destination $extractRoot

  Reset-Directory $WebView2ExtractDir
  Copy-Item -Path (Join-Path $extractRoot '*') -Destination $WebView2ExtractDir -Recurse -Force

  $browserExe = Get-ChildItem -Path $WebView2ExtractDir -Recurse -Filter 'msedgewebview2.exe' | Select-Object -First 1
  if (-not $browserExe) {
    throw 'Bundled WebView2 runtime is missing msedgewebview2.exe after extraction.'
  }

  return @{
    Url = $runtimeUrl
    Version = [IO.Path]::GetFileNameWithoutExtension($runtimeFile) -replace '^Microsoft\.WebView2\.FixedVersionRuntime\.', ''
  }
}

function Get-ChromeForTestingStableInfo {
  $json = Invoke-RestMethod -Uri 'https://googlechromelabs.github.io/chrome-for-testing/last-known-good-versions-with-downloads.json'
  $download = $json.channels.Stable.downloads.chrome | Where-Object { $_.platform -eq 'win64' } | Select-Object -First 1
  if (-not $download) {
    throw 'Could not find the official win64 Chrome for Testing download URL.'
  }

  return @{
    Url = $download.url
    Version = $json.channels.Stable.version
  }
}

function Install-Chromium {
  $chrome = Get-ChromeForTestingStableInfo
  $zipPath = Join-Path $DownloadDir ([IO.Path]::GetFileName($chrome.Url))
  $extractRoot = Join-Path $TempDir 'chromium'

  Download-File -Url $chrome.Url -Destination $zipPath

  Reset-Directory $extractRoot
  Expand-Archive -LiteralPath $zipPath -DestinationPath $extractRoot -Force

  Reset-Directory $ChromiumExtractDir
  Copy-Item -Path (Join-Path $extractRoot '*') -Destination $ChromiumExtractDir -Recurse -Force

  $browserExe = Get-ChildItem -Path $ChromiumExtractDir -Recurse -Filter 'chrome.exe' | Select-Object -First 1
  if (-not $browserExe) {
    throw 'Bundled Chromium runtime is missing chrome.exe after extraction.'
  }

  return $chrome
}

function Build-Binaries {
  Push-Location $ProjectRoot
  try {
    & wails build -clean -webview2 error
    if ($LASTEXITCODE -ne 0) {
      throw 'wails build failed.'
    }

    $cliOut = Join-Path $BundleDir 'chitchatbot.exe'
    & go build -o $cliOut .\cmd\chitchatbot
    if ($LASTEXITCODE -ne 0) {
      throw 'go build for chitchatbot.exe failed.'
    }
  } finally {
    Pop-Location
  }

  $uiExe = Join-Path $ProjectRoot 'build\bin\chitchatui.exe'
  if (-not (Test-Path -LiteralPath $uiExe)) {
    throw 'Expected UI binary build\bin\chitchatui.exe was not created.'
  }

  Copy-Item -LiteralPath $uiExe -Destination (Join-Path $BundleDir 'chitchatui.exe') -Force
}

function Write-BundleReadme {
  param(
    [string]$WebView2Version,
    [string]$WebView2Url,
    [string]$ChromiumVersion,
    [string]$ChromiumUrl
  )

  $content = @"
ChitChat Bot Windows Bundle

Files in this folder:
- chitchatui.exe: desktop UI
- chitchatbot.exe: CLI bot
- runtime\webview2\: bundled Microsoft Edge WebView2 Fixed Runtime
- runtime\chromium\: bundled Chrome for Testing browser

Notes:
- End users do not need to install WebView2 separately for this bundle.
- End users do not need to install Chrome or Edge separately for browser automation in this bundle.
- Proxies stay inside the app only. This bundle no longer changes whole-PC Windows proxy settings.

Bundled dependency versions:
- WebView2 Fixed Runtime: $WebView2Version
- WebView2 source: $WebView2Url
- Chromium: $ChromiumVersion
- Chromium source: $ChromiumUrl
"@

  Set-Content -LiteralPath (Join-Path $BundleDir 'README-BUNDLE.txt') -Value $content
}

Ensure-Directory $DownloadDir
Ensure-Directory $TempDir

if ($Clean) {
  if (Test-Path -LiteralPath $BundleDir) {
    Remove-Item -LiteralPath $BundleDir -Recurse -Force
  }
}

Reset-Directory $BundleDir
Build-Binaries

$webview2 = Install-WebView2Runtime
$chromium = Install-Chromium
Write-BundleReadme -WebView2Version $webview2.Version -WebView2Url $webview2.Url -ChromiumVersion $chromium.Version -ChromiumUrl $chromium.Url

Write-Host ''
Write-Host "Bundle ready: $BundleDir"
Write-Host "WebView2 runtime: $($webview2.Version)"
Write-Host "Chromium: $($chromium.Version)"
