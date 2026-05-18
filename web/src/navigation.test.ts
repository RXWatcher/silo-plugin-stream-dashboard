import { describe, expect, it } from "vitest";
import { adminBackTarget } from "./navigation";

describe("adminBackTarget", () => {
  it("returns the plugin admin list for admin routes", () => {
    expect(adminBackTarget("/api/v1/plugins/37/admin").href).toBe("/admin/plugins");
    expect(adminBackTarget("/api/v1/plugins/37/admin/settings").label).toBe("Plugins");
  });

  it("returns the main app for user dashboard routes", () => {
    expect(adminBackTarget("/api/v1/plugins/37/dashboard").href).toBe("/");
    expect(adminBackTarget("/dashboard").label).toBe("Continuum");
  });
});
