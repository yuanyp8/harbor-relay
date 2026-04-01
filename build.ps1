[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidateSet("amd64", "arm64")]
    [string]$Arch,

    [string]$Output,

    [string]$Version
)

$ErrorActionPreference = "Stop"

$RootDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$InstallerStub = Join-Path $RootDir "install.sh"
$ReadmeFile = Join-Path $RootDir "README.md"

function Write-Info([string]$Message) {
    Write-Host "[INFO] $Message"
}

function Get-GitVersion {
    if ($Version) {
        return $Version
    }

    try {
        $value = git -C $RootDir describe --tags --always --dirty 2>$null
        if ($LASTEXITCODE -eq 0 -and $value) {
            return $value.Trim()
        }
    } catch {
    }

    return "dev"
}

function Get-GitCommit {
    try {
        $value = git -C $RootDir rev-parse HEAD 2>$null
        if ($LASTEXITCODE -eq 0 -and $value) {
            return $value.Trim()
        }
    } catch {
    }

    return "unknown"
}

function Get-Sha256([string]$Path) {
    return (Get-FileHash -Path $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

function Copy-DirectoryContent([string]$Source, [string]$Destination) {
    New-Item -ItemType Directory -Force -Path $Destination | Out-Null
    Get-ChildItem -Path $Source -File | ForEach-Object {
        Copy-Item -Force $_.FullName (Join-Path $Destination $_.Name)
    }
}

function Build-Binary([string]$PackagePath, [string]$TargetPath) {
    $env:GOOS = "linux"
    $env:GOARCH = $Arch
    try {
        & go build -o $TargetPath $PackagePath
        if ($LASTEXITCODE -ne 0) {
            throw "go build failed for $PackagePath"
        }
    } finally {
        Remove-Item Env:GOOS -ErrorAction SilentlyContinue
        Remove-Item Env:GOARCH -ErrorAction SilentlyContinue
    }
}

if (-not (Test-Path $InstallerStub)) {
    throw "Missing install.sh"
}
if (-not (Test-Path $ReadmeFile)) {
    throw "Missing README.md"
}

$ResolvedVersion = Get-GitVersion
$WorkDir = Join-Path $RootDir ".payload-build-$Arch"
$InstallerPath = if ($Output) {
    $Output
} else {
    Join-Path $RootDir "dist/linux-$Arch/harbor-relay-toolkit-linux-$Arch.run"
}

if (Test-Path $WorkDir) {
    Remove-Item -Recurse -Force $WorkDir
}

New-Item -ItemType Directory -Force -Path $WorkDir | Out-Null
New-Item -ItemType Directory -Force -Path (Split-Path -Parent $InstallerPath) | Out-Null

$PayloadDir = Join-Path $WorkDir "payload"
$PayloadConfigsDir = Join-Path $PayloadDir "configs"
$PayloadSystemdDir = Join-Path $PayloadDir "deploy/systemd"
New-Item -ItemType Directory -Force -Path $PayloadConfigsDir, $PayloadSystemdDir | Out-Null

Write-Info "Building Linux binaries for $Arch"
Build-Binary "./cmd/relay" (Join-Path $PayloadDir "harbor-relay")
Build-Binary "./cmd/agent" (Join-Path $PayloadDir "harbor-relay-agent")

Copy-Item -Force $ReadmeFile (Join-Path $PayloadDir "README.md")
Copy-DirectoryContent (Join-Path $RootDir "configs") $PayloadConfigsDir
Copy-DirectoryContent (Join-Path $RootDir "deploy/systemd") $PayloadSystemdDir

$ManifestFile = Join-Path $PayloadDir "manifest.txt"
$BuiltAt = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
$RelaySha = Get-Sha256 (Join-Path $PayloadDir "harbor-relay")
$AgentSha = Get-Sha256 (Join-Path $PayloadDir "harbor-relay-agent")
$GitCommit = Get-GitCommit
$ManifestContent = @"
name=harbor-relay
version=$ResolvedVersion
arch=$Arch
built_at=$BuiltAt
relay_sha256=$RelaySha
agent_sha256=$AgentSha
git_commit=$GitCommit
"@
[System.IO.File]::WriteAllText($ManifestFile, $ManifestContent, [System.Text.UTF8Encoding]::new($false))

$PayloadTar = Join-Path $WorkDir "payload.tar.gz"
& tar -czf $PayloadTar -C $PayloadDir .
if ($LASTEXITCODE -ne 0) {
    throw "tar packaging failed"
}

$StubText = Get-Content -Raw -Encoding UTF8 $InstallerStub
if ($StubText -notmatch "(?m)^__PAYLOAD_BELOW__$") {
    throw "install.sh is missing the __PAYLOAD_BELOW__ marker"
}
$StubBytes = [System.Text.UTF8Encoding]::new($false).GetBytes(($StubText -replace "`r`n", "`n"))
$TarBytes = [System.IO.File]::ReadAllBytes($PayloadTar)

$OutStream = [System.IO.File]::Open($InstallerPath, [System.IO.FileMode]::Create, [System.IO.FileAccess]::Write)
try {
    $OutStream.Write($StubBytes, 0, $StubBytes.Length)
    $OutStream.Write($TarBytes, 0, $TarBytes.Length)
} finally {
    $OutStream.Dispose()
}

$InstallerSha = Get-Sha256 $InstallerPath
Set-Content -Path "${InstallerPath}.sha256" -Value $InstallerSha -Encoding ascii -NoNewline

Write-Info "Installer created: $InstallerPath"
Write-Info "Installer sha256: $InstallerSha"
