# Verify the portable archive layout and upgrade preservation with disposable data.
$ErrorActionPreference = 'Stop'
$fixtureRoot = Join-Path ([IO.Path]::GetTempPath()) ('yousage-package-test-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $fixtureRoot | Out-Null
try {
    foreach ($directory in @('bin/data/database', 'bin/data/webview', 'build/windows', '.task')) {
        New-Item -ItemType Directory -Path (Join-Path $fixtureRoot $directory) -Force | Out-Null
    }
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'package-windows.ps1') -Destination (Join-Path $fixtureRoot 'build/package-windows.ps1')
    [IO.File]::WriteAllText((Join-Path $fixtureRoot 'build/config.yml'), "info:`n  version: 0.1.1`n")
    foreach ($name in @('README.md', 'THIRD_PARTY_NOTICES.md', 'build/dependencies.json')) {
        [IO.File]::WriteAllText((Join-Path $fixtureRoot $name), $name)
    }
    $executable = [byte[]]::new(512)
    [BitConverter]::GetBytes([int]128).CopyTo($executable, 0x3c)
    [BitConverter]::GetBytes([uint16]0x8664).CopyTo($executable, 132)
    [BitConverter]::GetBytes([uint16]2).CopyTo($executable, 220)
    [IO.File]::WriteAllBytes((Join-Path $fixtureRoot 'bin/yousage.exe'), $executable)
    [IO.File]::WriteAllText((Join-Path $fixtureRoot 'bin/data/database/yousage.sqlite'), 'private saved database')
    [IO.File]::WriteAllText((Join-Path $fixtureRoot 'bin/data/webview/sentinel'), 'private browser state')
    $databasePath = Join-Path $fixtureRoot 'bin/data/database/yousage.sqlite'
    $browserPath = Join-Path $fixtureRoot 'bin/data/webview/sentinel'
    $databaseHash = (Get-FileHash -LiteralPath $databasePath).Hash
    $browserHash = (Get-FileHash -LiteralPath $browserPath).Hash
    & (Join-Path $fixtureRoot 'build/package-windows.ps1')
    $archivePath = Join-Path $fixtureRoot 'artifacts/releases/yousage_0.1.1_windows_amd64.zip'
    if (-not (Test-Path -LiteralPath $archivePath)) { throw 'Archive was not created outside bin.' }
    $checksum = [IO.File]::ReadAllText((Join-Path $fixtureRoot 'artifacts/releases/SHA256SUMS'))
    $archiveDigest = (Get-FileHash -Algorithm SHA256 -LiteralPath $archivePath).Hash.ToLowerInvariant()
    if ($checksum -ne "$archiveDigest  yousage_0.1.1_windows_amd64.zip`n") { throw 'Public checksum does not match the archive.' }
    $archive = [IO.Compression.ZipFile]::OpenRead($archivePath)
    try {
        $entries = @($archive.Entries | ForEach-Object { $_.FullName.Replace('\', '/') } | Where-Object { -not $_.EndsWith('/') })
        $expected = @('yousage.exe', 'data/package/README.md', 'data/package/THIRD_PARTY_NOTICES.md', 'data/package/dependencies.json', 'data/package/SHA256SUMS.txt')
        if (Compare-Object $expected $entries) { throw "Unexpected archive entries: $($entries -join ', ')" }
    } finally {
        $archive.Dispose()
    }
    foreach ($iteration in 1..2) {
        Expand-Archive -LiteralPath $archivePath -DestinationPath (Join-Path $fixtureRoot 'bin') -Force
    }
    if ((Get-FileHash -LiteralPath $databasePath).Hash -ne $databaseHash -or (Get-FileHash -LiteralPath $browserPath).Hash -ne $browserHash) {
        throw 'Extracting the upgrade changed runtime data.'
    }
    $rootNames = @(Get-ChildItem -LiteralPath (Join-Path $fixtureRoot 'bin') -Force | ForEach-Object { $_.Name })
    if (Compare-Object @('yousage.exe', 'data') $rootNames) { throw 'Portable root contains extra files.' }
    Write-Output 'Portable package layout, private-data exclusion and repeated upgrade preservation passed.'
} finally {
    $resolvedFixture = (Resolve-Path -LiteralPath $fixtureRoot).Path
    $temporaryRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\', '/')
    if ((Split-Path -Parent $resolvedFixture) -ne $temporaryRoot -or (Split-Path -Leaf $resolvedFixture) -notlike 'yousage-package-test-*') {
        throw "Refusing to remove unexpected fixture path: $resolvedFixture"
    }
    Remove-Item -LiteralPath $resolvedFixture -Recurse -Force
}
