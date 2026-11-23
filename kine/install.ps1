# KineRouter installer for Windows.
#
#   irm https://raw.githubusercontent.com/keyzakyy-dev/kinerouter/main/kine/install.ps1 | iex
#
# Downloads the release binary for this machine, verifies it against the
# release's checksums.txt, installs it under %LOCALAPPDATA%\KineRouter\bin and
# adds that directory to the user PATH. The script itself is not verified when
# fetched this way — pin a tag ref or read it first if you need that.
#
# Env:
#   KINEROUTER_VERSION=X.Y.Z    install a specific release (default: newest kine-v*)
#   KINEROUTER_INSTALL_DIR=DIR  install here instead of %LOCALAPPDATA%\KineRouter\bin
#   KINEROUTER_REPO=owner/name  override the release repository

# Saved so the `irm | iex` form leaves the caller's session settings alone.
$prevErrAction = $ErrorActionPreference
$prevProgress = $ProgressPreference
$ErrorActionPreference = 'Stop'
# Windows PowerShell 5.1 defaults to TLS 1.0 against api.github.com.
try { [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12 } catch {}
$ProgressPreference = 'SilentlyContinue'

$Repo = if ($env:KINEROUTER_REPO) { $env:KINEROUTER_REPO } else { 'keyzakyy-dev/kinerouter' }

# throw, not exit: when this script arrives through `irm | iex` it runs inside
# the user's interactive session, and `exit` would close their terminal.
function Stop-Install([string] $Message) {
    throw "kinerouter: $Message"
}
function Write-Say([string] $Message) {
    Write-Host "kinerouter: $Message" -ForegroundColor Cyan
}

# ---------------------------------------------------------------- platform

$arch = switch ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture) {
    'X64'   { 'amd64' }
    'Arm64' { 'arm64' }
    'X86'   { 'x86' }
    default { $_.ToString().ToLowerInvariant() }
}
if ($arch -ne 'amd64' -and $arch -ne 'arm64') {
    Stop-Install "unsupported CPU architecture: $arch (release assets cover amd64 and arm64)"
}

# ---------------------------------------------------------------- version

$version = $env:KINEROUTER_VERSION
if ($version) {
    $version = $version -replace '^kine-v', '' -replace '^v', ''
} else {
    $headers = @{ 'User-Agent' = 'kinerouter-installer'; 'Accept' = 'application/vnd.github+json' }
    $releases = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases?per_page=50" -Headers $headers -UseBasicParsing
    $tag = $releases | Where-Object { $_.tag_name -like 'kine-v*' } | Select-Object -First 1 -ExpandProperty tag_name
    if (-not $tag) {
        Stop-Install "could not find a kine-v* release in $Repo; pass KINEROUTER_VERSION=X.Y.Z"
    }
    $version = $tag -replace '^kine-v', ''
}
if ($version -notmatch '^\d+\.\d+\.\d+') {
    Stop-Install "'$version' is not a version number"
}

$tag   = "kine-v$version"
$asset = "kinerouter_${version}_windows_${arch}.exe"
$base  = if ($env:KINEROUTER_BASE_URL) { $env:KINEROUTER_BASE_URL } else { "https://github.com/$Repo/releases/download/$tag" }

# ---------------------------------------------------------------- download

$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("kinerouter-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp -Force | Out-Null
try {
    Write-Say "downloading $asset ($tag)"
    try {
        Invoke-WebRequest -Uri "$base/checksums.txt" -OutFile (Join-Path $tmp 'checksums.txt') -UseBasicParsing
    } catch {
        Stop-Install "no checksums.txt for $tag ($($_.Exception.Message))"
    }
    $assetPath = Join-Path $tmp $asset
    try {
        Invoke-WebRequest -Uri "$base/$asset" -OutFile $assetPath -UseBasicParsing
    } catch {
        Stop-Install "download failed: $base/$asset ($($_.Exception.Message))"
    }

    $want = $null
    foreach ($line in Get-Content (Join-Path $tmp 'checksums.txt')) {
        $parts = $line.Trim() -split '\s+'
        if ($parts.Count -ge 2 -and ($parts[1] -eq $asset -or $parts[1] -eq "./$asset")) { $want = $parts[0]; break }
    }
    if (-not $want) { Stop-Install "checksums.txt for $tag has no entry for $asset" }

    $got = (Get-FileHash -Path $assetPath -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($got -ne $want.ToLowerInvariant()) {
        Stop-Install "sha256 mismatch for $asset (got $got, want $want)"
    }

    # ------------------------------------------------------------ install

    $dir = if ($env:KINEROUTER_INSTALL_DIR) { $env:KINEROUTER_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'KineRouter\bin' }
    New-Item -ItemType Directory -Path $dir -Force | Out-Null

    # One owner per command name: an npm-installed kinerouter earlier on PATH
    # would shadow this one and update on its own schedule.
    $existing = Get-Command kinerouter -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($existing -and $existing.Source -ne (Join-Path $dir 'kinerouter.exe')) {
        Write-Say "warning: $($existing.Source) also provides kinerouter and may win by PATH order"
        Write-Say "         uninstall it (npm rm -g kinerouter) so updates cannot drift apart"
    }

    $dest = Join-Path $dir 'kinerouter.exe'
    Move-Item -Path $assetPath -Destination $dest -Force

    Write-Say "installed $dest ($tag, sha256 verified)"

    # The tray is a second binary that `kinerouter tray` execs from the same
    # directory. Older releases predate it, so a missing asset is not fatal.
    $trayAsset = "kinerouter-tray_${version}_windows_${arch}.exe"
    $trayWant = $null
    foreach ($line in Get-Content (Join-Path $tmp 'checksums.txt')) {
        $parts = $line.Trim() -split '\s+'
        if ($parts.Count -ge 2 -and ($parts[1] -eq $trayAsset -or $parts[1] -eq "./$trayAsset")) { $trayWant = $parts[0]; break }
    }
    if ($trayWant) {
        try {
            $trayPath = Join-Path $tmp $trayAsset
            Invoke-WebRequest -Uri "$base/$trayAsset" -OutFile $trayPath -UseBasicParsing
            $trayGot = (Get-FileHash -Path $trayPath -Algorithm SHA256).Hash.ToLowerInvariant()
            if ($trayGot -ne $trayWant.ToLowerInvariant()) { throw "sha256 mismatch" }
            Move-Item -Path $trayPath -Destination (Join-Path $dir 'kinerouter-tray.exe') -Force
            Write-Say "installed tray ($trayAsset, sha256 verified)"
        } catch {
            Write-Say "tray not installed: $($_.Exception.Message)"
        }
    } else {
        Write-Say "note: $tag has no Windows tray binary"
    }

    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $parts = @()
    if ($userPath) { $parts = $userPath -split ';' | Where-Object { $_ -ne '' } }
    if ($parts -notcontains $dir) {
        $newPath = if ($parts.Count -gt 0) { ($parts + $dir) -join ';' } else { $dir }
        [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
        Write-Say "added $dir to your user PATH (open a new terminal to pick it up)"
    }
    $env:Path = "$dir;$env:Path"

    Write-Say "start it with: `$env:INITIAL_PASSWORD='choose-something'; kinerouter"
    Write-Say "open http://localhost:20128/"
}
finally {
    Remove-Item -Path $tmp -Recurse -Force -ErrorAction SilentlyContinue
    $ErrorActionPreference = $prevErrAction
    $ProgressPreference = $prevProgress
}
