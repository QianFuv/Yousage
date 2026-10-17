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
$releaseDirectory = Join-Path $workspaceDirectory "bin/$packageName"
New-Item -ItemType Directory -Force -Path $releaseDirectory | Out-Null
$packageFiles = @('bin/yousage.exe', 'README.md', 'THIRD_PARTY_NOTICES.md', 'build/dependencies.json')
foreach ($relativePath in $packageFiles) {
    Copy-Item -LiteralPath (Join-Path $workspaceDirectory $relativePath) -Destination $releaseDirectory -Force
}
$checksums = @('yousage.exe', 'README.md', 'THIRD_PARTY_NOTICES.md', 'dependencies.json') | ForEach-Object {
    $digest = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $releaseDirectory $_)).Hash.ToLowerInvariant()
    "$digest  $_"
}
[IO.File]::WriteAllText((Join-Path $releaseDirectory 'SHA256SUMS.txt'), ($checksums -join "`n") + "`n", [Text.Encoding]::ASCII)
$archivePath = Join-Path $workspaceDirectory "bin/$packageName.zip"
$archiveFiles = @('yousage.exe', 'README.md', 'THIRD_PARTY_NOTICES.md', 'dependencies.json', 'SHA256SUMS.txt') | ForEach-Object { Join-Path $releaseDirectory $_ }
Compress-Archive -LiteralPath $archiveFiles -DestinationPath $archivePath -Force
Get-FileHash -Algorithm SHA256 -LiteralPath $executablePath, $archivePath | Format-Table
$archiveDigest = (Get-FileHash -Algorithm SHA256 -LiteralPath $archivePath).Hash.ToLowerInvariant()
[IO.File]::WriteAllText((Join-Path $workspaceDirectory 'bin/SHA256SUMS.txt'), "$archiveDigest  $packageName.zip`n", [Text.Encoding]::ASCII)
