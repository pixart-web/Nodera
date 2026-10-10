import { getCSRFToken, getCurrentOrgId } from "./session";

const API_BASE = process.env.NEXT_PUBLIC_NODERA_API_URL ?? "http://localhost:8080";

export interface SSEEvent { event: string; data: unknown; id?: string }

// streamSSE opens a server-sent-events stream with fetch (EventSource cannot
// send the X-Nodera-Org header the API requires). It resolves when the server
// ends the stream or the signal aborts, and reconnects are left to the caller.
export async function streamSSE(path: string, onEvent: (e: SSEEvent) => void, signal: AbortSignal, lastEventId?: string): Promise<void> {
  const headers: Record<string, string> = { Accept: "text/event-stream" };
  const org = getCurrentOrgId();
  if (org) headers["X-Nodera-Org"] = org;
  const csrf = getCSRFToken();
  if (csrf) headers["X-CSRF-Token"] = csrf;
  if (lastEventId) headers["Last-Event-ID"] = lastEventId;
  const res = await fetch(`${API_BASE}${path}`, { headers, credentials: "include", signal });
  if (!res.ok || !res.body) throw new Error(`stream failed (${res.status})`);
  const reader = res.body.getReader();
  const dec = new TextDecoder();
  let buf = "";
  for (;;) {
    const { done, value } = await reader.read();
    if (done) return;
    buf += dec.decode(value, { stream: true });
    let i: number;
    while ((i = buf.indexOf("\n\n")) >= 0) {
      const block = buf.slice(0, i);
      buf = buf.slice(i + 2);
      let event = "message", id: string | undefined;
      const data: string[] = [];
      for (const line of block.split("\n")) {
        if (line.startsWith(":")) continue;
        if (line.startsWith("event:")) event = line.slice(6).trim();
        else if (line.startsWith("id:")) id = line.slice(3).trim();
        else if (line.startsWith("data:")) data.push(line.slice(5).trim());
      }
      if (!data.length) continue;
      try { onEvent({ event, id, data: JSON.parse(data.join("\n")) }); } catch { /* ignore a malformed frame */ }
    }
  }
}
