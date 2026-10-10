import { describe, expect, it } from "vitest";
import { ago, bytes, colorFor } from "@/lib/format";
import { toStatus } from "@/lib/status";

describe("format", () => {
  it("renders relative times and tolerates missing values", () => {
    expect(ago(null)).toBe("—");
    expect(ago("not a date")).toBe("—");
    expect(ago(new Date(Date.now() - 5 * 60_000).toISOString())).toBe("há 5 min");
    expect(ago(new Date().toISOString())).toBe("agora mesmo");
  });
  it("formats byte sizes", () => {
    expect(bytes(null)).toBe("—");
    expect(bytes(512)).toBe("512 B");
    expect(bytes(1536)).toBe("1.5 KB");
    expect(bytes(5 * 1024 * 1024)).toBe("5.0 MB");
  });
  it("gives a stable colour per name", () => {
    expect(colorFor("Acme")).toBe(colorFor("Acme"));
  });
});

describe("status mapping", () => {
  it("maps every API vocabulary onto the visual scale", () => {
    expect(toStatus("active").status).toBe("ONLINE");
    expect(toStatus("provisioning").status).toBe("PENDING");
    expect(toStatus("failing").status).toBe("ERROR");
    expect(toStatus("expiring").status).toBe("WARNING");
    expect(toStatus("rolled_back").status).toBe("WARNING");
    expect(toStatus("ready_for_cutover").label).toBe("Pronto para cutover");
  });
  it("never claims health for an unknown value", () => {
    expect(toStatus("something-new").status).toBe("UNKNOWN");
    expect(toStatus(undefined).status).toBe("UNKNOWN");
  });
});
