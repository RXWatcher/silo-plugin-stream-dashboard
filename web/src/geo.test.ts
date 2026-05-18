import { describe, expect, it } from "vitest";
import { hasCoords } from "./geo";

describe("hasCoords", () => {
  it("requires both latitude and longitude", () => {
    expect(hasCoords({ lat: 52.37, lon: 4.9 })).toBe(true);
    expect(hasCoords({ lat: 52.37 })).toBe(false);
    expect(hasCoords({ lon: 4.9 })).toBe(false);
    expect(hasCoords({})).toBe(false);
  });
});
