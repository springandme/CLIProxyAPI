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

### Deno Deploy

1. Create a new Deno Deploy project.
2. Upload `codex-proxy.ts`.
3. Deploy and record the project URL, for example `https://your-project.deno.dev`.

### Local run

```bash
deno run --allow-net codex-proxy.ts
```

## CLIProxyAPI Configuration

### `codex-api-key`

```yaml
codex-api-key:
  - api-key: "sk-..."
    base-url: "https://chatgpt.com/backend-api/codex"
    deno-proxy-host: "https://your-project.deno.dev"
    websockets: true
```

### Codex OAuth auth-file

```json
{
  "type": "codex",
  "email": "user@example.com",
  "deno_proxy_host": "https://your-project.deno.dev",
  "websockets": true
}
```

When `deno-proxy-host` or `deno_proxy_host` is present, CLIProxyAPI rewrites official Codex traffic from:

- `https://chatgpt.com/backend-api/codex/responses`
- `wss://chatgpt.com/backend-api/codex/responses`

to:

- `https://your-project.deno.dev/codex/responses`
- `wss://your-project.deno.dev/codex/responses`

Whether websocket relay is used is still decided by CLIProxyAPI's `websockets` setting:

- `websockets: false` -> CPA sends HTTP/SSE requests to `/codex/*`
- `websockets: true` -> CPA sends websocket upgrade requests to `/codex/*`
- if the Deno websocket handshake fails, CPA automatically falls back to HTTP/SSE

## Notes

- This relay only supports the official Codex upstream. It is not intended for arbitrary custom Codex base URLs.
- Deno Deploy Classic is scheduled to sunset on 2026-07-20. If you still deploy there, plan a later migration to the current Deno Deploy platform.
