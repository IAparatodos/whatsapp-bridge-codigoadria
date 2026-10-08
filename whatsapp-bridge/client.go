package main

// Código AdrIA additions for client installs: QR in a browser page, a `send` subcommand that needs
// no Python, a configurable port and a working directory that does not depend on how it was launched.

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

// chdirToExecutable makes store/ resolve next to the binary, so autostart entries and shortcuts work
// whatever their working directory is.
func chdirToExecutable() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	_ = os.Chdir(filepath.Dir(exe))
}

func bridgePort() int {
	var port int
	if _, err := fmt.Sscanf(os.Getenv("WA_BRIDGE_PORT"), "%d", &port); err == nil && port > 0 {
		return port
	}
	return 8080
}

var qrPageOpened sync.Once

const qrPageHead = `<!doctype html><html lang="es"><head><meta charset="utf-8"><title>Conectar WhatsApp</title>`

// showQRPage writes the current code as store/qr.png plus a page that reloads itself, and opens the
// page once. WhatsApp rotates the code every ~20 s; the page picks up each new image on its own.
func showQRPage(code string) {
	if err := qrcode.WriteFile(code, qrcode.Medium, 420, filepath.Join("store", "qr.png")); err != nil {
		fmt.Println("WARN: could not write QR image:", err)
		return
	}
	page := qrPageHead + `<meta http-equiv="refresh" content="3"></head>
<body style="font-family:system-ui,sans-serif;text-align:center;padding:32px;background:#f4f7f7;color:#14181a">
<h1 style="font-size:1.6rem">Conecta tu WhatsApp</h1>
<p>En el móvil: WhatsApp → Ajustes → Dispositivos vinculados → Vincular un dispositivo.<br>Escanea este código.</p>
<img src="qr.png?t=` + fmt.Sprint(time.Now().UnixNano()) + `" width="420" height="420" alt="Código QR">
<p style="color:#6d797e">El código cambia cada pocos segundos; esta página se actualiza sola.</p>
</body></html>`
	pagePath := filepath.Join("store", "qr.html")
	if err := os.WriteFile(pagePath, []byte(page), 0600); err != nil {
		return
	}
	qrPageOpened.Do(func() { openInBrowser(pagePath) })
}

func showQRConnected() {
	page := qrPageHead + `</head>
<body style="font-family:system-ui,sans-serif;text-align:center;padding:48px;background:#f4f7f7;color:#14181a">
<h1 style="font-size:2rem">✓ WhatsApp conectado</h1><p>Ya puedes cerrar esta ventana.</p></body></html>`
	_ = os.WriteFile(filepath.Join("store", "qr.html"), []byte(page), 0600)
	_ = os.Remove(filepath.Join("store", "qr.png"))
}

func openInBrowser(path string) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", abs)
	case "darwin":
		cmd = exec.Command("open", abs)
	default:
		cmd = exec.Command("xdg-open", abs)
	}
	_ = cmd.Start()
}

// normalizePhone accepts "+34 619 94 57 59", "619945759" or a full JID. A bare 9-digit Spanish
// number gets the 34 prefix; anything with "@" is left alone.
func normalizePhone(to string) string {
	if strings.Contains(to, "@") {
		return to
	}
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, to)
	if len(digits) == 9 && strings.ContainsRune("6789", rune(digits[0])) {
		digits = "34" + digits
	}
	return digits + "@s.whatsapp.net"
}

// runSendCommand: whatsapp-bridge send --a <número|jid> [--texto "..."] [--archivo ruta] --aprobado-por "..."
// It talks to the running bridge, so every guard of the REST API (approval, allowlist, cap, log) applies.
func runSendCommand(args []string) int {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	to := fs.String("a", "", "número de teléfono o JID del destinatario")
	text := fs.String("texto", "", "texto del mensaje (o pie del archivo)")
	file := fs.String("archivo", "", "vídeo, imagen, audio o documento a adjuntar")
	approvedBy := fs.String("aprobado-por", "", "quién aprobó este envío")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *to == "" || *approvedBy == "" || (*text == "" && *file == "") {
		fmt.Fprintln(os.Stderr, "Uso: whatsapp-bridge send --a <número> [--texto \"...\"] [--archivo ruta] --aprobado-por \"...\"")
		return 2
	}

	payload := map[string]string{
		"recipient":   normalizePhone(*to),
		"message":     *text,
		"approved_by": *approvedBy,
	}
	if *file != "" {
		abs, err := filepath.Abs(*file)
		if err == nil {
			if st, statErr := os.Stat(abs); statErr != nil || st.IsDir() {
				err = fmt.Errorf("no existe el archivo")
			}
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "No existe el archivo: %s\n", *file)
			return 1
		}
		payload["media_path"] = abs
	}

	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("http://127.0.0.1:%d/api/send", bridgePort())
	resp, err := (&http.Client{Timeout: 2 * time.Minute}).Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintln(os.Stderr, "El bridge no está en marcha: arráncalo antes de enviar.")
		return 1
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "RECHAZADO %d: %s\n", resp.StatusCode, strings.TrimSpace(string(out)))
		return 1
	}
	fmt.Println(strings.TrimSpace(string(out)))
	return 0
}
