import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  base: "./",
  plugins: [react()],
  build: {
    outDir: "dist",
    emptyOutDir: true,
    chunkSizeWarningLimit: 520,
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (!id.includes("node_modules")) return undefined;
          if (id.includes("/three/")) return "vendor-three";
          if (id.includes("/lucide-react/")) return "vendor-icons";
          if (id.includes("/react/") || id.includes("/react-dom/")) return "vendor-react";
          return "vendor";
        },
      },
    },
  },
});
