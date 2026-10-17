# Assemble the portable Windows package from an already verified native build.
$ErrorActionPreference = 'Stop'
$workspaceDirectory = Split-Path -Parent $PSScriptRoot
$configText = [IO.File]::ReadAllText((Join-Path $workspaceDirectory 'build/config.yml'))
$versionMatch = [regex]::Match($configText, '(?m)^\s+version:\s*["'']?([^\s"'']+)')
if (-not $versionMatch.Success) { throw 'build/config.yml has no info.version.' }
$packageName = "yousage-$($versionMatch.Groups[1].Value)-windows-amd64"
$executablePath = Join-Path $workspaceDirectory 'bin/yousage.exe'
if (-not (Test-Path -LiteralPath $executablePath)) { throw 'Build bin/yousage.exe first.' }
$binaryBytes = [IO.File]::ReadAllBytes($executablePath)
$headerOffset = [BitConverter]::ToInt32($binaryBytes, 0x3c)
if ([BitConverter]::ToUInt16($binaryBytes, $headerOffset + 4) -ne 0x8664) { throw 'This package requires a Windows amd64 executable.' }
if ([BitConverter]::ToUInt16($binaryBytes, $headerOffset + 24 + 68) -ne 2) { throw 'The executable must use the Windows GUI subsystem.' }
$outputDirectory = Join-Path $workspaceDirectory 'artifacts/releases'
$taskDirectory = Join-Path $workspaceDirectory '.task'
New-Item -ItemType Directory -Force -Path $outputDirectory, $taskDirectory | Out-Null
$releaseDirectory = Join-Path $taskDirectory ('package-windows-' + [guid]::NewGuid().ToString('N'))
$metadataDirectory = Join-Path $releaseDirectory 'data/package'
New-Item -ItemType Directory -Path $metadataDirectory -Force | Out-Null
try {
    Copy-Item -LiteralPath $executablePath -Destination (Join-Path $releaseDirectory 'yousage.exe')
    foreach ($relativePath in @('README.md', 'THIRD_PARTY_NOTICES.md', 'build/dependencies.json')) {
        Copy-Item -LiteralPath (Join-Path $workspaceDirectory $relativePath) -Destination $metadataDirectory
    }
    $checksums = @('yousage.exe', 'data/package/README.md', 'data/package/THIRD_PARTY_NOTICES.md', 'data/package/dependencies.json') | ForEach-Object {
        $digest = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $releaseDirectory $_)).Hash.ToLowerInvariant()
        "$digest  $_"
    }
    [IO.File]::WriteAllText((Join-Path $metadataDirectory 'SHA256SUMS.txt'), ($checksums -join "`n") + "`n", [Text.Encoding]::ASCII)
    $archivePath = Join-Path $outputDirectory "$packageName.zip"
    $archiveFiles = @('yousage.exe', 'data') | ForEach-Object { Join-Path $releaseDirectory $_ }
    Compress-Archive -LiteralPath $archiveFiles -DestinationPath $archivePath -Force
    Get-FileHash -Algorithm SHA256 -LiteralPath $executablePath, $archivePath | Format-Table
    $archiveDigest = (Get-FileHash -Algorithm SHA256 -LiteralPath $archivePath).Hash.ToLowerInvariant()
    [IO.File]::WriteAllText((Join-Path $outputDirectory 'SHA256SUMS.txt'), "$archiveDigest  $packageName.zip`n", [Text.Encoding]::ASCII)
} finally {
    $resolvedRelease = (Resolve-Path -LiteralPath $releaseDirectory).Path
    $resolvedTask = (Resolve-Path -LiteralPath $taskDirectory).Path
    if ((Split-Path -Parent $resolvedRelease) -ne $resolvedTask -or (Split-Path -Leaf $resolvedRelease) -notlike 'package-windows-*') {
        throw "Refusing to remove unexpected staging directory: $resolvedRelease"
    }
    Remove-Item -LiteralPath $resolvedRelease -Recurse -Force
}
