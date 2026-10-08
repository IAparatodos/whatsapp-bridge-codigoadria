# WhatsApp de Código AdrIA

Conecta tu WhatsApp con Claude y con tus apps. Se instala en tu ordenador, se vincula con un QR (como WhatsApp
Web) y a partir de ahí:

- **Le hablas a Claude:** «mándale a Juan el presupuesto en PDF», «¿qué me ha dicho Ana esta semana?».
- **Tu web o tu CRM recibe tus WhatsApp** para conversaciones y estadísticas, y **envía mensajes** desde tu
  número: confirmaciones, recordatorios, facturas.
- **Claude Code lo instala solo en la web que quieras:** trae una skill con todo lo necesario.

Funciona en **Windows y Mac**, sin instalar nada más.

## Instalación

**Mac** — abre la app Terminal, pega esto y pulsa Enter:

```
curl -fsSL https://raw.githubusercontent.com/IAparatodos/whatsapp-bridge-codigoadria/main/install-mac.sh | bash
```

**Windows** — abre PowerShell, pega esto y pulsa Enter:

```
irm https://raw.githubusercontent.com/IAparatodos/whatsapp-bridge-codigoadria/main/install-windows.ps1 | iex
```

Se abrirá una página con un código QR. En el móvil: **WhatsApp → Ajustes → Dispositivos vinculados → Vincular
un dispositivo**, y escanéalo. Listo: arranca solo cada vez que enciendes el ordenador.

El instalador también conecta el WhatsApp con **Claude Code** y **Claude Desktop** (si los tienes) e
instala la skill `whatsapp-codigoadria`. Para actualizar, vuelve a ejecutar la misma línea: no hace falta
escanear otra vez.

## Uso

**Desde Claude:** pídeselo con tus palabras. Antes de enviar, Claude te confirma a quién y qué.

**Desde una web o app:** pídele a Claude Code «conecta mi WhatsApp a mi web». La skill le explica cómo
montarlo; el contrato completo está en [`skill/whatsapp-codigoadria/SKILL.md`](skill/whatsapp-codigoadria/SKILL.md).
Al final se enlaza con:

```
whatsapp-bridge conectar-web --url https://tuweb.com --token <clave>
```

**Desde la línea de comandos:**

```
whatsapp-bridge send --a 600123456 --texto "Hola" --aprobado-por "tu nombre"
whatsapp-bridge send --a 600123456 --archivo factura.pdf --texto "Aquí la tienes" --aprobado-por "tu nombre"
```

Las imágenes (jpg, png, gif, webp) se envían como foto, los vídeos (mp4, mov, avi) como vídeo, el ogg como
audio y el resto (pdf, docx, xlsx…) como documento con su nombre.

## Seguridad

- Tu sesión de WhatsApp y tus mensajes **se quedan en tu ordenador**, en la carpeta `store/`. No se suben a
  ningún sitio salvo a la web que tú conectes.
- El programa solo acepta órdenes **desde tu propio ordenador** (`127.0.0.1`): nadie puede usarlo desde
  internet.
- Cada envío necesita un responsable (`--aprobado-por`) y queda registrado en `store/send-log.jsonl`.
- **Tope diario** de envíos: 25 por defecto. Se cambia en `store/config.json`:

  ```json
  { "tope_diario": 100 }
  ```

- **A quién se puede escribir:** `store/send-allowlist.json`. La instalación pone `["*"]` (cualquier
  contacto); se puede limitar a una lista de números.
- WhatsApp banea los números que mandan mensajes masivos o en frío. Úsalo para hablar con quien te espera.

## Configuración avanzada

| Qué | Cómo |
|---|---|
| Otro puerto (si el 8080 está ocupado) | Variable de entorno `WA_BRIDGE_PORT` |
| Desconectar la web | `whatsapp-bridge conectar-web --quitar` |
| Volver a vincular (otro móvil) | Borra `store/whatsapp.db` y vuelve a arrancar |

---

Basado en [lharries/whatsapp-mcp](https://github.com/lharries/whatsapp-mcp) (licencia MIT; README original en
[README-original-lharries.md](README-original-lharries.md)). No es un producto oficial de WhatsApp ni de
Meta: usa el protocolo de dispositivos vinculados, como WhatsApp Web.
