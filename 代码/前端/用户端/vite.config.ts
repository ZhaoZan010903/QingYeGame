import { defineConfig, loadEnv } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, '.', '');
  const proxy = {
    '/api': { target: env.QINGYE_API_PROXY || 'http://127.0.0.1:8080', changeOrigin: true, ws: true },
  };
  return {
    plugins: [react(), tailwindcss()],
    server: { port: Number(env.QINGYE_WEB_PORT) || 5173, proxy },
    preview: { port: Number(env.QINGYE_WEB_PORT) || 4173, proxy },
    build: { sourcemap: false },
  };
});
