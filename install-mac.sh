#!/bin/bash
# Código AdrIA WhatsApp bridge — Mac installer.
#   curl -fsSL https://raw.githubusercontent.com/IAparatodos/whatsapp-bridge-codigoadria/main/install-mac.sh | bash
#
# Downloads the right binary, opens sends to any contact, starts it at login and shows the QR in the
# browser. Re-running it updates the binary and keeps the WhatsApp session (store/).
set -euo pipefail

REPO="IAparatodos/whatsapp-bridge-codigoadria"
DIR="$HOME/WhatsApp-CodigoAdria"
LABEL="com.codigoadria.whatsapp-bridge"
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"

case "$(uname -m)" in
  arm64) ASSET="whatsapp-bridge-darwin-arm64" ;;
  x86_64) ASSET="whatsapp-bridge-darwin-amd64" ;;
  *) echo "Este Mac no está soportado ($(uname -m))."; exit 1 ;;
esac

echo "→ Descargando el programa…"
mkdir -p "$DIR/store"
curl -fsSL "https://github.com/$REPO/releases/latest/download/$ASSET" -o "$DIR/whatsapp-bridge.new"
chmod +x "$DIR/whatsapp-bridge.new"

# Stop the running copy before replacing the binary.
launchctl bootout "gui/$(id -u)/$LABEL" 2>/dev/null || true
mv "$DIR/whatsapp-bridge.new" "$DIR/whatsapp-bridge"

# "*" = may send to any contact. Only written on a fresh install, never over a list already there.
[ -f "$DIR/store/send-allowlist.json" ] || echo '["*"]' > "$DIR/store/send-allowlist.json"

mkdir -p "$HOME/Library/LaunchAgents"
cat > "$PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>$LABEL</string>
  <key>ProgramArguments</key><array><string>$DIR/whatsapp-bridge</string></array>
  <key>WorkingDirectory</key><string>$DIR</string>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>$DIR/bridge.log</string>
  <key>StandardErrorPath</key><string>$DIR/bridge.log</string>
</dict></plist>
EOF
launchctl bootstrap "gui/$(id -u)" "$PLIST"

echo "→ Conectando con Claude…"
# Skill: lets Claude Code use WhatsApp and wire it into the client's web apps.
mkdir -p "$HOME/.claude/skills/whatsapp-codigoadria"
curl -fsSL "https://raw.githubusercontent.com/$REPO/main/skill/whatsapp-codigoadria/SKILL.md" \
  -o "$HOME/.claude/skills/whatsapp-codigoadria/SKILL.md" || echo "  (no pude bajar la skill; se puede reinstalar luego)"

# Claude Code: MCP server for the user, available in every project.
if command -v claude >/dev/null 2>&1; then
  claude mcp remove --scope user whatsapp >/dev/null 2>&1 || true
  claude mcp add --scope user whatsapp -- "$DIR/whatsapp-bridge" mcp >/dev/null && echo "  ✓ Claude Code"
fi

# Claude Desktop: merge our server into its config without touching the rest.
DESKTOP_CFG="$HOME/Library/Application Support/Claude/claude_desktop_config.json"
if [ -d "$HOME/Library/Application Support/Claude" ]; then
  osascript -l JavaScript - "$DESKTOP_CFG" "$DIR/whatsapp-bridge" <<'JXA' && echo "  ✓ Claude Desktop (reinícialo para verlo)"
ObjC.import('Foundation');
function run(argv) {
  const [path, exe] = argv;
  const fm = $.NSFileManager.defaultManager;
  let cfg = {};
  if (fm.fileExistsAtPath(path)) {
    const txt = $.NSString.stringWithContentsOfFileEncodingError(path, $.NSUTF8StringEncoding, null).js;
    try { cfg = JSON.parse(txt); } catch (e) { throw new Error('claude_desktop_config.json no es JSON válido; no lo toco'); }
  }
  cfg.mcpServers = cfg.mcpServers || {};
  cfg.mcpServers.whatsapp = { command: exe, args: ['mcp'] };
  $(JSON.stringify(cfg, null, 2)).writeToFileAtomicallyEncodingError(path, true, $.NSUTF8StringEncoding, null);
}
JXA
fi

echo "→ Arrancando…"
for _ in $(seq 1 40); do
  if [ -f "$DIR/store/qr.html" ] || grep -q "Connected to WhatsApp" "$DIR/bridge.log" 2>/dev/null; then break; fi
  sleep 1
done

if grep -q "Connected to WhatsApp" "$DIR/bridge.log" 2>/dev/null && [ ! -f "$DIR/store/qr.png" ]; then
  echo "✓ WhatsApp ya estaba conectado. Programa actualizado."
else
  echo "✓ Instalado. Se ha abierto una página con un código QR:"
  echo "  en el móvil, WhatsApp → Ajustes → Dispositivos vinculados → Vincular un dispositivo, y escanéalo."
  echo "  (Si no se abre: open \"$DIR/store/qr.html\")"
fi
echo ""
echo "Para enviar:  \"$DIR/whatsapp-bridge\" send --a 600123456 --texto \"Hola\" --aprobado-por \"tu nombre\""
echo "Con archivo:  añade --archivo /ruta/al/archivo.pdf"
