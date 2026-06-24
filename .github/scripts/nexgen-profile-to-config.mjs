#!/usr/bin/env node

import fs from "node:fs";

const [profilePath, defaultConfigPath, outputPath] = process.argv.slice(2);

if (!profilePath || !defaultConfigPath || !outputPath) {
  console.error("用法：node nexgen-profile-to-config.mjs <profile.yaml> <默认nexgen.config.yaml> <输出路径>");
  process.exit(1);
}

const profile = parseSimpleYaml(fs.readFileSync(profilePath, "utf8"));
const defaultConfig = parseSimpleYaml(fs.readFileSync(defaultConfigPath, "utf8"));
const profileConfig = record(profile.nexgen);
const legacyXboard = record(profile.xboard);
const nexgen = normalizeNexgenConfigDefaults(deepMerge(
  record(defaultConfig.nexgen),
  Object.keys(profileConfig).length > 0 ? convertNexgenProfile(profileConfig) : convertXboardToNexgen(legacyXboard),
));

fs.writeFileSync(outputPath, toYaml({ nexgen }));

function convertNexgenProfile(nexgen) {
  const converted = record(nexgen);
  normalizeCloudDispatchOverride(converted);
  converted.ui = {
    ...record(converted.ui),
    telegram: normalizeTelegramOverride(record(converted.ui).telegram),
    utilities: normalizeUtilitiesOverride(record(converted.ui).utilities),
  };
  return pruneEmpty(converted);
}

function convertXboardToNexgen(xboard) {
  const app = record(xboard.app);
  const ui = record(xboard.ui);
  const converted = {
    provider: text(xboard.provider),
    remote_config: record(xboard.remote_config),
    app: {
      title: text(app.title || xboard.title),
      logo: record(app.logo),
      auth_background: record(app.auth_background),
      app_icon: record(app.app_icon),
    },
    subscription_cache: record(xboard.subscription_cache),
    auto_offline: record(xboard.auto_offline || xboard.offline_mode),
    subscription: record(xboard.subscription),
    security: record(xboard.security),
    cloud_dispatch: getCloudDispatchOverride(xboard),
    registration_invite: record(xboard.registration_invite ?? xboard.registrationInvite),
    ui: {
      hide_traffic_details: boolOrUndefined(ui.hide_traffic_details),
      hide_node_status: boolOrUndefined(ui.hide_node_status),
      hide_current_node_label: boolOrUndefined(ui.hide_current_node_label ?? ui.hideCurrentNodeLabel),
      latency_reduction: record(ui.latency_reduction),
      notice: record(ui.notice),
      checkin: record(ui.checkin),
      gift_card: record(ui.gift_card),
      telegram: normalizeTelegramOverride(ui.telegram),
      utilities: normalizeUtilitiesOverride(ui.utilities),
      proxy_groups: record(ui.proxy_groups),
    },
  };
  return pruneEmpty(converted);
}

function getCloudDispatchOverride(config) {
  const hasSnake = Object.prototype.hasOwnProperty.call(config, "cloud_dispatch");
  const hasCamel = Object.prototype.hasOwnProperty.call(config, "cloudDispatch");
  if (!hasSnake && !hasCamel) return disabledCloudDispatchOverride();

  const cloudDispatch = {
    ...record(config.cloud_dispatch ?? config.cloudDispatch),
  };
  normalizeAlias(cloudDispatch, "enable", "enabled");
  normalizeAlias(cloudDispatch, "queryUrl", "query_url");
  normalizeAlias(cloudDispatch, "querySecret", "query_secret");
  normalizeAlias(cloudDispatch, "fallbackRetryMinutes", "fallback_retry_minutes");

  const auto = {
    ...record(cloudDispatch.auto),
  };
  normalizeAlias(auto, "enable", "enabled");
  normalizeAlias(auto, "intervalMinutes", "interval_minutes");
  delete auto.enable;
  delete auto.intervalMinutes;

  cloudDispatch.enabled = typeof cloudDispatch.enabled === "boolean" ? cloudDispatch.enabled : false;
  cloudDispatch.query_url = nullableTextOverride(cloudDispatch.query_url);
  cloudDispatch.query_secret = nullableTextOverride(cloudDispatch.query_secret);
  cloudDispatch.fallback_retry_minutes = cloudDispatch.fallback_retry_minutes ?? 5;
  cloudDispatch.auto = {
    ...auto,
    enabled: typeof auto.enabled === "boolean" ? auto.enabled : false,
    interval_minutes: auto.interval_minutes ?? 5,
  };
  delete cloudDispatch.enable;
  delete cloudDispatch.queryUrl;
  delete cloudDispatch.querySecret;
  delete cloudDispatch.fallbackRetryMinutes;
  return cloudDispatch;
}

function normalizeCloudDispatchOverride(config) {
  config.cloud_dispatch = getCloudDispatchOverride(config);
  delete config.cloudDispatch;
}

function disabledCloudDispatchOverride() {
  return {
    enabled: false,
    query_url: null,
    query_secret: null,
    fallback_retry_minutes: 5,
    auto: {
      enabled: false,
      interval_minutes: 5,
    },
  };
}

function normalizeAlias(target, oldKey, nextKey) {
  if (Object.prototype.hasOwnProperty.call(target, oldKey) && !Object.prototype.hasOwnProperty.call(target, nextKey)) {
    target[nextKey] = target[oldKey];
  }
}

function nullableTextOverride(value) {
  if (value === undefined || value === null) return null;
  if (typeof value === "string" && value.trim() === "") return null;
  return value;
}

function normalizeUtilitiesOverride(value) {
  const utilities = record(value);
  const popularApps = record(utilities.popular_apps);
  const tools = record(utilities.tools);
  const nextTools = {
    ...tools,
  };
  if (Object.prototype.hasOwnProperty.call(tools, "cf_speed") || Object.prototype.hasOwnProperty.call(tools, "cfSpeed")) {
    nextTools.cf_speed = normalizeCfSpeedOverride(tools.cf_speed ?? tools.cfSpeed);
  }
  delete nextTools.cfSpeed;
  return {
    ...utilities,
    tools: nextTools,
    popular_apps: {
      show_section: typeof popularApps.show_section === "boolean" ? popularApps.show_section : false,
      items: Array.isArray(popularApps.items) ? popularApps.items : [],
    },
  };
}

function normalizeTelegramOverride(value) {
  const telegram = {
    ...record(value),
  };
  normalizeAlias(telegram, "showButton", "show_button");
  normalizeAlias(telegram, "channelUrl", "url");
  normalizeAlias(telegram, "channel_url", "url");
  normalizeAlias(telegram, "groupUrl", "url");
  normalizeAlias(telegram, "group_url", "url");
  normalizeAlias(telegram, "link", "url");
  return {
    show_button: typeof telegram.show_button === "boolean" ? telegram.show_button : false,
    url: text(telegram.url),
  };
}

function normalizeNexgenConfigDefaults(config) {
  const nexgen = record(config);
  const ui = {
    ...record(nexgen.ui),
  };
  ui.telegram = normalizeTelegramOverride(ui.telegram);
  nexgen.ui = ui;
  return nexgen;
}

function normalizeCfSpeedOverride(value) {
  const cfSpeed = {
    ...record(value),
  };
  normalizeAlias(cfSpeed, "showButton", "show_button");
  normalizeAlias(cfSpeed, "targetDomains", "target_domains");

  const autoReplace = {
    ...record(cfSpeed.auto_replace ?? cfSpeed.autoReplace),
  };
  normalizeAlias(autoReplace, "intervalMinutes", "interval_minutes");

  cfSpeed.show_button = typeof cfSpeed.show_button === "boolean" ? cfSpeed.show_button : true;
  cfSpeed.target_domains = normalizeStringList(cfSpeed.target_domains);
  cfSpeed.auto_replace = {
    ...autoReplace,
    enabled: typeof autoReplace.enabled === "boolean" ? autoReplace.enabled : false,
    interval_minutes: normalizeIntervalMinutes(autoReplace.interval_minutes, 1440),
  };
  delete cfSpeed.showButton;
  delete cfSpeed.targetDomains;
  delete cfSpeed.autoReplace;
  delete cfSpeed.auto_replace.intervalMinutes;
  return cfSpeed;
}

function normalizeStringList(value) {
  if (typeof value === "string") {
    value = value.split(/[,\n]/);
  }
  if (!Array.isArray(value)) return [];
  const seen = new Set();
  const result = [];
  for (const item of value) {
    const textValue = text(item);
    if (!textValue || seen.has(textValue)) continue;
    seen.add(textValue);
    result.push(textValue);
  }
  return result;
}

function normalizeIntervalMinutes(value, fallback) {
  const parsed = Number.parseInt(value ?? fallback, 10);
  if (!Number.isFinite(parsed) || parsed < 1) return fallback;
  return Math.min(parsed, 1440);
}

function deepMerge(base, override) {
  if (Array.isArray(base) || Array.isArray(override)) {
    return override === undefined ? base : override;
  }
  if (!isPlainObject(base) || !isPlainObject(override)) {
    return override === undefined || override === "" ? base : override;
  }
  const result = { ...base };
  for (const [key, value] of Object.entries(override)) {
    if (value === undefined) continue;
    result[key] = deepMerge(result[key], value);
  }
  return result;
}

function pruneEmpty(value) {
  if (Array.isArray(value)) {
    return value.map(pruneEmpty);
  }
  if (!isPlainObject(value)) {
    return value;
  }
  const result = {};
  for (const [key, item] of Object.entries(value)) {
    const pruned = pruneEmpty(item);
    if (pruned === undefined) continue;
    if (pruned === "" || (isPlainObject(pruned) && Object.keys(pruned).length === 0)) continue;
    result[key] = pruned;
  }
  return result;
}

function record(value) {
  return isPlainObject(value) ? value : {};
}

function text(value) {
  if (value === undefined || value === null) return "";
  return String(value).trim();
}

function boolOrUndefined(value) {
  return typeof value === "boolean" ? value : undefined;
}

function isPlainObject(value) {
  return !!value && typeof value === "object" && !Array.isArray(value);
}

function parseSimpleYaml(source) {
  const lines = source.split(/\r?\n/);
  const root = {};
  const stack = [{ indent: -1, value: root }];

  for (let index = 0; index < lines.length; index += 1) {
    const parsedLine = parseLine(lines[index]);
    if (!parsedLine) continue;

    const { indent, trimmed } = parsedLine;
    while (stack.length > 1 && indent <= stack.at(-1).indent) {
      stack.pop();
    }

    const parent = stack.at(-1).value;
    if (trimmed.startsWith("- ")) {
      if (!Array.isArray(parent)) continue;
      const itemText = trimmed.slice(2).trim();
      if (!itemText) {
        const item = {};
        parent.push(item);
        stack.push({ indent, value: item });
        continue;
      }

      const pair = splitKeyValue(itemText);
      if (!pair) {
        parent.push(parseScalar(itemText));
        continue;
      }

      const item = {};
      parent.push(item);
      const assigned = assignKeyValue(item, pair.key, pair.value, lines, index, indent);
      stack.push({ indent, value: assigned && typeof assigned === "object" ? assigned : item });
      continue;
    }

    if (Array.isArray(parent)) continue;
    const pair = splitKeyValue(trimmed);
    if (!pair) continue;

    const assigned = assignKeyValue(parent, pair.key, pair.value, lines, index, indent);
    if (assigned && typeof assigned === "object") {
      stack.push({ indent, value: assigned });
    }
  }

  return root;
}

function assignKeyValue(target, key, value, lines, index, indent) {
  if (value) {
    target[key] = parseScalar(value);
    return undefined;
  }

  const next = findNextLine(lines, index + 1);
  if (!next || next.indent <= indent) {
    target[key] = "";
    return undefined;
  }

  const nested = next.trimmed.startsWith("- ") ? [] : {};
  target[key] = nested;
  return nested;
}

function parseLine(line) {
  const withoutComment = stripComment(line);
  if (!withoutComment.trim()) return null;
  return {
    indent: withoutComment.match(/^ */)?.[0].length ?? 0,
    trimmed: withoutComment.trim(),
  };
}

function findNextLine(lines, startIndex) {
  for (let index = startIndex; index < lines.length; index += 1) {
    const parsedLine = parseLine(lines[index]);
    if (parsedLine) return parsedLine;
  }
  return null;
}

function splitKeyValue(value) {
  const separatorIndex = value.search(/:\s|:$/);
  if (separatorIndex < 0) return null;
  return {
    key: value.slice(0, separatorIndex).trim(),
    value: value.slice(separatorIndex + 1).trim(),
  };
}

function parseScalar(value) {
  if (!value) return "";
  if (value === "true") return true;
  if (value === "false") return false;
  if (value === "null") return null;
  if (value === "[]") return [];
  if (value === "{}") return {};
  if (/^-?\d+(\.\d+)?$/.test(value)) return Number(value);
  if ((value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'"))) {
    return value.slice(1, -1);
  }
  return value;
}

function stripComment(line) {
  let quote = "";
  for (let index = 0; index < line.length; index += 1) {
    const char = line[index];
    if ((char === '"' || char === "'") && line[index - 1] !== "\\") {
      quote = quote === char ? "" : quote || char;
      continue;
    }
    if (char === "#" && !quote) {
      return line.slice(0, index);
    }
  }
  return line;
}

function toYaml(value, indent = 0) {
  if (!isPlainObject(value)) return "";
  const spaces = " ".repeat(indent);
  const lines = [];
  for (const [key, item] of Object.entries(value)) {
    if (Array.isArray(item)) {
      lines.push(`${spaces}${key}:`);
      if (item.length === 0) continue;
      for (const entry of item) {
        if (isPlainObject(entry)) {
          lines.push(...formatSequenceObject(entry, indent + 2));
        } else {
          lines.push(`${spaces}  - ${formatScalar(entry)}`);
        }
      }
      continue;
    }
    if (isPlainObject(item)) {
      lines.push(`${spaces}${key}:`);
      lines.push(toYaml(item, indent + 2).replace(/\n$/, ""));
      continue;
    }
    lines.push(`${spaces}${key}: ${formatScalar(item)}`);
  }
  return `${lines.join("\n")}\n`;
}

function formatScalar(value) {
  if (value === null || value === undefined) return "";
  if (typeof value === "boolean" || typeof value === "number") return String(value);
  const textValue = String(value);
  if (textValue === "") return "";
  if (/^[A-Za-z0-9_./:@?=&%,+ -]+$/.test(textValue) && !/^\s|\s$/.test(textValue) && !textValue.startsWith("#")) {
    return textValue;
  }
  return JSON.stringify(textValue);
}

function formatSequenceObject(value, indent) {
  const spaces = " ".repeat(indent);
  const childSpaces = " ".repeat(indent + 2);
  const entries = Object.entries(value);
  if (entries.length === 0) {
    return [`${spaces}- {}`];
  }
  const lines = [];
  entries.forEach(([key, item], index) => {
    const prefix = index === 0 ? `${spaces}- ` : childSpaces;
    if (Array.isArray(item)) {
      lines.push(`${prefix}${key}:`);
      if (item.length === 0) return;
      for (const entry of item) {
        if (isPlainObject(entry)) {
          lines.push(...formatSequenceObject(entry, indent + 4));
        } else {
          lines.push(`${" ".repeat(indent + 4)}- ${formatScalar(entry)}`);
        }
      }
      return;
    }
    if (isPlainObject(item)) {
      lines.push(`${prefix}${key}:`);
      lines.push(toYaml(item, indent + 4).replace(/\n$/, ""));
      return;
    }
    lines.push(`${prefix}${key}: ${formatScalar(item)}`);
  });
  return lines;
}
