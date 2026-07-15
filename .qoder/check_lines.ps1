$root = 'f:\Code\Go\运维\ongrid-new\internal\pluginhost'
Get-ChildItem -Path $root -Recurse -File -Filter '*.go' | ForEach-Object {
  $lines = (Get-Content $_.FullName | Measure-Object -Line).Lines
  $rel = $_.FullName.Substring($root.Length + 1)
  Write-Host ("{0,5}  {1}" -f $lines, $rel)
} | Sort-Object
