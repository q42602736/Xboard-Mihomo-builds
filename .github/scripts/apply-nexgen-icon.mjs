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

const pngAppIconSource = ensurePngIconSource(appIconSource, sourceDir);
const preparedAppIconSource = ensureSquareIconSource(pngAppIconSource, sourceDir);

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

function ensurePngIconSource(sourcePath, sourceDir) {
  const inspection = inspectImageSource(sourcePath);
  if (!inspection.valid) {
    throw new Error(`NexGen App 图标文件无效：${inspection.reason}`);
  }

  const outputDir = path.join(sourceDir, ".nexgen", "icons");
  const outputPath = path.join(outputDir, "app-icon.source.png");
  const format = normalizeImageFormat(inspection.format);

  fs.mkdirSync(outputDir, { recursive: true });

  if (format === "png") {
    if (path.extname(sourcePath).toLowerCase() === ".png") {
      return sourcePath;
    }
    if (path.resolve(sourcePath) !== path.resolve(outputPath)) {
      fs.copyFileSync(sourcePath, outputPath);
    }
    return outputPath;
  }

  const convertCommand = findImageMagickConvertCommand(sourcePath, outputPath);
  if (!convertCommand) {
    throw new Error(`图标格式为 ${format}，但当前环境缺少可用的图片转换工具（ImageMagick）将其转换为 PNG。`);
  }

  const [command, args] = convertCommand;
  const result = spawnSync(command, args, {
    cwd: sourceDir,
    env: process.env,
    stdio: "ignore",
    shell: process.platform === "win32",
  });
  if (result.status !== 0 || !fs.existsSync(outputPath)) {
    throw new Error(`图标转换为 PNG 失败：${path.basename(sourcePath)}`);
  }

  const convertedInspection = inspectImageSource(outputPath);
  if (!convertedInspection.valid || normalizeImageFormat(convertedInspection.format) !== "png") {
    throw new Error("图标转换后的输出不是有效 PNG。");
  }

  return outputPath;
}

function ensureSquareIconSource(sourcePath, sourceDir) {
  const dimension = getImageDimension(sourcePath);
  if (!dimension) {
    throw new Error("无法识别图标尺寸，不能确认是否符合 Tauri 正方形图标要求。请使用 PNG/WebP/JPEG/GIF/BMP 图标，或确保 ImageMagick 可用。");
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

  const outputDimension = getImageDimension(outputPath);
  if (!outputDimension || outputDimension.width !== outputDimension.height) {
    throw new Error("图标自动规整后的输出仍不是有效正方形。");
  }

  return outputPath;
}

function inspectImageSource(sourcePath) {
  if (!fs.existsSync(sourcePath)) {
    return { valid: false, reason: `文件不存在：${sourcePath}` };
  }

  const fileBuffer = fs.readFileSync(sourcePath);
  if (fileBuffer.length === 0) {
    return { valid: false, reason: "文件为空" };
  }

  const head = fileBuffer.subarray(0, Math.min(fileBuffer.length, 4096));
  const signatureFormat = detectImageFormatFromBuffer(head);
  if (signatureFormat) {
    return { valid: true, format: signatureFormat };
  }

  const textPreview = head.toString("utf8").replace(/^\uFEFF/, "").trimStart();
  if (looksLikeSvg(textPreview)) {
    return { valid: true, format: "svg" };
  }
  if (looksLikeHtml(textPreview)) {
    return { valid: false, reason: "内容看起来是 HTML，可能是图床错误页或鉴权页面" };
  }
  if (looksLikeJson(textPreview)) {
    return { valid: false, reason: "内容看起来是 JSON，不是图片" };
  }

  const metadata = getImageMetadata(sourcePath);
  if (metadata?.format) {
    return { valid: true, format: normalizeImageFormat(metadata.format) };
  }

  return { valid: false, reason: "文件头无法识别为受支持图片，且图片工具也无法解析" };
}

function detectImageFormatFromBuffer(buffer) {
  if (buffer.length >= 8 && compareBytes(buffer, [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a])) return "png";
  if (buffer.length >= 3 && compareBytes(buffer, [0xff, 0xd8, 0xff])) return "jpeg";
  if (buffer.length >= 12 && buffer.toString("ascii", 0, 4) === "RIFF" && buffer.toString("ascii", 8, 12) === "WEBP") return "webp";
  if (buffer.length >= 6) {
    const gifHeader = buffer.toString("ascii", 0, 6);
    if (gifHeader === "GIF87a" || gifHeader === "GIF89a") return "gif";
  }
  if (buffer.length >= 2 && buffer.toString("ascii", 0, 2) === "BM") return "bmp";
  if (buffer.length >= 4 && compareBytes(buffer, [0x00, 0x00, 0x01, 0x00])) return "ico";
  if (buffer.length >= 4) {
    const littleEndianTiff = compareBytes(buffer, [0x49, 0x49, 0x2a, 0x00]);
    const bigEndianTiff = compareBytes(buffer, [0x4d, 0x4d, 0x00, 0x2a]);
    if (littleEndianTiff || bigEndianTiff) return "tiff";
  }
  if (buffer.length >= 12 && buffer.toString("ascii", 4, 8) === "ftyp") {
    const brand = buffer.toString("ascii", 8, 12).toLowerCase();
    if (brand === "avif" || brand === "avis") return "avif";
    if (brand.startsWith("hei") || brand.startsWith("mif")) return "heic";
  }
  return "";
}

function compareBytes(buffer, bytes) {
  if (buffer.length < bytes.length) return false;
  return bytes.every((value, index) => buffer[index] === value);
}

function looksLikeSvg(text) {
  return /^<\?xml[\s\S]*?<svg\b/i.test(text) || /^<!doctype\s+svg[\s\S]*?<svg\b/i.test(text) || /^<svg\b/i.test(text);
}

function looksLikeHtml(text) {
  return /^<!doctype\s+html\b/i.test(text) || /^<html\b/i.test(text) || /^<head\b/i.test(text) || /^<body\b/i.test(text);
}

function looksLikeJson(text) {
  return /^(?:\{|\[)/.test(text);
}

function normalizeImageFormat(format) {
  const normalized = String(format || "").trim().toLowerCase();
  if (!normalized) return "";
  if (normalized === "jpg" || normalized === "jpeg") return "jpeg";
  if (normalized === "svg+xml") return "svg";
  if (normalized === "tif") return "tiff";
  return normalized;
}

function getImageMetadata(sourcePath) {
  const probeCommands = [
    ["magick", ["identify", "-format", "%m %w %h", sourcePath]],
    ["identify", ["-format", "%m %w %h", sourcePath]],
  ];

  for (const [command, args] of probeCommands) {
    if (!commandExists(command)) continue;
    const result = spawnSync(command, args, {
      cwd: path.dirname(sourcePath),
      env: process.env,
      encoding: "utf8",
      shell: process.platform === "win32",
    });
    if (result.status !== 0) continue;
    const match = String(result.stdout || "").trim().match(/^([A-Za-z0-9+_-]+)\s+(\d+)\s+(\d+)$/);
    if (!match) continue;
    return {
      format: match[1],
      width: Number(match[2]),
      height: Number(match[3]),
    };
  }

  return null;
}

function getImageDimension(sourcePath) {
  const metadata = getImageMetadata(sourcePath);
  if (metadata) {
    return { width: metadata.width, height: metadata.height };
  }

  return getImageDimensionFromBuffer(fs.readFileSync(sourcePath));
}

function getImageDimensionFromBuffer(buffer) {
  if (buffer.length >= 24 && compareBytes(buffer, [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a])) {
    return {
      width: buffer.readUInt32BE(16),
      height: buffer.readUInt32BE(20),
    };
  }

  if (buffer.length >= 10) {
    const gifHeader = buffer.toString("ascii", 0, 6);
    if (gifHeader === "GIF87a" || gifHeader === "GIF89a") {
      return {
        width: buffer.readUInt16LE(6),
        height: buffer.readUInt16LE(8),
      };
    }
  }

  if (buffer.length >= 30 && buffer.toString("ascii", 0, 2) === "BM") {
    return {
      width: Math.abs(buffer.readInt32LE(18)),
      height: Math.abs(buffer.readInt32LE(22)),
    };
  }

  if (buffer.length >= 12 && buffer.toString("ascii", 0, 4) === "RIFF" && buffer.toString("ascii", 8, 12) === "WEBP") {
    return getWebpDimension(buffer);
  }

  if (buffer.length >= 4 && compareBytes(buffer, [0xff, 0xd8, 0xff])) {
    return getJpegDimension(buffer);
  }

  return null;
}

function getWebpDimension(buffer) {
  let offset = 12;
  while (offset + 8 <= buffer.length) {
    const chunkType = buffer.toString("ascii", offset, offset + 4);
    const chunkSize = buffer.readUInt32LE(offset + 4);
    const payloadOffset = offset + 8;
    const nextOffset = payloadOffset + chunkSize + (chunkSize % 2);

    if (payloadOffset + chunkSize > buffer.length) return null;

    if (chunkType === "VP8X" && chunkSize >= 10) {
      return {
        width: 1 + readUInt24LE(buffer, payloadOffset + 4),
        height: 1 + readUInt24LE(buffer, payloadOffset + 7),
      };
    }

    if (chunkType === "VP8L" && chunkSize >= 5 && buffer[payloadOffset] === 0x2f) {
      const b1 = buffer[payloadOffset + 1];
      const b2 = buffer[payloadOffset + 2];
      const b3 = buffer[payloadOffset + 3];
      const b4 = buffer[payloadOffset + 4];
      return {
        width: 1 + (((b2 & 0x3f) << 8) | b1),
        height: 1 + (((b4 & 0x0f) << 10) | (b3 << 2) | ((b2 & 0xc0) >> 6)),
      };
    }

    if (chunkType === "VP8 " && chunkSize >= 10) {
      return {
        width: buffer.readUInt16LE(payloadOffset + 6) & 0x3fff,
        height: buffer.readUInt16LE(payloadOffset + 8) & 0x3fff,
      };
    }

    offset = nextOffset;
  }

  return null;
}

function readUInt24LE(buffer, offset) {
  return buffer[offset] | (buffer[offset + 1] << 8) | (buffer[offset + 2] << 16);
}

function getJpegDimension(buffer) {
  let offset = 2;
  while (offset + 9 < buffer.length) {
    if (buffer[offset] !== 0xff) {
      offset += 1;
      continue;
    }

    while (buffer[offset] === 0xff) {
      offset += 1;
    }

    const marker = buffer[offset];
    offset += 1;

    if (marker === 0xd9 || marker === 0xda) return null;
    if (offset + 2 > buffer.length) return null;

    const segmentLength = buffer.readUInt16BE(offset);
    if (segmentLength < 2 || offset + segmentLength > buffer.length) return null;

    if (isJpegStartOfFrameMarker(marker)) {
      return {
        height: buffer.readUInt16BE(offset + 3),
        width: buffer.readUInt16BE(offset + 5),
      };
    }

    offset += segmentLength;
  }

  return null;
}

function isJpegStartOfFrameMarker(marker) {
  return [
    0xc0,
    0xc1,
    0xc2,
    0xc3,
    0xc5,
    0xc6,
    0xc7,
    0xc9,
    0xca,
    0xcb,
    0xcd,
    0xce,
    0xcf,
  ].includes(marker);
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

function findImageMagickConvertCommand(sourcePath, targetPath) {
  const args = [sourcePath, targetPath];

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
