# Sync deploy/migrations/ ke deploy/helm/parkir-pintar/migrations/
# Cross-platform alternative untuk Makefile target `helm-sync-migrations`
# (yang pakai rm -rf — gak jalan di Windows PowerShell native).

$ErrorActionPreference = "Stop"
$repoRoot   = (Get-Item $PSScriptRoot).Parent.FullName
$source     = Join-Path $repoRoot "deploy\migrations"
$target     = Join-Path $repoRoot "deploy\helm\parkir-pintar\migrations"

if (-not (Test-Path $source)) {
    Write-Host "[FAIL] Source folder tidak ada: $source" -ForegroundColor Red
    exit 1
}

# Hapus target (kalau ada)
if (Test-Path $target) {
    Remove-Item -Path $target -Recurse -Force
}

# Buat target + copy semua isi source
New-Item -ItemType Directory -Path $target | Out-Null
Copy-Item -Path "$source\*" -Destination $target -Recurse -Force

# Verify
$sqlCount = (Get-ChildItem -Path $target -Recurse -Filter "*.sql" | Measure-Object).Count
Write-Host "==> Migrations synced ke deploy/helm/parkir-pintar/migrations/" -ForegroundColor Green
Write-Host "    Total SQL files: $sqlCount"
