# Desplegar el hub de sync en Google Cloud (Always Free) con HTTPS

Esta guía despliega el **hub de sincronización** de kandel en una VM `e2-micro`
del *Always Free* de Google Cloud, con TLS terminado por **Caddy** (certificado
Let's Encrypt automático). El hub es un proceso aparte del daemon local: no corre
lógica de dominio, solo guarda el change-feed y arbitra por LWW (ver
`internal/hub`).

> **Modelo de seguridad.** El acceso al hub se controla con el `sync_token`: es a
> la vez el *namespace* del usuario y su secreto de acceso. Quien conozca el token
> tiene lectura + escritura de esa partición. Por eso:
> - El token debe ser de **alta entropía** — genéralo con `kandel config gen-token`,
>   nunca lo escribas a mano.
> - El hub **solo** debe exponerse por **HTTPS**. Nunca en HTTP plano a internet:
>   el token viaja en cada petición.
> - Usa el **mismo** token en todas tus máquinas para compartir memorias entre ellas.

---

## 1. Crear la VM Always Free

En Google Cloud, crea una instancia de Compute Engine:

- **Tipo de máquina:** `e2-micro` (elegible para *Always Free*).
- **Región:** una de las gratuitas — `us-west1`, `us-central1` o `us-east1`.
  Fuera de esas regiones la `e2-micro` **no** es gratis.
- **SO:** Debian o Ubuntu LTS.
- **Disco:** el disco de arranque persiste entre reinicios; ahí vive `hub.db`.
  No hace falta disco extra para el tamaño de un hub personal (SQLite de KB a MB).

Reserva una **IP externa estática** y apunta un **dominio** (registro `A`) a esa
IP. Caddy necesita un dominio para emitir el certificado.

## 2. Abrir solo los puertos de TLS/ACME

En el firewall de GCP (reglas de ingreso), abre únicamente:

- **443/tcp** (HTTPS).
- **80/tcp** (validación ACME de Let's Encrypt).

**No abras el 8766** (el puerto del hub). El hub escuchará en *loopback* y solo
Caddy —en la misma máquina— le hablará.

## 3. Instalar el binario del hub

Copia el binario `kandel` a la VM (compílalo con `CGO_ENABLED=0` para un binario
estático) y colócalo en `/usr/local/bin/kandel`.

Configura el hub para escuchar **solo en loopback**:

```bash
sudo -u kandel kandel config set sync_hub_addr 127.0.0.1:8766
```

Con eso el hub no es accesible directamente desde internet, solo a través de Caddy.

> El rate limit por IP del hub está activo por defecto (120 req/min por IP). Se
> ajusta con `sync_hub_rate` (0 lo desactiva). Detrás de Caddy, el hub identifica
> la IP real del cliente por la cabecera `X-Forwarded-For` que pone el proxy.

## 4. Correr el hub como servicio (systemd)

Crea un usuario de servicio y define el directorio de datos (donde vivirá
`hub.db`):

```bash
sudo useradd --system --home /var/lib/kandel --create-home kandel
```

`/etc/systemd/system/kandel-hub.service`:

```ini
[Unit]
Description=kandel sync hub
After=network-online.target
Wants=network-online.target

[Service]
User=kandel
Environment=KANDEL_DATA_DIR=/var/lib/kandel
ExecStart=/usr/local/bin/kandel serve --sync-hub
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
```

`KANDEL_DATA_DIR` fija dónde se abre `hub.db` (aquí `/var/lib/kandel/hub.db`), en
el disco persistente de la VM.

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now kandel-hub
sudo systemctl status kandel-hub    # debe estar "active (running)"
```

## 5. Caddy: HTTPS automático como reverse proxy

Instala Caddy ([caddyserver.com/docs/install](https://caddyserver.com/docs/install)).

`/etc/caddy/Caddyfile`:

```caddy
hub.tudominio.com {
    reverse_proxy 127.0.0.1:8766
}
```

```bash
sudo systemctl reload caddy
```

Caddy obtiene y renueva el certificado de Let's Encrypt solo, termina el TLS en
`:443` y reenvía en claro a `127.0.0.1:8766` (loopback, en la misma máquina). Pone
`X-Forwarded-For` con la IP real del cliente, que el rate limit del hub usa.

## 6. Configurar los clientes (tus máquinas)

En cada máquina desde la que sincronizas (laptop, escritorio):

```bash
kandel config set sync_url https://hub.tudominio.com
kandel config set sync_token <EL-MISMO-TOKEN-EN-TODAS>
# opcional: activar los disparadores automáticos (push al cerrar, pull al iniciar)
kandel config set sync_auto true
```

Genera el token **una vez** con `kandel config gen-token` y copia esa misma cadena
al resto de máquinas. Verifica:

```bash
kandel sync            # debe reportar subidas/bajadas, no un error de conexión
```

---

## Comprobaciones rápidas

- **HTTPS vivo:** `curl -i https://hub.tudominio.com/memory/<token>/changes?since=0`
  con `Authorization: Bearer <token>` debe responder `200` y un JSON de cambios.
- **Sin token → 401:** la misma URL sin el Bearer correcto debe dar `401`.
- **Puerto del hub cerrado:** desde fuera, `:8766` no debe responder (solo `:443`).
- **Persistencia:** reinicia la VM; `hub.db` y las memorias del hub deben seguir ahí.

## Notas

- **Solo se replican memorias.** Sesiones y prompts no viajan al hub (por diseño).
- El hub arbitra por **LWW** sobre `updated_at`: reenviar filas iguales o más
  viejas no hace daño (idempotente).
- El rate limit es **defensa en profundidad**, no la seguridad primaria: esa es la
  entropía del token. No bajes la guardia con tokens débiles por tenerlo activo.
