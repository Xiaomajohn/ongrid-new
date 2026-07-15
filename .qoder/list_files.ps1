$paths = @(
  'f:\Code\Go\运维\ongrid-new\web\src\api',
  'f:\Code\Go\运维\ongrid-new\web\src\lib',
  'f:\Code\Go\运维\ongrid-new\web\src\pages\Plugins',
  'f:\Code\Go\运维\ongrid-new\scripts',
  'f:\Code\Go\运维\ongrid-new\deploy\pluginhost',
  'f:\Code\Go\运维\ongrid-new\.github\workflows'
)
foreach ($p in $paths) {
  Write-Host "==== $p ===="
  if (Test-Path $p) {
    Get-ChildItem -Path $p -Recurse -File -ErrorAction SilentlyContinue | ForEach-Object {
      $rel = $_.FullName.Substring($p.Length + 1)
      Write-Host ("  {0,8}  {1}" -f $_.Length, $rel)
    }
  } else {
    Write-Host "  (missing)"
  }
}
