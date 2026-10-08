---
name: whatsapp-codigoadria
description: Conecta el WhatsApp del usuario (bridge de Código AdrIA instalado en su ordenador) con Claude y con sus apps. Úsala cuando pida leer o enviar WhatsApp («mándale a Juan el PDF», «¿qué me ha dicho Ana?»), o cuando quiera que una web o app suya reciba los WhatsApp (estadísticas, conversaciones tipo CRM) o envíe mensajes («que la web mande la confirmación por WhatsApp», «conecta mi WhatsApp a la web»).
---

# WhatsApp de Código AdrIA

El usuario tiene instalado el **bridge de WhatsApp de Código AdrIA**: un programa que corre en su ordenador,
vinculado a su número por QR (como WhatsApp Web). Se usa de tres formas:

| Para qué | Cómo |
|---|---|
| Hablar por Claude | Herramientas MCP `whatsapp` (ver §1) |
| Que una web o app reciba y envíe WhatsApp | Tres endpoints en la web + `conectar-web` (ver §2) |
| Línea de comandos | `whatsapp-bridge send --a 600123456 --texto "..." [--archivo ruta] --aprobado-por "..."` |

Dónde está el programa: Mac `~/WhatsApp-CodigoAdria/whatsapp-bridge`; Windows
`%LOCALAPPDATA%\WhatsApp-CodigoAdria\whatsapp-bridge.exe`.

**Reglas que no se saltan:**
- Solo se envía cuando el usuario lo pide, y antes se confirma con él el destinatario y el texto exactos. Un
  WhatsApp enviado no se deshace y sale desde SU número.
- No se envían mensajes masivos ni en frío: WhatsApp banea los números que lo hacen. Una web solo manda
  mensajes que un cliente espera (confirmaciones, recordatorios, respuestas).
- El bridge tiene un tope diario de envíos (25 por defecto, en `store/config.json` → `tope_diario`). Si una
  web necesita más, se sube ahí, nunca se esquiva.

## 1 · Hablar por Claude (MCP)

El instalador registra el servidor MCP `whatsapp`. Herramientas:

- Leer: `listar_chats`, `leer_chat`, `buscar_mensajes` y `buscar_contactos`.
- Enviar: `enviar_mensaje` y `enviar_archivo` (vídeo, imagen, PDF, documento: la ruta es de este ordenador).
- Comprobar: `estado` (conectado y envíos de hoy).

Si no aparecen, se registra a mano:

```
claude mcp add --scope user whatsapp -- "<ruta del programa>" mcp
```

Si `estado` dice que no está en marcha: en Mac, `launchctl kickstart -k gui/$(id -u)/com.codigoadria.whatsapp-bridge`;
en Windows, abrir «WhatsApp Codigo AdrIA» desde el menú Inicio.

## 2 · Conectar una web o app (tipo CRM)

**Cómo funciona.** La web está en internet y el bridge en el ordenador del usuario, detrás de su router, así
que la web NO puede llamar al bridge. Todas las conexiones las abre el bridge:

- Empuja cada mensaje (entrante o saliente) a `POST /api/whatsapp/eventos`.
- Cada 5 segundos pregunta `GET /api/whatsapp/salida` por mensajes pendientes, los envía y confirma cada uno
  en `POST /api/whatsapp/salida/{id}`.

Si el ordenador está apagado, la web acumula la salida y el bridge acumula los eventos en una cola local.
Nada se pierde: todo se pone al día al volver a encenderlo. No hacen falta túneles ni abrir puertos.

### 2.1 · Contrato (tiene que cumplirse exacto)

Las tres rutas exigen `Authorization: Bearer <WHATSAPP_BRIDGE_TOKEN>` y responden **401** si falta o no
coincide. El token es un secreto largo y aleatorio que guarda la web en una variable de entorno.

**`POST /api/whatsapp/eventos`** → recibe lotes de hasta 200 mensajes:

```json
{ "eventos": [ {
  "id": "3EB06BF3E99E835F06C1BD",   // id de WhatsApp: clave única, idempotente (puede llegar repetido)
  "chat": "34600123456@s.whatsapp.net",  // o "...@g.us" si es grupo
  "nombre_chat": "Juan Pérez",
  "telefono": "34600123456",          // vacío en grupos
  "remitente": "34600123456",         // quién lo escribió (en grupos, el participante)
  "de_mi": false,                     // true = lo envió el usuario
  "texto": "Hola, ¿tenéis cita el jueves?",
  "tipo_archivo": "image",            // opcional: image | video | audio | document
  "nombre_archivo": "foto.jpg",       // opcional
  "fecha": "2026-10-08T23:51:55+02:00",
  "es_grupo": false
} ] }
```

Responde **2xx** solo cuando los ha guardado. Cualquier otra respuesta hace que el bridge reintente el lote
entero, así que guarda con **upsert por `id`**, nunca con insert a secas. La primera vez que se conecta llega
todo el histórico, que pueden ser decenas de miles de mensajes en lotes de 200.

**`GET /api/whatsapp/salida`** → devuelve lo pendiente de enviar:

```json
{ "mensajes": [ {
  "id": "msg_01J...",                 // id de la web, único
  "destinatario": "600123456",        // teléfono (9 cifras → se asume España) o JID
  "texto": "Tu cita está confirmada para el jueves a las 10:00.",
  "archivo_url": "https://miweb.com/facturas/123.pdf",  // opcional
  "nombre_archivo": "factura-123.pdf" // opcional: nombre con el que llega
} ] }
```

Devuelve solo los que estén en estado `pendiente`, como mucho 20 por llamada. Si `archivo_url` es de la propia
web, el bridge la descarga con el mismo `Bearer`; si es de otro dominio, sin token (tiene que ser pública o
firmada).

**`POST /api/whatsapp/salida/{id}`** → resultado de un envío:

```json
{ "estado": "enviado" }
{ "estado": "error", "motivo": "RECHAZADO 429: Daily send cap reached" }
```

Marca el mensaje como `enviado` o `error` (guarda el motivo) para que no vuelva a salir en `/salida`.
Responde 2xx.

### 2.2 · Modelo de datos mínimo

```sql
create table whatsapp_mensajes (
  id text primary key,          -- id de WhatsApp
  chat text not null, nombre_chat text, telefono text, remitente text,
  de_mi boolean not null, texto text, tipo_archivo text, nombre_archivo text,
  fecha timestamptz not null, es_grupo boolean not null default false
);
create index on whatsapp_mensajes (chat, fecha desc);

create table whatsapp_salida (
  id text primary key, destinatario text not null, texto text,
  archivo_url text, nombre_archivo text,
  estado text not null default 'pendiente',   -- pendiente | enviado | error
  motivo text, creado timestamptz not null default now(), actualizado timestamptz
);
```

Las conversaciones del CRM son `whatsapp_mensajes` agrupado por `chat`. Las estadísticas (mensajes por día,
tiempo de respuesta, chats sin contestar…) salen de ahí. Para enviar desde la web, se inserta una fila en
`whatsapp_salida`.

### 2.3 · Implementación de referencia (Next.js App Router + Postgres)

Adáptala a la base de datos que ya use el proyecto (Supabase, Neon, Prisma, Drizzle…). Lo que no se toca es
el contrato.

`lib/whatsapp-auth.ts`

```ts
import { timingSafeEqual } from "node:crypto";

export function bridgeAutorizado(req: Request): boolean {
  const esperado = process.env.WHATSAPP_BRIDGE_TOKEN ?? "";
  const llega = (req.headers.get("authorization") ?? "").replace(/^Bearer /, "");
  if (!esperado || llega.length !== esperado.length) return false;
  return timingSafeEqual(Buffer.from(llega), Buffer.from(esperado));
}
```

`app/api/whatsapp/eventos/route.ts`

```ts
import { bridgeAutorizado } from "@/lib/whatsapp-auth";
import { sql } from "@/lib/db"; // cliente de Postgres del proyecto

export async function POST(req: Request) {
  if (!bridgeAutorizado(req)) return new Response("No autorizado", { status: 401 });
  const { eventos = [] } = await req.json();
  for (const e of eventos) {
    await sql`insert into whatsapp_mensajes
      (id, chat, nombre_chat, telefono, remitente, de_mi, texto, tipo_archivo, nombre_archivo, fecha, es_grupo)
      values (${e.id}, ${e.chat}, ${e.nombre_chat}, ${e.telefono}, ${e.remitente}, ${e.de_mi}, ${e.texto},
              ${e.tipo_archivo ?? null}, ${e.nombre_archivo ?? null}, ${e.fecha}, ${e.es_grupo})
      on conflict (id) do update set texto = excluded.texto, nombre_chat = excluded.nombre_chat`;
  }
  return Response.json({ ok: true, recibidos: eventos.length });
}
```

`app/api/whatsapp/salida/route.ts`

```ts
import { bridgeAutorizado } from "@/lib/whatsapp-auth";
import { sql } from "@/lib/db";

export const dynamic = "force-dynamic";

export async function GET(req: Request) {
  if (!bridgeAutorizado(req)) return new Response("No autorizado", { status: 401 });
  const mensajes = await sql`select id, destinatario, texto, archivo_url, nombre_archivo
    from whatsapp_salida where estado = 'pendiente' order by creado limit 20`;
  return Response.json({ mensajes });
}
```

`app/api/whatsapp/salida/[id]/route.ts`

```ts
import { bridgeAutorizado } from "@/lib/whatsapp-auth";
import { sql } from "@/lib/db";

export async function POST(req: Request, { params }: { params: Promise<{ id: string }> }) {
  if (!bridgeAutorizado(req)) return new Response("No autorizado", { status: 401 });
  const { id } = await params;
  const { estado, motivo } = await req.json();
  await sql`update whatsapp_salida set estado = ${estado === "enviado" ? "enviado" : "error"},
    motivo = ${motivo ?? null}, actualizado = now() where id = ${id}`;
  return Response.json({ ok: true });
}
```

**En otros stacks:**
- **WordPress:** tres rutas con `register_rest_route('whatsapp', ...)` bajo `/wp-json/whatsapp/...`. El
  bridge pide exactamente `/api/whatsapp/...`, así que hace falta una regla de reescritura o una carpeta
  `/api/whatsapp/` con un `index.php` que enrute.
- **Supabase solo:** una Edge Function por ruta, servidas bajo `/api/whatsapp/` desde el dominio de la web.

### 2.4 · Pasos para dejarlo funcionando

1. Crea las tablas y las tres rutas. Respeta el contrato tal cual.
2. Genera el token con `openssl rand -hex 32` y guárdalo como `WHATSAPP_BRIDGE_TOKEN` en las variables de
   entorno de la web (en Vercel: Settings → Environment Variables) y vuelve a desplegar. Nunca lo subas al
   repositorio.
3. Comprueba con curl, contra la web ya publicada, que las tres rutas responden 401 sin token y 200 con él.
4. En el ordenador donde está el bridge, ejecuta (o pídele al usuario que ejecute):

   ```
   "<ruta del programa>" conectar-web --url https://suweb.com --token <TOKEN>
   ```

   El comando comprueba que la web responde y que acepta el token antes de guardar nada. Si dice que la web
   no tiene instalada la conexión, falta alguna ruta o no está desplegada.
5. Verifica:
   - En `whatsapp_mensajes` empieza a entrar el histórico en menos de un minuto.
   - Inserta en `whatsapp_salida` un mensaje para el propio usuario: tiene que pasar a `enviado` en menos de
     10 segundos y llegarle al móvil.

**Para desconectar:** `"<ruta del programa>" conectar-web --quitar`.

## Problemas típicos

| Síntoma | Causa |
|---|---|
| `RECHAZADO 403 … allowlist` | `store/send-allowlist.json` no lleva `"*"` ni ese contacto |
| `RECHAZADO 429` | Se ha llegado al tope diario; se sube en `store/config.json` |
| La salida no se mueve | El bridge está apagado o el ordenador suspendido |
| Llegan eventos repetidos | Es normal tras un corte: por eso el upsert por `id` |
| `conectar-web` da 404 | Las rutas no están desplegadas o no cuelgan de `/api/whatsapp/` |
