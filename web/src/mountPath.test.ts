import { describe, expect, it } from "vitest";
import { extractMountPath } from "./mountPath";

describe("extractMountPath", () => {
  it("returns an empty mount path on local routes", () => {
    expect(extractMountPath("/dashboard")).toBe("");
  });

  it("supports numeric and slug plugin installation ids", () => {
    expect(extractMountPath("/api/v1/plugins/42/dashboard")).toBe("/api/v1/plugins/42");
    expect(extractMountPath("/api/v1/plugins/stream-dashboard/dashboard")).toBe("/api/v1/plugins/stream-dashboard");
  });
});
