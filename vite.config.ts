import path from "node:path";

import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

const API_TARGET = process.env.MANAGER_API_TARGET ?? "http://127.0.0.1:8787";

export default defineConfig({
  root: "src/web",
  plugins: [react()],
  build: {
    outDir: path.resolve(import.meta.dirname, "dist/web"),
    emptyOutDir: true,
    sourcemap: false
  },
  server: {
    // 明确绑 IPv4。
    //
    // Vite 默认绑 localhost，在 Windows 上会解析成 ::1（IPv6）。
    // 那样 Electron 加载 http://127.0.0.1:5173 就会连不上，而且
    // 表面上「端口在监听」，很难看出是地址族的问题。
    host: "127.0.0.1",
    port: 5173,
    strictPort: true,
    proxy: {
      // 必须用正则精确匹配，不能写成 "/api" 这样的前缀。
      //
      // Vite 的代理键是**前缀匹配**：写 "/api" 会把前端自己的模块
      // /api.ts 也代理走。核心对未知路径返回 HTML 兜底，浏览器拿到
      // text/html 却要当 ES 模块执行，于是报
      // 「Expected a JavaScript module script」——模块图直接崩掉，
      // 表现是整个界面一片空白，且看不出跟代理有关。
      "^/api(/|$)": { target: API_TARGET, changeOrigin: true },
      // 网关同理。开发模式下「接入」页显示的 Base URL 是 Vite 的地址，
      // 正好由这条代理转给核心。
      "^/v1(/|$)": { target: API_TARGET, changeOrigin: true }
    }
  }
});
