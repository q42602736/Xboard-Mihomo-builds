#!/usr/bin/env node

import fs from "node:fs";
import path from "node:path";

const [sourceDirArg, rawVersionArg] = process.argv.slice(2);

if (!sourceDirArg || !rawVersionArg) {
  console.error("用法：node apply-nexgen-version.mjs <NexGen源码目录> <版本号>");
  process.exit(1);
}

const sourceDir = path.resolve(sourceDirArg);
const appVersion = normalizeAppVersion(rawVersionArg);

updateJsonVersion(path.join(sourceDir, "package.json"), appVersion);
updatePackageLockVersion(path.join(sourceDir, "package-lock.json"), appVersion);
updateJsonVersion(path.join(sourceDir, "src-tauri", "tauri.conf.json"), appVersion);
updateCargoTomlVersion(path.join(sourceDir, "src-tauri", "Cargo.toml"), appVersion);
updateCargoLockVersion(path.join(sourceDir, "src-tauri", "Cargo.lock"), "nexgen_client_react", appVersion);

console.log(`已同步 NexGen App 版本号：${appVersion}`);

function normalizeAppVersion(value) {
  const normalized = String(value || "").trim().replace(/^v/i, "");
  if (!/^\d+\.\d+\.\d+$/.test(normalized)) {
    throw new Error(`NexGen App 版本号格式不正确：${value}，请使用 1.0.0 这种格式。`);
  }
  return normalized;
}

function updateJsonVersion(filePath, version) {
  if (!fs.existsSync(filePath)) return;

  const data = JSON.parse(fs.readFileSync(filePath, "utf8"));
  data.version = version;
  fs.writeFileSync(filePath, `${JSON.stringify(data, null, 2)}\n`);
}

function updatePackageLockVersion(filePath, version) {
  if (!fs.existsSync(filePath)) return;

  const data = JSON.parse(fs.readFileSync(filePath, "utf8"));
  data.version = version;
  if (data.packages && data.packages[""]) {
    data.packages[""].version = version;
  }
  fs.writeFileSync(filePath, `${JSON.stringify(data, null, 2)}\n`);
}

function updateCargoTomlVersion(filePath, version) {
  if (!fs.existsSync(filePath)) return;

  const content = fs.readFileSync(filePath, "utf8");
  fs.writeFileSync(filePath, updateTomlPackageVersion(content, version));
}

function updateTomlPackageVersion(content, version) {
  const packageStart = content.search(/^\[package\]\s*$/m);
  if (packageStart < 0) return content;

  const sectionContent = content.slice(packageStart + 1);
  const nextSectionMatch = /^\[[^\]]+\]\s*$/m.exec(sectionContent);
  const packageEnd = nextSectionMatch ? packageStart + 1 + nextSectionMatch.index : content.length;
  const before = content.slice(0, packageStart);
  let packageSection = content.slice(packageStart, packageEnd);
  const after = content.slice(packageEnd);
  const versionLine = `version = "${escapeTomlString(version)}"`;

  if (/^version\s*=\s*"[^"]*"/m.test(packageSection)) {
    packageSection = packageSection.replace(/^version\s*=\s*"[^"]*"/m, versionLine);
  } else if (/^name\s*=/m.test(packageSection)) {
    packageSection = packageSection.replace(/^name\s*=.*$/m, (line) => `${line}\n${versionLine}`);
  } else {
    packageSection = packageSection.replace(/^(\[package\]\s*)$/m, `$1\n${versionLine}`);
  }

  return `${before}${packageSection}${after}`;
}

function updateCargoLockVersion(filePath, packageName, version) {
  if (!fs.existsSync(filePath)) return;

  const content = fs.readFileSync(filePath, "utf8");
  const packagePattern = new RegExp(
    `(\\[\\[package\\]\\]\\nname = "${escapeRegExp(packageName)}"\\nversion = )"[^"]*"`,
    "m",
  );
  if (!packagePattern.test(content)) return;

  fs.writeFileSync(filePath, content.replace(packagePattern, `$1"${escapeTomlString(version)}"`));
}

function escapeTomlString(value) {
  return String(value).replace(/\\/g, "\\\\").replace(/"/g, '\\"');
}

function escapeRegExp(value) {
  return String(value).replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}
