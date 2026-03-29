import { serve } from "https://deno.land/std/http/server.ts";

const CODEX_PREFIX = "/codex";
const CODEX_UPSTREAM = "https://chatgpt.com/backend-api/codex";

const FORWARD_REQUEST_HEADERS = new Set([
  "accept",
  "accept-encoding",
  "accept-language",
  "authorization",
  "content-type",
  "cookie",
  "origin",
  "originator",
  "referer",
  "session_id",
  "user-agent",
  "version",
  "x-client-request-id",
  "x-codex-turn-metadata",
  "x-codex-turn-state",
  "x-openai-client-id",
  "x-openai-client-version",
  "x-responsesapi-include-timing-metrics",
]);

const HOP_BY_HOP_HEADERS = new Set([
  "connection",
  "content-length",
  "content-encoding",
  "keep-alive",
  "proxy-authenticate",
  "proxy-authorization",
  "te",
  "trailer",
  "transfer-encoding",
  "upgrade",
]);

const DEFAULT_USER_AGENT =
  "codex_cli_rs/0.116.0 (Mac OS 26.0.1; arm64) Apple_Terminal/464";

serve(async (request) => {
  const url = new URL(request.url);
  const pathname = url.pathname;

  if (pathname === "/" || pathname === "/index.html") {
    return new Response("Codex Deno proxy is running!", {
      status: 200,
      headers: { "Content-Type": "text/plain; charset=utf-8" },
    });
  }

  if (pathname === "/robots.txt") {
    return new Response("User-agent: *\nDisallow: /", {
      status: 200,
      headers: { "Content-Type": "text/plain; charset=utf-8" },
    });
  }

  if (!pathname.startsWith(CODEX_PREFIX)) {
    return new Response("Not Found", { status: 404 });
  }

  const suffix = pathname.slice(CODEX_PREFIX.length) || "/";
  const targetUrl = `${CODEX_UPSTREAM}${suffix}${url.search}`;

  try {
    const upstreamHeaders = new Headers();
    for (const [key, value] of request.headers.entries()) {
      const lower = key.toLowerCase();
      if (FORWARD_REQUEST_HEADERS.has(lower) && !HOP_BY_HOP_HEADERS.has(lower)) {
        upstreamHeaders.set(key, value);
      }
    }

    upstreamHeaders.set("Host", new URL(CODEX_UPSTREAM).host);
    if (!upstreamHeaders.has("User-Agent") && !upstreamHeaders.has("user-agent")) {
      upstreamHeaders.set("User-Agent", DEFAULT_USER_AGENT);
    }

    const response = await fetch(targetUrl, {
      method: request.method,
      headers: upstreamHeaders,
      body: request.body,
      redirect: "manual",
    });

    const responseHeaders = new Headers();
    for (const [key, value] of response.headers.entries()) {
      const lower = key.toLowerCase();
      if (HOP_BY_HOP_HEADERS.has(lower)) {
        continue;
      }
      responseHeaders.set(key, value);
    }

    responseHeaders.set("X-Content-Type-Options", "nosniff");
    responseHeaders.set("X-Frame-Options", "DENY");
    responseHeaders.set("Referrer-Policy", "no-referrer");

    return new Response(response.body, {
      status: response.status,
      headers: responseHeaders,
    });
  } catch (error) {
    console.error("Codex proxy error:", error);
    return new Response(JSON.stringify({ error: "Internal Server Error" }), {
      status: 500,
      headers: { "Content-Type": "application/json; charset=utf-8" },
    });
  }
});
