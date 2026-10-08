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
