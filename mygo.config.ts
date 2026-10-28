import { defineConfig } from "mygo-cli";
import pkg from "./package.json" with { type: "json" };

export default defineConfig({
  name: "KunpengClash",
  identifier: "com.kunpeng.clash",
  version: pkg.version,
  devUrl: "http://localhost:5173",
  devCommand: "npm run dev:web",
  buildCommand: "npm run build:web",
  frontendDist: "dist",
  bindings: "src/mygo.ts",
  out: "build",
});
