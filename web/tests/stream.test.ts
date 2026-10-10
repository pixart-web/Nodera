import { afterEach, describe, expect, it, vi } from "vitest";
import { streamSSE } from "@/lib/stream";

function sseResponse(chunks: string[]): Response {
  const enc = new TextEncoder();
  const body = new ReadableStream({ start(c) { for (const ch of chunks) c.enqueue(enc.encode(ch)); c.close(); } });
  return new Response(body, { status: 200, headers: { "Content-Type": "text/event-stream" } });
}

afterEach(() => vi.unstubAllGlobals());

describe("streamSSE", () => {
  it("parses events split across chunks and ignores keep-alive comments", async () => {
    vi.stubGlobal("window", { localStorage: { getItem: () => null } });
    vi.stubGlobal("document", { cookie: "" });
    vi.stubGlobal("fetch", vi.fn(async () => sseResponse([": keep-alive\n\nid: 7\nevent: lo", 'g\ndata: {"message":"hi"}\n\n', 'event: end\ndata: {"status":"succeeded"}\n\n'])));
    const got: Array<{ event: string; id?: string; data: unknown }> = [];
    await streamSSE("/x", (e) => got.push(e), new AbortController().signal);
    expect(got).toEqual([
      { event: "log", id: "7", data: { message: "hi" } },
      { event: "end", id: undefined, data: { status: "succeeded" } },
    ]);
  });
  it("skips malformed frames instead of throwing", async () => {
    vi.stubGlobal("window", { localStorage: { getItem: () => null } });
    vi.stubGlobal("document", { cookie: "" });
    vi.stubGlobal("fetch", vi.fn(async () => sseResponse(["event: log\ndata: {not json\n\n", 'event: log\ndata: {"ok":true}\n\n'])));
    const got: unknown[] = [];
    await streamSSE("/x", (e) => got.push(e.data), new AbortController().signal);
    expect(got).toEqual([{ ok: true }]);
  });
  it("rejects on a non-OK response", async () => {
    vi.stubGlobal("window", { localStorage: { getItem: () => null } });
    vi.stubGlobal("document", { cookie: "" });
    vi.stubGlobal("fetch", vi.fn(async () => new Response("no", { status: 401 })));
    await expect(streamSSE("/x", () => {}, new AbortController().signal)).rejects.toThrow(/401/);
  });
});
