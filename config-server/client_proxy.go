package main

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// 内置代理默认监听端口，与客户端 GeneratedConfig.clientProxyPort 的默认值保持一致。
const defaultClientProxyPort = 17890

// ClientProxyState 描述档案中的 xboard.client_proxy 配置。
//
// 该配置只用于生成客户端本地通讯专用的 Mihomo 监听器，
// 不会写进 remote.config.json，也不接管用户的代理流量。
type ClientProxyState struct {
	Enabled bool                     `json:"enabled"`
	Port    int                      `json:"port"`
	Nodes   []map[string]interface{} `json:"nodes"`
}

// mergeClientProxyConfig 把面板表单提交的内置代理配置写回档案 YAML。
//
// state 为 nil 时不做任何修改，保持档案原样。
// 监听端口不作为面板配置项：state.Port <= 0 时不写、也不清除档案里已有的 port，
// 客户端会用自身默认值（17890），并在被占用时自动改用随机可用端口。
func mergeClientProxyConfig(profileRoot *yaml.Node, state *ClientProxyState) error {
	if state == nil {
		return nil
	}

	nodes := sanitizeClientProxyNodes(state.Nodes)
	if state.Enabled && len(nodes) == 0 {
		return fmt.Errorf("开启客户端内置代理时必须至少导入一个有效节点")
	}

	clientProxy := ensureMapValueNode(profileRoot, "client_proxy")
	setMapBoolValue(clientProxy, "enabled", state.Enabled)
	if state.Port > 0 {
		setMapIntValue(clientProxy, "port", normalizeClientProxyPort(state.Port))
	}
	setMapNodeValue(clientProxy, "nodes", newClientProxyNodesYamlNode(nodes))
	// 客户端仍兼容单个 node 写法，这里统一收敛成 nodes 列表。
	removeMapKeys(clientProxy, "node")
	return nil
}

func normalizeClientProxyPort(value int) int {
	if value >= 1024 && value <= 65535 {
		return value
	}
	return defaultClientProxyPort
}

// sanitizeClientProxyNodes 按客户端 ClientProxyConfig 的校验规则过滤节点。
//
// 客户端会丢弃 type/server/port 不完整的节点，这里提前做同样的过滤，
// 避免面板显示"已启用"但客户端实际没有可用内置代理。
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
		if proxyType == "" || server == "" || !ok || port <= 0 {
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

// clientProxyStateFromYaml 从档案 YAML 中读出 client_proxy 配置。
//
// 兼容三种历史写法：nodes 列表、单个 node 对象、以及旧的扁平字段。
func clientProxyStateFromYaml(yamlContent string) ClientProxyState {
	state := ClientProxyState{Port: defaultClientProxyPort}
	doc, err := parseProfileYamlDocument(yamlContent)
	if err != nil {
		return state
	}
	root := ensureDocumentMappingNode(doc)
	profileRoot := getMapValueNode(root, "xboard")
	if profileRoot == nil {
		return state
	}
	clientProxy := getMapValueNode(profileRoot, "client_proxy")
	if clientProxy == nil || clientProxy.Kind != yaml.MappingNode {
		return state
	}

	if port, ok := readNodePort(readYamlNodeValue(getMapValueNode(clientProxy, "port"))); ok {
		state.Port = normalizeClientProxyPort(port)
	}

	nodes := make([]map[string]interface{}, 0)
	if nodesNode := getMapValueNode(clientProxy, "nodes"); nodesNode != nil && nodesNode.Kind == yaml.SequenceNode {
		for _, item := range nodesNode.Content {
			if decoded := decodeYamlNodeMap(item); decoded != nil {
				nodes = append(nodes, decoded)
			}
		}
	}
	if len(nodes) == 0 {
		if singleNode := getMapValueNode(clientProxy, "node"); singleNode != nil && singleNode.Kind == yaml.MappingNode {
			if decoded := decodeYamlNodeMap(singleNode); decoded != nil {
				nodes = append(nodes, decoded)
			}
		}
	}
	state.Nodes = sanitizeClientProxyNodes(nodes)

	// enabled 缺省时以"是否存在节点"作为判断依据，与 generate_config.dart 保持一致。
	if enabledNode := getMapValueNode(clientProxy, "enabled"); enabledNode != nil {
		state.Enabled = enabledNode.Value == "true"
	} else {
		state.Enabled = len(state.Nodes) > 0
	}
	return state
}

func readYamlNodeValue(node *yaml.Node) interface{} {
	if node == nil {
		return nil
	}
	var value interface{}
	if err := node.Decode(&value); err != nil {
		return nil
	}
	return value
}

func decodeYamlNodeMap(node *yaml.Node) map[string]interface{} {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	var decoded map[string]interface{}
	if err := node.Decode(&decoded); err != nil {
		return nil
	}
	return decoded
}

// newClientProxyNodesYamlNode 把节点列表转成 YAML 序列节点。
//
// 通过 yaml 往返转换构造节点，避免手工拼装嵌套结构（reality-opts、ws-opts 等）。
func newClientProxyNodesYamlNode(nodes []map[string]interface{}) *yaml.Node {
	encoded, err := yaml.Marshal(nodes)
	if err != nil {
		return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	}
	var node yaml.Node
	if err := yaml.Unmarshal(encoded, &node); err != nil {
		return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	}
	if node.Kind != yaml.DocumentNode || len(node.Content) == 0 {
		return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	}
	sequence := node.Content[0]
	if sequence.Kind != yaml.SequenceNode {
		return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	}
	return sequence
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
