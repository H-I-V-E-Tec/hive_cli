# Installs the signed HIVE launcher release in the current user's .hive/bin.
$ErrorActionPreference = 'Stop'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
$repo = if ($env:HIVE_CLI_REPOSITORY) { $env:HIVE_CLI_REPOSITORY } else { 'H-I-V-E-Tec/hive_cli' }
if ($repo -notmatch '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$') { throw 'Invalid repository name.' }
$version = $env:HIVE_CLI_VERSION
if (-not $version) {
    $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases/latest" -Headers @{ 'User-Agent' = 'hive-cli-installer' }
    $version = $release.tag_name
}
if ($version -notmatch '^v[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$') { throw 'Invalid release version.' }

$architecture = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()
if ($architecture -notin @('x64', 'arm64')) { throw "Unsupported Windows architecture: $architecture" }
$assetArch = if ($architecture -eq 'x64') { 'amd64' } else { 'arm64' }
$asset = "hive-cli-$version-windows-$assetArch.zip"
$baseUrl = "https://github.com/$repo/releases/download/$version"
$tempDir = Join-Path ([System.IO.Path]::GetTempPath()) ("hive-install-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tempDir | Out-Null
try {
    $archive = Join-Path $tempDir $asset
    $sums = Join-Path $tempDir 'SHA256SUMS'
    Invoke-WebRequest -UseBasicParsing -Uri "$baseUrl/$asset" -OutFile $archive
    Invoke-WebRequest -UseBasicParsing -Uri "$baseUrl/SHA256SUMS" -OutFile $sums
    $entry = Get-Content $sums | Where-Object { $_ -match ('^[0-9a-fA-F]{64}\s+\*?' + [regex]::Escape($asset) + '$') } | Select-Object -First 1
    if (-not $entry) { throw 'Release checksum is missing.' }
    $expected = ($entry -split '\s+')[0].ToLowerInvariant()
    $actual = (Get-FileHash -Path $archive -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actual -ne $expected) { throw 'Release checksum does not match.' }

    if (Get-Command cosign -ErrorAction SilentlyContinue) {
        $bundle = Join-Path $tempDir 'SHA256SUMS.sigstore-bundle.json'
        Invoke-WebRequest -UseBasicParsing -Uri "$baseUrl/SHA256SUMS.sigstore-bundle.json" -OutFile $bundle
        & cosign verify-blob --new-bundle-format --bundle $bundle --certificate-identity "https://github.com/$repo/.github/workflows/release.yml@refs/tags/$version" --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' $sums | Out-Null
        if ($LASTEXITCODE -ne 0) { throw 'Release signature is invalid.' }
    } else {
        Write-Warning 'cosign was not found; the launcher archive was verified against the downloaded checksum only.'
    }

    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $zip = [System.IO.Compression.ZipFile]::OpenRead($archive)
    try {
        if ($zip.Entries.Count -ne 1 -or $zip.Entries[0].FullName -ne 'hive.exe') {
            throw 'The release archive has unexpected entries.'
        }
    } finally {
        $zip.Dispose()
    }
    $extractDir = Join-Path $tempDir 'extract'
    Expand-Archive -Path $archive -DestinationPath $extractDir
    $source = Join-Path $extractDir 'hive.exe'
    if (-not (Test-Path -LiteralPath $source -PathType Leaf)) { throw 'The release archive does not contain hive.exe.' }
    $identity = & $source version --json | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0 -or $identity.version -ne $version) { throw 'The downloaded launcher has the wrong version or cannot run on this computer.' }

    $hiveDir = if ($env:HIVE_HOME) { $env:HIVE_HOME } else { Join-Path $env:USERPROFILE '.hive' }
    $binDir = Join-Path $hiveDir 'bin'
    New-Item -ItemType Directory -Path $binDir -Force | Out-Null
    $staged = Join-Path $binDir ("hive-" + [guid]::NewGuid().ToString('N') + '.exe')
    try {
        Copy-Item -LiteralPath $source -Destination $staged
        Move-Item -LiteralPath $staged -Destination (Join-Path $binDir 'hive.exe') -Force
    } finally {
        Remove-Item -LiteralPath $staged -Force -ErrorAction SilentlyContinue
    }
    $currentPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $pathParts = @($currentPath -split ';' | Where-Object { $_ })
    if ($pathParts -notcontains $binDir) {
        [Environment]::SetEnvironmentVariable('Path', (($pathParts + $binDir) -join ';'), 'User')
    }
    if (($env:Path -split ';') -notcontains $binDir) { $env:Path += ";$binDir" }
    Write-Host "hive $version installed in $binDir. Open a new terminal and run: hive version"
} finally {
    Remove-Item -LiteralPath $tempDir -Recurse -Force -ErrorAction SilentlyContinue
}
