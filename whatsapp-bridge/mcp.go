package main

// `whatsapp-bridge mcp`: an MCP server over stdio so Claude (Code or Desktop) can read and send WhatsApp.
// Reads store/*.db read-only; sends go through the running bridge, so every send guard applies.

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const mcpInstructions = `WhatsApp del usuario, conectado desde su propio número.
- Para leer: listar_chats, leer_chat, buscar_mensajes y buscar_contactos. Las fechas van en hora local.
- Para enviar: enviar_mensaje o enviar_archivo SOLO cuando el usuario lo pida. Antes de enviar, confirma con él
  el destinatario y el texto exactos; un mensaje enviado no se puede deshacer.
- Si un envío falla porque el bridge no está en marcha, dile que abra «WhatsApp Código AdrIA».`

func openReadOnly(path string) (*sql.DB, error) {
	return sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(10000)")
}

type mcpStore struct {
	msgs    *sql.DB
	session *sql.DB
}

// displayName resolves a JID to the best known name: chat name, then the contact book.
func (st *mcpStore) displayName(jid string) string {
	var name sql.NullString
	if st.msgs.QueryRow(`SELECT name FROM chats WHERE jid = ?`, jid).Scan(&name) == nil && name.String != "" {
		return name.String
	}
	if st.session != nil {
		user := strings.SplitN(jid, "@", 2)[0]
		var full, push sql.NullString
		if st.session.QueryRow(`SELECT full_name, push_name FROM whatsmeow_contacts WHERE their_jid LIKE ? LIMIT 1`,
			user+"@%").Scan(&full, &push) == nil {
			if full.String != "" {
				return full.String
			}
			return push.String
		}
	}
	return ""
}

// resolveChat accepts a JID, a phone number or part of a chat name.
func (st *mcpStore) resolveChat(chat string) (string, error) {
	if strings.Contains(chat, "@") {
		return chat, nil
	}
	if jid := normalizePhone(chat); strings.Trim(chat, "+ 0123456789") == "" && jid != "@s.whatsapp.net" {
		return jid, nil
	}
	var jid string
	err := st.msgs.QueryRow(`SELECT jid FROM chats WHERE name LIKE ? ORDER BY last_message_time DESC LIMIT 1`,
		"%"+chat+"%").Scan(&jid)
	if err != nil {
		return "", fmt.Errorf("no encuentro ningún chat que se llame «%s»", chat)
	}
	return jid, nil
}

type chatInfo struct {
	Chat          string `json:"chat"`
	Nombre        string `json:"nombre"`
	UltimoMensaje string `json:"ultimo_mensaje"`
}

type mensaje struct {
	Fecha   string `json:"fecha"`
	Chat    string `json:"chat"`
	De      string `json:"de"`
	Texto   string `json:"texto"`
	Archivo string `json:"archivo,omitempty"`
}

func (st *mcpStore) queryMessages(where string, args []any, limit int) ([]mensaje, error) {
	rows, err := st.msgs.Query(`SELECT CAST(timestamp AS TEXT), chat_jid, sender, is_from_me, COALESCE(content,''),
		COALESCE(media_type,''), COALESCE(filename,'') FROM messages WHERE `+where+
		` ORDER BY timestamp DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []mensaje
	for rows.Next() {
		var m mensaje
		var sender, media, file string
		var fromMe bool
		if err := rows.Scan(&m.Fecha, &m.Chat, &sender, &fromMe, &m.Texto, &media, &file); err != nil {
			return nil, err
		}
		if fromMe {
			m.De = "yo"
		} else if n := st.displayName(sender + "@s.whatsapp.net"); n != "" {
			m.De = n
		} else {
			m.De = sender
		}
		if media != "" {
			m.Archivo = strings.TrimSpace(media + " " + file)
		}
		out = append(out, m)
	}
	// Oldest first reads like a conversation.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}

func textResult(format string, a ...any) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(format, a...)}}}
}

func limitOr(n, def, max int) int {
	if n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

func runMCPServer() int {
	msgs, err := openReadOnly("store/messages.db")
	if err != nil {
		fmt.Fprintln(os.Stderr, "No puedo abrir los mensajes:", err)
		return 1
	}
	st := &mcpStore{msgs: msgs}
	if s, err := openReadOnly("store/whatsapp.db"); err == nil {
		st.session = s
	}

	server := mcp.NewServer(&mcp.Implementation{Name: "whatsapp-codigoadria", Version: "0.3.0"},
		&mcp.ServerOptions{Instructions: mcpInstructions})

	type enviarIn struct {
		Destinatario string `json:"destinatario" jsonschema:"teléfono (600123456 o +34 600 12 34 56), nombre del chat o JID"`
		Texto        string `json:"texto" jsonschema:"texto del mensaje"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "enviar_mensaje", Description: "Envía un WhatsApp de texto desde el número del usuario. Solo cuando el usuario lo pida y tras confirmar destinatario y texto."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in enviarIn) (*mcp.CallToolResult, any, error) {
			jid, err := st.resolveChat(in.Destinatario)
			if err != nil {
				return nil, nil, err
			}
			out, err := sendViaBridge(jid, in.Texto, "", "usuario vía Claude (MCP)")
			if err != nil {
				return nil, nil, err
			}
			return textResult("Enviado a %s. %s", jid, out), nil, nil
		})

	type archivoIn struct {
		Destinatario string `json:"destinatario" jsonschema:"teléfono, nombre del chat o JID"`
		Ruta         string `json:"ruta" jsonschema:"ruta del archivo en este ordenador (vídeo, imagen, PDF, documento)"`
		Pie          string `json:"pie,omitempty" jsonschema:"texto opcional que acompaña al archivo"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "enviar_archivo", Description: "Envía un vídeo, imagen, PDF u otro documento por WhatsApp. Solo cuando el usuario lo pida y tras confirmar destinatario y archivo."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in archivoIn) (*mcp.CallToolResult, any, error) {
			jid, err := st.resolveChat(in.Destinatario)
			if err != nil {
				return nil, nil, err
			}
			out, err := sendViaBridge(jid, in.Pie, in.Ruta, "usuario vía Claude (MCP)")
			if err != nil {
				return nil, nil, err
			}
			return textResult("Archivo enviado a %s. %s", jid, out), nil, nil
		})

	type buscarIn struct {
		Texto string `json:"texto" jsonschema:"nombre o parte del número"`
	}
	type contacto struct {
		Nombre   string `json:"nombre"`
		Telefono string `json:"telefono"`
		Chat     string `json:"chat"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "buscar_contactos", Description: "Busca contactos por nombre o número."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in buscarIn) (*mcp.CallToolResult, struct {
			Contactos []contacto `json:"contactos"`
		}, error) {
			var res struct {
				Contactos []contacto `json:"contactos"`
			}
			q := "%" + in.Texto + "%"
			seen := map[string]bool{}
			if st.session != nil {
				rows, err := st.session.Query(`SELECT their_jid, COALESCE(NULLIF(full_name,''), push_name, '') FROM whatsmeow_contacts
					WHERE full_name LIKE ? OR push_name LIKE ? OR their_jid LIKE ? LIMIT 30`, q, q, q)
				if err == nil {
					for rows.Next() {
						var c contacto
						if rows.Scan(&c.Chat, &c.Nombre) == nil && !seen[c.Chat] {
							seen[c.Chat] = true
							c.Telefono = strings.SplitN(c.Chat, "@", 2)[0]
							res.Contactos = append(res.Contactos, c)
						}
					}
					rows.Close()
				}
			}
			rows, err := st.msgs.Query(`SELECT jid, COALESCE(name,'') FROM chats WHERE name LIKE ? OR jid LIKE ? LIMIT 30`, q, q)
			if err == nil {
				for rows.Next() {
					var c contacto
					if rows.Scan(&c.Chat, &c.Nombre) == nil && !seen[c.Chat] {
						seen[c.Chat] = true
						c.Telefono = strings.SplitN(c.Chat, "@", 2)[0]
						res.Contactos = append(res.Contactos, c)
					}
				}
				rows.Close()
			}
			return nil, res, nil
		})

	type listarIn struct {
		Texto  string `json:"texto,omitempty" jsonschema:"filtra por nombre del chat"`
		Limite int    `json:"limite,omitempty" jsonschema:"cuántos chats (por defecto 20, máximo 100)"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "listar_chats", Description: "Lista los chats con actividad más reciente."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in listarIn) (*mcp.CallToolResult, struct {
			Chats []chatInfo `json:"chats"`
		}, error) {
			var res struct {
				Chats []chatInfo `json:"chats"`
			}
			rows, err := st.msgs.Query(`SELECT jid, COALESCE(name,''), COALESCE(CAST(last_message_time AS TEXT),'') FROM chats
				WHERE name LIKE ? ORDER BY last_message_time DESC LIMIT ?`, "%"+in.Texto+"%", limitOr(in.Limite, 20, 100))
			if err != nil {
				return nil, res, err
			}
			defer rows.Close()
			for rows.Next() {
				var c chatInfo
				if rows.Scan(&c.Chat, &c.Nombre, &c.UltimoMensaje) == nil {
					res.Chats = append(res.Chats, c)
				}
			}
			return nil, res, rows.Err()
		})

	type leerIn struct {
		Chat   string `json:"chat" jsonschema:"teléfono, nombre del chat o JID"`
		Limite int    `json:"limite,omitempty" jsonschema:"cuántos mensajes (por defecto 30, máximo 300)"`
		Desde  string `json:"desde,omitempty" jsonschema:"fecha AAAA-MM-DD desde la que leer"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "leer_chat", Description: "Lee los últimos mensajes de un chat, del más antiguo al más reciente."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in leerIn) (*mcp.CallToolResult, struct {
			Mensajes []mensaje `json:"mensajes"`
		}, error) {
			var res struct {
				Mensajes []mensaje `json:"mensajes"`
			}
			jid, err := st.resolveChat(in.Chat)
			if err != nil {
				return nil, res, err
			}
			res.Mensajes, err = st.queryMessages(`chat_jid = ? AND CAST(timestamp AS TEXT) >= ?`, []any{jid, in.Desde}, limitOr(in.Limite, 30, 300))
			return nil, res, err
		})

	type buscarMsgIn struct {
		Texto  string `json:"texto" jsonschema:"palabras a buscar"`
		Chat   string `json:"chat,omitempty" jsonschema:"limita la búsqueda a un chat"`
		Desde  string `json:"desde,omitempty" jsonschema:"fecha AAAA-MM-DD"`
		Limite int    `json:"limite,omitempty" jsonschema:"máximo de resultados (por defecto 30, máximo 200)"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "buscar_mensajes", Description: "Busca mensajes que contengan un texto, en todos los chats o en uno."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in buscarMsgIn) (*mcp.CallToolResult, struct {
			Mensajes []mensaje `json:"mensajes"`
		}, error) {
			var res struct {
				Mensajes []mensaje `json:"mensajes"`
			}
			where := `content LIKE ? AND CAST(timestamp AS TEXT) >= ?`
			args := []any{"%" + in.Texto + "%", in.Desde}
			if in.Chat != "" {
				jid, err := st.resolveChat(in.Chat)
				if err != nil {
					return nil, res, err
				}
				where += ` AND chat_jid = ?`
				args = append(args, jid)
			}
			var err error
			res.Mensajes, err = st.queryMessages(where, args, limitOr(in.Limite, 30, 200))
			return nil, res, err
		})

	mcp.AddTool(server, &mcp.Tool{Name: "estado", Description: "Dice si el WhatsApp está conectado y cuántos envíos quedan hoy."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", bridgePort()), 2*time.Second)
			conectado := err == nil
			if conn != nil {
				conn.Close()
			}
			estado := "conectado"
			if !conectado {
				estado = "NO está en marcha (hay que abrir «WhatsApp Código AdrIA»)"
			}
			return textResult("WhatsApp %s. Envíos hoy: %d de %d.", estado, sendsToday(), dailyCap()), nil, nil
		})

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		fmt.Fprintln(os.Stderr, "MCP:", err)
		return 1
	}
	return 0
}
