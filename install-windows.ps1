# Código AdrIA WhatsApp bridge — Windows installer.
#   irm https://raw.githubusercontent.com/IAparatodos/whatsapp-bridge-codigoadria/main/install-windows.ps1 | iex
#
# Downloads the binary, opens sends to any contact, adds a Startup shortcut (minimized window, visible in the
# taskbar), wires Claude Code / Claude Desktop and shows the QR in the browser. Re-running it updates the
# binary and keeps the WhatsApp session (store\).
$ErrorActionPreference = 'Stop'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$Repo  = 'IAparatodos/whatsapp-bridge-codigoadria'
$Dir   = Join-Path $env:LOCALAPPDATA 'WhatsApp-CodigoAdria'
$Exe   = Join-Path $Dir 'whatsapp-bridge.exe'
$Store = Join-Path $Dir 'store'

Write-Host '-> Descargando el programa...'
New-Item -ItemType Directory -Force -Path $Store | Out-Null
$tmp = "$Exe.new"
Invoke-WebRequest -UseBasicParsing "https://github.com/$Repo/releases/latest/download/whatsapp-bridge-windows-amd64.exe" -OutFile $tmp

# Stop the running copy before replacing the binary.
Get-Process whatsapp-bridge -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq $Exe } | Stop-Process -Force
Start-Sleep -Seconds 1
Move-Item -Force $tmp $Exe

# "*" = may send to any contact. Only written on a fresh install, never over a list already there.
$Allow = Join-Path $Store 'send-allowlist.json'
if (-not (Test-Path $Allow)) { Set-Content -Path $Allow -Value '["*"]' -Encoding Ascii }

# Start with Windows, minimized: the window stays in the taskbar so it is clear what is running.
$Link = Join-Path ([Environment]::GetFolderPath('Startup')) 'WhatsApp Codigo AdrIA.lnk'
$sc = (New-Object -ComObject WScript.Shell).CreateShortcut($Link)
$sc.TargetPath = $Exe
$sc.WorkingDirectory = $Dir
$sc.WindowStyle = 7
$sc.Description = 'Bridge de WhatsApp de Codigo AdrIA'
$sc.Save()

Write-Host '-> Conectando con Claude...'
# Skill: lets Claude Code use WhatsApp and wire it into the client's web apps.
$SkillDir = Join-Path $env:USERPROFILE '.claude\skills\whatsapp-codigoadria'
New-Item -ItemType Directory -Force -Path $SkillDir | Out-Null
try {
  Invoke-WebRequest -UseBasicParsing "https://raw.githubusercontent.com/$Repo/main/skill/whatsapp-codigoadria/SKILL.md" -OutFile (Join-Path $SkillDir 'SKILL.md')
} catch { Write-Host '   (no pude bajar la skill; se puede reinstalar luego)' }

# Claude Code: MCP server for the user, available in every project.
if (Get-Command claude -ErrorAction SilentlyContinue) {
  & claude mcp remove --scope user whatsapp *> $null
  & claude mcp add --scope user whatsapp -- $Exe mcp *> $null
  if ($LASTEXITCODE -eq 0) { Write-Host '   OK Claude Code' }
}

# Claude Desktop: merge our server into its config without touching the rest.
$DesktopDir = Join-Path $env:APPDATA 'Claude'
if (Test-Path $DesktopDir) {
  $Cfg = Join-Path $DesktopDir 'claude_desktop_config.json'
  $ok = $true
  $json = [pscustomobject]@{}
  if (Test-Path $Cfg) {
    try { $json = Get-Content $Cfg -Raw | ConvertFrom-Json } catch { $ok = $false }
  }
  if ($ok) {
    if (-not $json.PSObject.Properties['mcpServers']) { $json | Add-Member -NotePropertyName mcpServers -NotePropertyValue ([pscustomobject]@{}) }
    $server = [pscustomobject]@{ command = $Exe; args = @('mcp') }
    if ($json.mcpServers.PSObject.Properties['whatsapp']) { $json.mcpServers.whatsapp = $server }
    else { $json.mcpServers | Add-Member -NotePropertyName whatsapp -NotePropertyValue $server }
    # UTF-8 without BOM: Claude Desktop cannot read a BOM.
    [IO.File]::WriteAllText($Cfg, ($json | ConvertTo-Json -Depth 20), (New-Object Text.UTF8Encoding $false))
    Write-Host '   OK Claude Desktop (reinicialo para verlo)'
  } else {
    Write-Host '   claude_desktop_config.json no es JSON valido; no lo toco'
  }
}

Write-Host '-> Arrancando...'
Start-Process -FilePath $Exe -WorkingDirectory $Dir -WindowStyle Minimized

function Test-Bridge {
  try { (New-Object Net.Sockets.TcpClient).Connect('127.0.0.1', 8080); return $true } catch { return $false }
}
for ($i = 0; $i -lt 40; $i++) {
  if ((Test-Path (Join-Path $Store 'qr.html')) -or (Test-Bridge)) { break }
  Start-Sleep -Seconds 1
}

if ((Test-Bridge) -and -not (Test-Path (Join-Path $Store 'qr.png'))) {
  Write-Host 'OK  WhatsApp ya estaba conectado. Programa actualizado.'
} else {
  Write-Host 'OK  Instalado. Se ha abierto una pagina con un codigo QR:'
  Write-Host '    en el movil, WhatsApp > Ajustes > Dispositivos vinculados > Vincular un dispositivo, y escanealo.'
  Write-Host "    (Si no se abre: $Store\qr.html)"
}
Write-Host ''
Write-Host "Para enviar:  & `"$Exe`" send --a 600123456 --texto `"Hola`" --aprobado-por `"tu nombre`""
Write-Host 'Con archivo:  anade --archivo C:\ruta\al\archivo.pdf'
