package main

// Link to the client's own web app (a CRM-like dashboard), with every connection opened by the bridge:
//   - every stored message is pushed to POST {url}/api/whatsapp/eventos (batched, queued on disk while
//     the web is down, so nothing is lost);
//   - GET {url}/api/whatsapp/salida is polled for messages the web wants sent; each one goes through the
//     same guarded send path and its result is reported to POST {url}/api/whatsapp/salida/{id}.
// No tunnels and no open ports: it works behind any home or office router.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	webConfigPath = "store/web.json"
	webQueuePath  = "store/web-queue.jsonl"
	webBatchSize  = 200
)

type webConfig struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

func loadWebConfig() (webConfig, bool) {
	var cfg webConfig
	data, err := os.ReadFile(webConfigPath)
	if err != nil || json.Unmarshal(data, &cfg) != nil || cfg.URL == "" || cfg.Token == "" {
		return cfg, false
	}
	cfg.URL = strings.TrimRight(cfg.URL, "/")
	return cfg, true
}

// webEvent is one message as the web receives it. Field names are the public contract (see the skill).
type webEvent struct {
	ID          string `json:"id"`
	Chat        string `json:"chat"`
	NombreChat  string `json:"nombre_chat"`
	Telefono    string `json:"telefono"`
	Remitente   string `json:"remitente"`
	DeMi        bool   `json:"de_mi"`
	Texto       string `json:"texto"`
	TipoArchivo string `json:"tipo_archivo,omitempty"`
	NombreArch  string `json:"nombre_archivo,omitempty"`
	Fecha       string `json:"fecha"`
	EsGrupo     bool   `json:"es_grupo"`
}

var webQueueMu sync.Mutex

// webEnqueue appends an event to the on-disk queue. Cheap and never blocks message storage on the network.
func webEnqueue(store *MessageStore, id, chatJID, sender, content string, fecha string, fromMe bool, mediaType, filename string) {
	if _, ok := loadWebConfig(); !ok {
		return
	}
	var name string
	_ = store.db.QueryRow("SELECT COALESCE(name,'') FROM chats WHERE jid = ?", chatJID).Scan(&name)
	ev := webEvent{
		ID: id, Chat: chatJID, NombreChat: name, Remitente: sender, DeMi: fromMe, Texto: content,
		TipoArchivo: mediaType, NombreArch: filename, Fecha: fecha,
		EsGrupo: strings.HasSuffix(chatJID, "@g.us"),
	}
	if strings.HasSuffix(chatJID, "@s.whatsapp.net") {
		ev.Telefono = strings.TrimSuffix(chatJID, "@s.whatsapp.net")
	}
	line, _ := json.Marshal(ev)
	webQueueMu.Lock()
	defer webQueueMu.Unlock()
	f, err := os.OpenFile(webQueuePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(line, '\n'))
}

func webRequest(cfg webConfig, method, path string, body []byte) (*http.Response, error) {
	req, err := http.NewRequest(method, cfg.URL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "whatsapp-bridge-codigoadria")
	return (&http.Client{Timeout: 30 * time.Second}).Do(req)
}

// webFlushQueue sends the queue in batches and keeps whatever the web did not accept.
func webFlushQueue(cfg webConfig) {
	webQueueMu.Lock()
	data, err := os.ReadFile(webQueuePath)
	webQueueMu.Unlock()
	if err != nil || len(data) == 0 {
		return
	}
	var lines []json.RawMessage
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		if l := bytes.TrimSpace(sc.Bytes()); len(l) > 0 {
			lines = append(lines, append(json.RawMessage(nil), l...))
		}
	}
	sent := 0
	for sent < len(lines) {
		end := sent + webBatchSize
		if end > len(lines) {
			end = len(lines)
		}
		body, _ := json.Marshal(map[string]any{"eventos": lines[sent:end]})
		resp, err := webRequest(cfg, "POST", "/api/whatsapp/eventos", body)
		if err != nil {
			break
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			fmt.Printf("WEB: eventos rechazados (%d), se reintentará\n", resp.StatusCode)
			break
		}
		sent = end
	}
	if sent == 0 {
		return
	}
	// Remove what was delivered; keep events queued meanwhile (appended after our read).
	webQueueMu.Lock()
	defer webQueueMu.Unlock()
	current, _ := os.ReadFile(webQueuePath)
	rest := current[len(data):]
	var keep bytes.Buffer
	for _, l := range lines[sent:] {
		keep.Write(l)
		keep.WriteByte('\n')
	}
	keep.Write(rest)
	tmp := webQueuePath + ".tmp"
	if os.WriteFile(tmp, keep.Bytes(), 0600) == nil {
		os.Rename(tmp, webQueuePath)
	}
}

type outboxItem struct {
	ID           string `json:"id"`
	Destinatario string `json:"destinatario"`
	Texto        string `json:"texto"`
	ArchivoURL   string `json:"archivo_url"`
	NombreArch   string `json:"nombre_archivo"`
}

func webReportResult(cfg webConfig, id, estado, motivo string) {
	body, _ := json.Marshal(map[string]string{"estado": estado, "motivo": motivo})
	if resp, err := webRequest(cfg, "POST", "/api/whatsapp/salida/"+url.PathEscape(id), body); err == nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

// downloadAttachment fetches a file the web wants sent, keeping its name so it arrives as e.g. factura.pdf.
func downloadAttachment(cfg webConfig, item outboxItem) (string, error) {
	req, err := http.NewRequest("GET", item.ArchivoURL, nil)
	if err != nil {
		return "", err
	}
	// Only hand our token to the web we are linked to, never to a third-party file host.
	if strings.HasPrefix(item.ArchivoURL, cfg.URL+"/") {
		req.Header.Set("Authorization", "Bearer "+cfg.Token)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("no se pudo descargar el archivo (%d)", resp.StatusCode)
	}
	name := filepath.Base(item.NombreArch)
	if name == "" || name == "." || name == "/" {
		name = filepath.Base(resp.Request.URL.Path)
	}
	dir := filepath.Join("store", "tmp", item.ID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, io.LimitReader(resp.Body, 100<<20)); err != nil {
		return "", err
	}
	return path, nil
}

func webPollOutbox(cfg webConfig) {
	resp, err := webRequest(cfg, "GET", "/api/whatsapp/salida", nil)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return
	}
	var out struct {
		Mensajes []outboxItem `json:"mensajes"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil {
		return
	}
	for _, item := range out.Mensajes {
		if item.ID == "" || item.Destinatario == "" || (item.Texto == "" && item.ArchivoURL == "") {
			continue
		}
		file := ""
		if item.ArchivoURL != "" {
			path, err := downloadAttachment(cfg, item)
			if err != nil {
				webReportResult(cfg, item.ID, "error", err.Error())
				continue
			}
			file = path
		}
		_, err := sendViaBridge(item.Destinatario, item.Texto, file, "web:"+cfg.URL)
		if file != "" {
			os.RemoveAll(filepath.Dir(file))
		}
		if err != nil {
			webReportResult(cfg, item.ID, "error", err.Error())
		} else {
			webReportResult(cfg, item.ID, "enviado", "")
		}
	}
}

// toRFC3339 turns a stored timestamp ("2006-01-02 15:04:05-07:00", optional fractions) into RFC 3339.
func toRFC3339(v string) string {
	for _, layout := range []string{"2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05-07:00", time.RFC3339Nano} {
		if t, err := time.Parse(layout, v); err == nil {
			return t.Format(time.RFC3339)
		}
	}
	return v
}

// startWebSync runs in the background for the life of the bridge. Picks up web.json changes on its own.
func startWebSync(store *MessageStore) {
	go func() {
		for {
			if cfg, ok := loadWebConfig(); ok {
				webBackfill(store)
				webFlushQueue(cfg)
				webPollOutbox(cfg)
			}
			time.Sleep(5 * time.Second)
		}
	}()
}

// webBackfill queues the whole local history once, the first time a web is linked.
func webBackfill(store *MessageStore) {
	marker := "store/web-backfill.done"
	if _, ok := loadWebConfig(); !ok {
		return
	}
	if _, err := os.Stat(marker); err == nil {
		return
	}
	rows, err := store.db.Query(`SELECT id, chat_jid, sender, COALESCE(content,''), CAST(timestamp AS TEXT), is_from_me,
		COALESCE(media_type,''), COALESCE(filename,'') FROM messages ORDER BY timestamp`)
	if err != nil {
		return
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var id, chat, sender, content, media, file string
		var ts string
		var fromMe bool
		if rows.Scan(&id, &chat, &sender, &content, &ts, &fromMe, &media, &file) == nil {
			webEnqueue(store, id, chat, sender, content, toRFC3339(ts), fromMe, media, file)
			n++
		}
	}
	os.WriteFile(marker, []byte(time.Now().Format(time.RFC3339)), 0600)
	fmt.Printf("WEB: histórico en cola (%d mensajes)\n", n)
}

// runConnectWeb: whatsapp-bridge conectar-web --url https://suweb.com --token XXX
func runConnectWeb(args []string) int {
	fs := flag.NewFlagSet("conectar-web", flag.ContinueOnError)
	u := fs.String("url", "", "dirección de la web, p. ej. https://miempresa.com")
	token := fs.String("token", "", "clave que te ha dado la web")
	quitar := fs.Bool("quitar", false, "desconecta la web")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	os.MkdirAll("store", 0755)
	if *quitar {
		os.Remove(webConfigPath)
		fmt.Println("Web desconectada.")
		return 0
	}
	if !strings.HasPrefix(*u, "https://") && !strings.HasPrefix(*u, "http://localhost") && !strings.HasPrefix(*u, "http://127.0.0.1") {
		fmt.Fprintln(os.Stderr, "Uso: whatsapp-bridge conectar-web --url https://suweb.com --token CLAVE (la web tiene que ir por https)")
		return 2
	}
	if *token == "" {
		fmt.Fprintln(os.Stderr, "Falta --token")
		return 2
	}
	cfg := webConfig{URL: strings.TrimRight(*u, "/"), Token: *token}
	// Check the web answers and accepts the token before saving anything.
	resp, err := webRequest(cfg, "GET", "/api/whatsapp/salida", nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "No puedo llegar a la web:", err)
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		fmt.Fprintln(os.Stderr, "La web rechaza la clave (token incorrecto).")
		return 1
	}
	if resp.StatusCode/100 != 2 {
		fmt.Fprintf(os.Stderr, "La web no tiene instalada la conexión de WhatsApp (respuesta %d en /api/whatsapp/salida).\n", resp.StatusCode)
		return 1
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(webConfigPath, data, 0600); err != nil {
		fmt.Fprintln(os.Stderr, "No pude guardar la configuración:", err)
		return 1
	}
	os.Remove("store/web-backfill.done")
	fmt.Println("✓ Web conectada. El histórico se enviará en cuanto el bridge esté en marcha.")
	return 0
}
