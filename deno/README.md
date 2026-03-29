# Deno Relay Scripts

This directory contains Deno relay scripts for providers that need an extra forwarding hop.

## Codex Relay

`codex-proxy.ts` forwards official Codex upstream requests:

- `/codex/*` -> `https://chatgpt.com/backend-api/codex/*`

The script preserves `/` and `/robots.txt` health endpoints and forwards the required Codex headers while dropping hop-by-hop headers.

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
```

### Codex OAuth auth-file

```json
{
  "type": "codex",
  "email": "user@example.com",
  "deno_proxy_host": "https://your-project.deno.dev"
}
```

When `deno-proxy-host` or `deno_proxy_host` is present, CLIProxyAPI rewrites official Codex requests from:

- `https://chatgpt.com/backend-api/codex/responses`

to:

- `https://your-project.deno.dev/codex/responses`

## Notes

- Deno relay mode is HTTP/SSE only. Codex websocket transport is intentionally disabled when a Deno relay host is configured.
- This relay only supports the official Codex upstream. It is not intended for arbitrary custom Codex base URLs.
