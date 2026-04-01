# Deno Relay Scripts

This directory contains Deno relay scripts for providers that need an extra forwarding hop.

## Codex Relay

`codex-proxy.ts` forwards official Codex upstream requests:

- `/codex/*` -> `https://chatgpt.com/backend-api/codex/*`

The script preserves `/` and `/robots.txt` health endpoints and handles both transports on the same `/codex/*` route:

- normal HTTP requests -> HTTP/SSE relay
- `Upgrade: websocket` requests -> websocket relay

The relay forwards Codex request headers, drops hop-by-hop handshake headers, and rewrites the target host to the official Codex upstream.

## Deploy

### Deno Deploy Platform

Use the current Deno Deploy platform at `console.deno.com` as the primary deployment target.

Recommended setup:

1. Create a project in `console.deno.com`.
2. Connect the GitHub repository, or upload the script manually.
3. Set the entrypoint to `deno/codex-proxy.ts`.
4. Deploy and record the project URL, for example `https://your-project.deno.net`.

Why this script is written this way:

- It uses `Deno.serve(...)`, which is the recommended HTTP server API for the current platform.
- It avoids the old `std/http/server.ts` import so the script stays closer to the runtime API expected by the newer Deploy environment.
- The upstream websocket is created through an explicitly typed `WebSocketOptions` object. This helps `console.deno.com` resolve the constructor overload correctly when custom headers are used.

If you edit the file directly in the Deploy console and still see a red type underline around the websocket constructor, treat the repository copy as the source of truth and prefer deploying from GitHub. The console editor's inline type analysis can lag behind the actual runtime API surface.

### Deno Deploy Classic

Deploy Classic can still run simple HTTP relay scripts, but it is being sunset on `2026-07-20`. Avoid using it for new deployments and plan to migrate existing relays to the current platform.

### Local run

```bash
cd deno
deno task dev
```

### Local checks

```bash
cd deno
deno task check
deno task fmt
deno task lint
```

## CLIProxyAPI Configuration

### `codex-api-key`

```yaml
codex-api-key:
  - api-key: "sk-..."
    base-url: "https://chatgpt.com/backend-api/codex"
    deno-proxy-host: "https://your-project.deno.net"
    websockets: true
```

### Codex OAuth auth-file

```json
{
  "type": "codex",
  "email": "user@example.com",
  "deno_proxy_host": "https://your-project.deno.net",
  "websockets": true
}
```

When `deno-proxy-host` or `deno_proxy_host` is present, CLIProxyAPI rewrites official Codex traffic from:

- `https://chatgpt.com/backend-api/codex/responses`
- `wss://chatgpt.com/backend-api/codex/responses`

to:

- `https://your-project.deno.net/codex/responses`
- `wss://your-project.deno.net/codex/responses`

Whether websocket relay is used is still decided by CLIProxyAPI's `websockets` setting:

- `websockets: false` -> CPA sends HTTP/SSE requests to `/codex/*`
- `websockets: true` -> CPA sends websocket upgrade requests to `/codex/*`
- if the Deno websocket handshake fails, CPA automatically falls back to HTTP/SSE

## Notes

- This relay only supports the official Codex upstream. It is not intended for arbitrary custom Codex base URLs.
- Deno Deploy Classic is scheduled to sunset on 2026-07-20. Use the current Deploy platform for new projects.
- Quick health checks after deployment:
  - `GET /` -> `Codex Deno proxy is running!`
  - `GET /robots.txt` -> `User-agent: *`
  - `POST /codex/responses` -> should forward to the official Codex upstream
- If the root path returns unrelated content such as another proxy's landing page, that deployment is not serving this script yet.
