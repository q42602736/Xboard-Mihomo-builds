#!/usr/bin/env node

import { spawnSync } from "node:child_process";
import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";

const [sourceDirArg] = process.argv.slice(2);

if (!sourceDirArg) {
  console.error("用法：node apply-nexgen-icon.mjs <NexGen源码目录>");
  process.exit(1);
}

const sourceDir = path.resolve(sourceDirArg);
const iconsDir = path.join(sourceDir, "src-tauri", "icons");
const iconStampPath = path.join(iconsDir, ".nexgen-icon-source");
const appIconSource = resolveAppIconSource();

if (!appIconSource) {
  console.log("未配置 NexGen App 图标源，使用源码默认图标。");
  process.exit(0);
}

if (!fs.existsSync(appIconSource)) {
  throw new Error(`NexGen App 图标源不存在：${appIconSource}`);
}

if (isPathInside(appIconSource, iconsDir)) {
  console.log("NexGen App 图标源已在 Tauri icons 目录，跳过图标生成。");
  process.exit(0);
}

const stampValue = `${hashFile(appIconSource)} ${appIconSource}`;
if (fs.existsSync(iconStampPath) && fs.readFileSync(iconStampPath, "utf8").trim() === stampValue) {
  console.log("NexGen Tauri 图标已是最新。");
  process.exit(0);
}

console.log("根据 nexgen.config.yaml 生成 NexGen Tauri 图标...");
run("npm", ["run", "tauri", "--", "icon", appIconSource]);
fs.mkdirSync(iconsDir, { recursive: true });
fs.writeFileSync(iconStampPath, `${stampValue}\n`);

function resolveAppIconSource() {
  const scriptPath = path.join(sourceDir, "scripts", "sync-branding.mjs");
  if (!fs.existsSync(scriptPath)) {
    throw new Error(`缺少 NexGen 品牌同步脚本：${scriptPath}`);
  }

  const result = spawnSync("node", [scriptPath, "--prepare-icon-source"], {
    cwd: sourceDir,
    env: process.env,
    encoding: "utf8",
  });
  if (result.error) throw result.error;
  if (result.status !== 0) {
    process.stderr.write(result.stderr || "");
    throw new Error("解析 NexGen App 图标源失败");
  }

  return String(result.stdout || "").trim();
}

function hashFile(filePath) {
  return crypto.createHash("sha256").update(fs.readFileSync(filePath)).digest("hex");
}

function isPathInside(filePath, dirPath) {
  const relative = path.relative(path.resolve(dirPath), path.resolve(filePath));
  return relative === "" || (!relative.startsWith("..") && !path.isAbsolute(relative));
}

function run(command, args) {
  const result = spawnSync(command, args, {
    cwd: sourceDir,
    env: process.env,
    stdio: "inherit",
    shell: process.platform === "win32",
  });
  if (result.error) throw result.error;
  if (result.status !== 0) process.exit(result.status ?? 1);
}
