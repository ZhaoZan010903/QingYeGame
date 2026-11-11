import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, ".", "");
  const proxy = {
    "/api": {
      target: env.QINGYE_ADMIN_API_PROXY || "http://127.0.0.1:5182",
      changeOrigin: true,
      ws: true,
    },
  };
  return {
    plugins: [react()],
    server: { port: Number(env.QINGYE_ADMIN_PORT) || 5181, proxy },
    preview: { port: Number(env.QINGYE_ADMIN_PORT) || 5181, proxy },
    build: { sourcemap: false },
  };
});
