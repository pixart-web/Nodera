import { describe, expect, it } from "vitest";
import { brand } from "@/lib/brand";

describe("brand", () => {
  it("exposes the commercial name while technical identifiers stay Nodera", () => {
    expect(brand.name).toBe("Servon");
    expect(JSON.stringify(brand)).not.toMatch(/nodera/i);
  });
});
