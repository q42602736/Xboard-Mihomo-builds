package main

import (
	"fmt"
	"strconv"
	"strings"
)

// 内置代理配置已整体迁移到 OSS 远程配置（顶层 clientProxy）下发：
//   clientProxy: { enabled, auto_enabled, nodes: [...] }
// 档案里不再保留该配置块。监听端口也不是面板项，两个客户端缺省都用 17890，
// 被占用时由核心自动改端口。

// sanitizeClientProxyNodes 按两个客户端 RemoteClientProxyConfig 的校验规则过滤节点。
//
// 客户端会丢弃 type/server/port 不完整的节点，这里提前做同样的过滤，
// 避免面板显示"已配置"但客户端实际没有可用线路。
func sanitizeClientProxyNodes(nodes []map[string]interface{}) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(nodes))
	for _, node := range nodes {
		if node == nil {
			continue
		}
		cleaned := make(map[string]interface{}, len(node))
		for key, value := range node {
			cleaned[key] = value
		}

		proxyType := readNodeString(cleaned, "type")
		server := readNodeString(cleaned, "server")
		port, ok := readNodePort(cleaned["port"])
		if proxyType == "" || server == "" || !ok || port <= 0 || port > 65535 {
			continue
		}

		cleaned["type"] = proxyType
		cleaned["server"] = server
		cleaned["port"] = port
		if name := readNodeString(cleaned, "name"); name != "" {
			cleaned["name"] = name
		} else {
			delete(cleaned, "name")
		}
		result = append(result, cleaned)
	}
	return result
}

func readNodeString(node map[string]interface{}, key string) string {
	value, ok := node[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", value))
}

// readNodePort 兼容数字和字符串两种端口写法，与客户端 _parsePort 一致。
func readNodePort(value interface{}) (int, bool) {
	switch typed := value.(type) {
	case nil:
		return 0, false
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		parsed, err := strconv.Atoi(strings.TrimSpace(fmt.Sprintf("%v", typed)))
		if err != nil {
			return 0, false
		}
		return parsed, true
	}
}

// describeClientProxyNodes 生成用于面板展示的单行摘要。
func describeClientProxyNodes(nodes []map[string]interface{}) string {
	parts := make([]string, 0, len(nodes))
	for _, node := range nodes {
		parts = append(parts, fmt.Sprintf("%s(%s %s:%v)",
			readNodeString(node, "name"),
			readNodeString(node, "type"),
			readNodeString(node, "server"),
			node["port"],
		))
	}
	return strings.Join(parts, ", ")
}
