const CODEX_PREFIX = "/codex";
const CODEX_UPSTREAM = "https://chatgpt.com/backend-api/codex";
const DEFAULT_USER_AGENT =
  "codex_cli_rs/0.116.0 (Mac OS 26.0.1; arm64) Apple_Terminal/464";
const WEBSOCKET_OPEN_TIMEOUT_MS = 15_000;

const HOP_BY_HOP_HEADERS = new Set([
  "connection",
  "content-encoding",
  "content-length",
  "keep-alive",
  "proxy-authenticate",
  "proxy-authorization",
  "te",
  "trailer",
  "transfer-encoding",
  "upgrade",
]);

Deno.serve(async (request) => {
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
  const targetHttpUrl = `${CODEX_UPSTREAM}${suffix}${url.search}`;

  try {
    if (isWebSocketUpgrade(request)) {
      return await handleWebSocketRelay(request, targetHttpUrl);
    }
    return await handleHttpRelay(request, targetHttpUrl);
  } catch (error) {
    console.error("Codex proxy error:", error);
    return jsonError(500, "Internal Server Error");
  }
});

async function handleHttpRelay(
  request: Request,
  targetUrl: string,
): Promise<Response> {
  const upstreamHeaders = buildUpstreamHeaders(request, targetUrl, false);
  const body = bodyAllowed(request.method) ? request.body : undefined;

  const response = await fetch(targetUrl, {
    method: request.method,
    headers: upstreamHeaders,
    body,
    redirect: "manual",
    ...(body ? { duplex: "half" as const } : {}),
  });

  return buildProxyResponse(response);
}

async function handleWebSocketRelay(
  request: Request,
  targetHttpUrl: string,
): Promise<Response> {
  const targetWsUrl = toWebSocketUrl(targetHttpUrl);
  const upstreamHeaders = buildUpstreamHeaders(request, targetHttpUrl, true);
  const upstreamSocket = openUpstreamWebSocket(targetWsUrl, upstreamHeaders);
  upstreamSocket.binaryType = "arraybuffer";

  const openResult = await waitForWebSocketOpen(upstreamSocket, targetWsUrl);
  if (!openResult.ok) {
    safeCloseSocket(upstreamSocket, 1011, openResult.message);
    return jsonError(openResult.status, openResult.message);
  }

  let downstreamSocket: WebSocket;
  let response: Response;
  try {
    const upgraded = Deno.upgradeWebSocket(request);
    downstreamSocket = upgraded.socket;
    response = upgraded.response;
  } catch (error) {
    safeCloseSocket(upstreamSocket, 1011, "Failed to upgrade downstream websocket");
    throw error;
  }

  downstreamSocket.binaryType = "arraybuffer";
  wireWebSocketRelay(downstreamSocket, upstreamSocket);
  return response;
}

function openUpstreamWebSocket(
  targetWsUrl: string,
  upstreamHeaders: Headers,
): WebSocket {
  // Keep the options object explicitly typed so Deploy console type-checking
  // resolves the second constructor argument as WebSocketOptions instead of
  // falling back to the legacy `protocols: string | string[]` overload.
  const options: WebSocketOptions = {
    headers: upstreamHeaders,
  };
  return new WebSocket(targetWsUrl, options);
}

function buildUpstreamHeaders(
  request: Request,
  targetUrl: string,
  websocket: boolean,
): Headers {
  const headers = new Headers();

  for (const [key, value] of request.headers.entries()) {
    const lower = key.toLowerCase();
    if (shouldSkipRequestHeader(lower)) {
      continue;
    }
    headers.set(key, value);
  }

  if (!websocket) {
    headers.set("Host", new URL(targetUrl).host);
  }
  if (!headers.has("User-Agent") && !headers.has("user-agent")) {
    headers.set("User-Agent", DEFAULT_USER_AGENT);
  }

  return headers;
}

function shouldSkipRequestHeader(lower: string): boolean {
  if (lower === "host" || HOP_BY_HOP_HEADERS.has(lower)) {
    return true;
  }
  return lower.startsWith("sec-websocket-");
}

function buildProxyResponse(response: Response): Response {
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
}

function wireWebSocketRelay(
  downstreamSocket: WebSocket,
  upstreamSocket: WebSocket,
) {
  downstreamSocket.addEventListener("message", (event) => {
    void relayWebSocketMessage(
      "downstream->upstream",
      event.data,
      upstreamSocket,
      downstreamSocket,
    );
  });
  upstreamSocket.addEventListener("message", (event) => {
    void relayWebSocketMessage(
      "upstream->downstream",
      event.data,
      downstreamSocket,
      upstreamSocket,
    );
  });

  downstreamSocket.addEventListener("close", (event) => {
    safeCloseSocket(
      upstreamSocket,
      sanitizeCloseCode(event.code),
      event.reason,
    );
  });
  upstreamSocket.addEventListener("close", (event) => {
    safeCloseSocket(
      downstreamSocket,
      sanitizeCloseCode(event.code),
      event.reason,
    );
  });

  downstreamSocket.addEventListener("error", () => {
    safeCloseSocket(upstreamSocket, 1011, "Downstream websocket error");
  });
  upstreamSocket.addEventListener("error", () => {
    safeCloseSocket(downstreamSocket, 1011, "Upstream websocket error");
  });
}

async function relayWebSocketMessage(
  direction: string,
  data: Blob | ArrayBuffer | ArrayBufferView | string,
  targetSocket: WebSocket,
  peerSocket: WebSocket,
) {
  if (targetSocket.readyState !== WebSocket.OPEN) {
    return;
  }

  try {
    targetSocket.send(await normalizeWebSocketData(data));
  } catch (error) {
    console.error(`Codex websocket relay send error (${direction}):`, error);
    safeCloseSocket(targetSocket, 1011, "Relay send failure");
    safeCloseSocket(peerSocket, 1011, "Relay send failure");
  }
}

async function normalizeWebSocketData(
  data: Blob | ArrayBuffer | ArrayBufferView | string,
): Promise<ArrayBuffer | ArrayBufferView | string> {
  if (typeof data === "string") {
    return data;
  }
  if (data instanceof Blob) {
    return await data.arrayBuffer();
  }
  return data;
}

async function waitForWebSocketOpen(
  socket: WebSocket,
  targetUrl: string,
): Promise<{ ok: true } | { ok: false; status: number; message: string }> {
  return await new Promise((resolve) => {
    let settled = false;
    const timeout = setTimeout(() => {
      settle({
        ok: false,
        status: 504,
        message: `Timed out connecting to upstream websocket: ${targetUrl}`,
      });
    }, WEBSOCKET_OPEN_TIMEOUT_MS);

    const cleanup = () => {
      clearTimeout(timeout);
      socket.removeEventListener("open", onOpen);
      socket.removeEventListener("error", onError);
      socket.removeEventListener("close", onClose);
    };

    const settle = (
      result: { ok: true } | { ok: false; status: number; message: string },
    ) => {
      if (settled) {
        return;
      }
      settled = true;
      cleanup();
      resolve(result);
    };

    const onOpen = () => settle({ ok: true });
    const onError = () =>
      settle({
        ok: false,
        status: 502,
        message: `Failed to connect to upstream websocket: ${targetUrl}`,
      });
    const onClose = (event: CloseEvent) =>
      settle({
        ok: false,
        status: 502,
        message: formatCloseMessage(
          "Upstream websocket closed before relay was ready",
          event,
        ),
      });

    socket.addEventListener("open", onOpen);
    socket.addEventListener("error", onError);
    socket.addEventListener("close", onClose);
  });
}

function formatCloseMessage(prefix: string, event: CloseEvent): string {
  const reason = event.reason ? `: ${event.reason}` : "";
  return `${prefix} (code=${event.code}${reason})`;
}

function safeCloseSocket(socket: WebSocket, code: number, reason: string) {
  if (
    socket.readyState !== WebSocket.OPEN &&
    socket.readyState !== WebSocket.CONNECTING
  ) {
    return;
  }

  try {
    socket.close(sanitizeCloseCode(code), sanitizeCloseReason(reason));
  } catch (error) {
    console.error("Codex websocket relay close error:", error);
  }
}

function sanitizeCloseCode(code: number): number {
  if (!Number.isInteger(code) || code < 1000 || code >= 5000) {
    return 1011;
  }
  if (code === 1005 || code === 1006 || code === 1015) {
    return 1011;
  }
  return code;
}

function sanitizeCloseReason(reason: string): string {
  const normalized = String(reason ?? "").trim();
  if (!normalized) {
    return "";
  }
  return normalized.slice(0, 120);
}

function isWebSocketUpgrade(request: Request): boolean {
  return request.headers.get("upgrade")?.toLowerCase() === "websocket";
}

function bodyAllowed(method: string): boolean {
  const normalized = method.toUpperCase();
  return normalized !== "GET" && normalized !== "HEAD";
}

function toWebSocketUrl(httpUrl: string): string {
  const target = new URL(httpUrl);
  target.protocol = target.protocol === "https:" ? "wss:" : "ws:";
  return target.toString();
}

function jsonError(status: number, message: string): Response {
  return new Response(JSON.stringify({ error: message }), {
    status,
    headers: { "Content-Type": "application/json; charset=utf-8" },
  });
}
