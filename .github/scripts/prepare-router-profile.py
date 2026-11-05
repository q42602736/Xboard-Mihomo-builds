#!/usr/bin/env python3

import base64
import binascii
import json
import re
import sys
from pathlib import Path

import yaml


def mapping(value, name):
    if value is None:
        return {}
    if not isinstance(value, dict):
        raise ValueError(f"{name} 必须是配置对象")
    return value


def text(value):
    return str(value).strip() if value is not None else ""


def prepare_profile(profile_path, source_dir, output_dir, profile_name):
    profile = mapping(yaml.safe_load(profile_path.read_text(encoding="utf-8")), "档案")
    xboard = mapping(profile.get("xboard"), "xboard")
    if not xboard:
        raise ValueError("软路由打包需要包含 xboard 配置的老客户端档案")
    app = mapping(xboard.get("app"), "xboard.app")
    remote = mapping(xboard.get("remote_config"), "xboard.remote_config")
    subscription = mapping(xboard.get("subscription"), "xboard.subscription")
    settings = mapping(xboard.get("settings"), "xboard.settings")
    ui = mapping(xboard.get("ui"), "xboard.ui")
    template_path = source_dir / "router/xboard-openclash/files/config.json.in"
    template = template_path.read_text(encoding="utf-8")
    # 新版模板的 UA、模式和延迟优化使用 JSON 值占位符，必须先填入合法字面量。
    # 默认值与客户端订阅设置一致，随后由档案覆盖；旧版固定值模板保持兼容。
    for placeholder, default in (
        ("__XBOARD_SUBSCRIPTION_USER_AGENT__", ""),
        ("__XBOARD_EXCLUSIVE_USER_AGENT__", ""),
        ("__XBOARD_USE_EXCLUSIVE_MODE__", True),
        ("__XBOARD_LATENCY_REDUCTION_ENABLED__", False),
        ("__XBOARD_LATENCY_REDUCTION_VALUE__", 0),
    ):
        template = template.replace(placeholder, json.dumps(default))
    config = json.loads(template)

    app_name = " ".join(text(app.get("title") or xboard.get("title") or "XBoard").split())
    config["app_name"] = app_name
    config["provider"] = text(xboard.get("provider")) or app_name
    config["panel_type"] = text(xboard.get("panel_type")) or config["panel_type"]
    config["panel_url"] = text(xboard.get("panel_url"))
    sources = remote.get("sources") or []
    if not isinstance(sources, list):
        raise ValueError("remote_config.sources 必须是列表")
    urls = []
    remote_sources = []
    for source in sources:
        source = mapping(source, "远程配置源")
        url = text(source.get("url"))
        if not url:
            continue
        encryption_key = text(source.get("encryption_key", source.get("encryptionKey")))
        entry = {"url": url}
        if encryption_key:
            if "remote_config_sources" not in config:
                raise ValueError("所选源码版本缺少远程配置解密支持，请同步客户端源码后重试")
            try:
                key_bytes = base64.b64decode(encryption_key, validate=True)
            except (ValueError, binascii.Error):
                raise ValueError("远程配置 encryption_key 必须是有效 Base64") from None
            if len(key_bytes) not in (16, 24, 32):
                raise ValueError("远程配置 AES 密钥必须为 16、24 或 32 字节")
            entry["encryption_key"] = encryption_key
        elif url not in urls:
            urls.append(url)
        if entry not in remote_sources:
            remote_sources.append(entry)
    config["remote_config_urls"] = urls
    config["remote_config_sources"] = remote_sources
    if not remote_sources and not config["panel_url"]:
        raise ValueError("软路由档案缺少远程配置源或面板地址")

    api_prefix = text(next((remote[key] for key in (
        "api_path_prefix", "apiPathPrefix", "api_path", "apiPath"
    ) if remote.get(key) is not None), "/api/v1"))
    config["api_path_prefix"] = "/" + api_prefix.strip("/") if api_prefix.strip("/") else "/api/v1"

    for source_key, target_key in (
        ("user_agent", "subscription_user_agent"),
        ("exclusive_user_agent", "exclusive_user_agent"),
        ("decrypt_key", "decrypt_key"),
    ):
        value = subscription.get(source_key, xboard.get(source_key))
        if value is not None:
            config[target_key] = text(value) or config.get(target_key, "")

    for source_key, target_key in (
        ("use_exclusive_mode", "use_exclusive_user_agent"),
        ("prefer_encrypt", "prefer_encrypt"),
    ):
        value = subscription.get(source_key, xboard.get(source_key))
        if value is not None:
            if not isinstance(value, bool):
                raise ValueError(f"subscription.{source_key} 必须是布尔值")
            config[target_key] = value

    for source_key, alias, target_key in (
        ("auto_test_after_login", "autoTestAfterLogin", "auto_test_after_login"),
        ("auto_connect_on_startup", "autoConnectOnStartup", "auto_start_runtime"),
    ):
        value = settings.get(source_key, settings.get(alias))
        if value is not None:
            if not isinstance(value, bool):
                raise ValueError(f"settings.{source_key} 必须是布尔值")
            config[target_key] = value

    # 与 App 一致读取 ui.latency_reduction，兼容旧版直接填写百分比的写法。
    latency = ui.get("latency_reduction")
    if isinstance(latency, dict):
        latency_enabled = latency.get("enabled", False)
        latency_value = latency.get("value", 0)
    else:
        latency_value = latency
        latency_enabled = isinstance(latency, int) and latency > 0
    if latency_enabled is None:
        latency_enabled = False
    if latency_value is None:
        latency_value = 0
    if not isinstance(latency_enabled, bool):
        raise ValueError("ui.latency_reduction.enabled 必须是布尔值")
    if not isinstance(latency_value, int) or isinstance(latency_value, bool):
        raise ValueError("ui.latency_reduction.value 必须是整数")
    config["latency_reduction_enabled"] = latency_enabled
    config["latency_reduction_value"] = max(0, min(90, latency_value))

    # 中文品牌名保留在界面中，包标识使用稳定的 ASCII 名称。
    package_slug = re.sub(r"[^a-z0-9-]", "", app_name.lower()).strip("-")
    if not package_slug:
        package_slug = re.sub(r"[^a-z0-9-]", "", profile_name.lower()).strip("-") or "xboard"

    output_dir.mkdir(parents=True, exist_ok=True)
    config_path = output_dir / "config.json"
    config_path.write_text(json.dumps(config, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    config_path.chmod(0o600)
    (output_dir / "app_name").write_text(app_name + "\n", encoding="utf-8")
    (output_dir / "package_slug").write_text(package_slug + "\n", encoding="utf-8")
    print("已生成软路由品牌和运行配置")


if __name__ == "__main__":
    if len(sys.argv) != 5:
        sys.exit("用法：python3 prepare-router-profile.py <档案.yaml> <源码目录> <输出目录> <档案名称>")
    try:
        prepare_profile(Path(sys.argv[1]), Path(sys.argv[2]), Path(sys.argv[3]), sys.argv[4])
    except yaml.YAMLError:
        sys.exit("档案 YAML 格式无效，请检查配置内容")
    except (OSError, ValueError) as error:
        sys.exit(f"生成软路由配置失败：{error}")
