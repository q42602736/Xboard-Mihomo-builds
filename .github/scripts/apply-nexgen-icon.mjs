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

const preparedAppIconSource = ensureSquareIconSource(appIconSource, sourceDir);

if (isPathInside(preparedAppIconSource, iconsDir)) {
  console.log("NexGen App 图标源已在 Tauri icons 目录，跳过图标生成。");
  process.exit(0);
}

const stampValue = `${hashFile(preparedAppIconSource)} ${preparedAppIconSource}`;
if (fs.existsSync(iconStampPath) && fs.readFileSync(iconStampPath, "utf8").trim() === stampValue) {
  console.log("NexGen Tauri 图标已是最新。");
  process.exit(0);
}

console.log("根据 nexgen.config.yaml 生成 NexGen Tauri 图标...");
run("npm", ["run", "tauri", "--", "icon", preparedAppIconSource]);
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

function ensureSquareIconSource(sourcePath, sourceDir) {
  const dimension = getImageDimension(sourcePath);
  if (!dimension) {
    console.warn("无法识别图标尺寸，继续使用原图。");
    return sourcePath;
  }

  if (dimension.width === dimension.height) {
    return sourcePath;
  }

  const outputDir = path.join(sourceDir, ".nexgen", "icons");
  const outputPath = path.join(outputDir, "app-icon.square.png");
  const size = Math.max(dimension.width, dimension.height);
  const padCommand = findImageMagickPadCommand(sourcePath, outputPath, size);
  if (!padCommand) {
    throw new Error("图标不是正方形，且未找到可用的图片处理工具。请安装 ImageMagick 后重试。");
  }

  fs.mkdirSync(outputDir, { recursive: true });
  const [command, args] = padCommand;
  const result = spawnSync(command, args, {
    cwd: sourceDir,
    env: process.env,
    stdio: "ignore",
    shell: process.platform === "win32",
  });
  if (result.status !== 0 || !fs.existsSync(outputPath)) {
    throw new Error("图标自动规整为正方形失败。");
  }

  return outputPath;
}

function getImageDimension(sourcePath) {
  if (commandExists("magick")) {
    const result = spawnSync("magick", ["identify", "-format", "%w %h", sourcePath], {
      cwd: path.dirname(sourcePath),
      env: process.env,
      encoding: "utf8",
      shell: process.platform === "win32",
    });
    if (result.status === 0) {
      const match = String(result.stdout || "").trim().match(/^(\d+)\s+(\d+)$/);
      if (match) {
        return { width: Number(match[1]), height: Number(match[2]) };
      }
    }
  }

  if (commandExists("identify")) {
    const result = spawnSync("identify", ["-format", "%w %h", sourcePath], {
      cwd: path.dirname(sourcePath),
      env: process.env,
      encoding: "utf8",
      shell: process.platform === "win32",
    });
    if (result.status === 0) {
      const match = String(result.stdout || "").trim().match(/^(\d+)\s+(\d+)$/);
      if (match) {
        return { width: Number(match[1]), height: Number(match[2]) };
      }
    }
  }

  return null;
}

function findImageMagickPadCommand(sourcePath, targetPath, size) {
  const args = [
    sourcePath,
    "-background",
    "none",
    "-gravity",
    "center",
    "-extent",
    `${size}x${size}`,
    targetPath,
  ];

  if (commandExists("magick")) {
    return ["magick", args];
  }

  if (commandExists("convert")) {
    return ["convert", args];
  }

  return undefined;
}

function commandExists(command) {
  const result = spawnSync(command, ["-version"], {
    cwd: process.cwd(),
    env: process.env,
    stdio: "ignore",
    shell: process.platform === "win32",
  });
  return result.status === 0;
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
