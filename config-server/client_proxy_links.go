package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// 固定的 WebSocket UA，保持生成结果可复现（上游 Clash.Meta 使用随机 UA）。
const shareLinkUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

var (
	shareLinkEncRaw = base64.RawStdEncoding
	shareLinkEnc    = base64.StdEncoding
)

// parseClientProxyShareLinks 把多行节点分享链接解析成 Mihomo 节点配置。
//
// 字段映射与上游 Clash.Meta 的 common/convert 保持一致，
// 因此同一个链接在面板里导入的结果与客户端自己解析订阅的结果相同。
// 返回解析成功的节点，以及被跳过的行说明（供页面提示，不阻断其它行）。
func parseClientProxyShareLinks(text string) ([]map[string]interface{}, []string) {
	lines := normalizeShareLinkLines(text)
	nodes := make([]map[string]interface{}, 0, len(lines))
	issues := make([]string, 0)
	names := make(map[string]int, len(lines))

	for index, line := range lines {
		scheme, body, found := strings.Cut(line, "://")
		if !found {
			issues = append(issues, fmt.Sprintf("第 %d 行不是有效的节点链接", index+1))
			continue
		}

		var (
			node map[string]interface{}
			err  error
		)
		switch strings.ToLower(strings.TrimSpace(scheme)) {
		case "vless":
			node, err = parseVShareLinkNode(names, line, "vless")
			if err == nil {
				node = applyVLessExtras(node, line)
			}
		case "vmess":
			node, err = parseVMessShareLinkNode(names, line, body)
		case "trojan":
			node, err = parseTrojanShareLinkNode(names, line)
		case "ss":
			node, err = parseShadowsocksShareLinkNode(names, line, body)
		case "ssr":
			node, err = parseShadowsocksRShareLinkNode(names, body)
		case "hysteria":
			node, err = parseHysteriaShareLinkNode(names, line)
		case "hysteria2", "hy2":
			node, err = parseHysteria2ShareLinkNode(names, line)
		case "tuic":
			node, err = parseTUICShareLinkNode(names, line)
		default:
			err = fmt.Errorf("暂不支持 %s:// 协议", strings.TrimSpace(scheme))
		}

		if err != nil {
			issues = append(issues, fmt.Sprintf("第 %d 行（%s://）导入失败：%s", index+1, strings.TrimSpace(scheme), err.Error()))
			continue
		}
		if node == nil {
			issues = append(issues, fmt.Sprintf("第 %d 行（%s://）导入失败：缺少必要字段", index+1, strings.TrimSpace(scheme)))
			continue
		}
		if readNodeString(node, "name") == "" {
			node["name"] = uniqueProxyName(names, fmt.Sprintf("内置节点 %d", len(nodes)+1))
		}
		nodes = append(nodes, node)
	}

	return nodes, issues
}

// normalizeShareLinkLines 拆分输入文本；支持整段被 Base64 编码的链接列表。
func normalizeShareLinkLines(text string) []string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil
	}
	if !strings.Contains(trimmed, "://") {
		if decoded, err := tryDecodeShareLinkBase64(trimmed); err == nil && strings.Contains(string(decoded), "://") {
			trimmed = string(decoded)
		}
	}

	rawLines := strings.Split(strings.ReplaceAll(trimmed, "\r\n", "\n"), "\n")
	lines := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

func tryDecodeShareLinkBase64(value string) ([]byte, error) {
	cleaned := strings.TrimSpace(value)
	buf, err := shareLinkEncRaw.DecodeString(cleaned)
	if err != nil {
		buf, err = shareLinkEnc.DecodeString(cleaned)
		if err != nil {
			buf, err = base64.RawURLEncoding.DecodeString(cleaned)
			if err != nil {
				buf, err = base64.URLEncoding.DecodeString(cleaned)
			}
		}
	}
	if err != nil {
		return nil, err
	}
	return buf, nil
}

// uniqueProxyName 与上游 convert.uniqueName 行为一致：重名时追加 -02、-03。
//
// 注意：上游会对空备注名也做去重（产生 ""、"-02" 这类名字），
// 面板里需要可读名称，因此空名不参与去重，由调用方补默认名后再走一次去重。
func uniqueProxyName(names map[string]int, name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if index, ok := names[name]; ok {
		index++
		names[name] = index
		return fmt.Sprintf("%s-%02d", name, index)
	}
	names[name] = 0
	return name
}

func splitShareLinkList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// readJSONBool 读取 JSON 节点字段里的布尔值，兼容 true / "1" / 1 等写法。
func readJSONBool(value interface{}) (bool, bool) {
	switch typed := value.(type) {
	case nil:
		return false, false
	case bool:
		return typed, true
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		if err != nil {
			return false, false
		}
		return parsed, true
	case float64:
		return typed != 0, true
	default:
		return false, false
	}
}

// readShareLinkBool 依次尝试多个参数名读取布尔开关。
//
// "跳过证书验证"在不同节点链接里写法不统一：hysteria/hysteria2 官方 URI 用 insecure，
// trojan-go 与 v2rayN/xray 用 allowInsecure，部分面板两种都会发。
// 按调用方给的顺序取第一个"链接里确实写了"的参数，值无效就继续看下一个；
// 都没写时返回 ok=false，由调用方决定是否省略该字段（Mihomo 默认不跳过验证）。
func readShareLinkBool(query url.Values, keys ...string) (bool, bool) {
	for _, key := range keys {
		value := strings.TrimSpace(query.Get(key))
		if value == "" {
			continue
		}
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			continue
		}
		return parsed, true
	}
	return false, false
}

// ==================== vless / vmess（Xray 标准） ====================

// parseVShareLinkNode 解析 Xray VMessAEAD / VLESS 分享链接标准。
// https://github.com/XTLS/Xray-core/discussions/716
func parseVShareLinkNode(names map[string]int, line, scheme string) (map[string]interface{}, error) {
	parsed, err := url.Parse(line)
	if err != nil {
		return nil, fmt.Errorf("链接格式错误")
	}
	if parsed.Hostname() == "" {
		return nil, fmt.Errorf("缺少服务器地址")
	}
	if parsed.Port() == "" {
		return nil, fmt.Errorf("缺少端口")
	}

	query := parsed.Query()
	node := map[string]interface{}{
		"name":   uniqueProxyName(names, parsed.Fragment),
		"type":   scheme,
		"server": parsed.Hostname(),
		"port":   normalizeNodePortValue(parsed.Port()),
		"uuid":   parsed.User.Username(),
		"udp":    true,
	}

	security := strings.ToLower(query.Get("security"))
	if strings.HasSuffix(security, "tls") || security == "reality" {
		node["tls"] = true
		if fingerprint := query.Get("fp"); fingerprint == "" {
			node["client-fingerprint"] = "chrome"
		} else {
			node["client-fingerprint"] = fingerprint
		}
		if alpn := splitShareLinkList(query.Get("alpn")); len(alpn) > 0 {
			node["alpn"] = alpn
		}
		if pcs := query.Get("pcs"); pcs != "" {
			node["fingerprint"] = pcs
		}
	}
	if sni := query.Get("sni"); sni != "" {
		node["servername"] = sni
	}
	if publicKey := query.Get("pbk"); publicKey != "" {
		node["reality-opts"] = map[string]interface{}{
			"public-key": publicKey,
			"short-id":   query.Get("sid"),
		}
	}
	// v2rayN/xray 用 allowInsecure，部分面板会改用 insecure；链接怎么写就怎么生效。
	if skipVerify, ok := readShareLinkBool(query, "allowInsecure", "insecure"); ok {
		node["skip-cert-verify"] = skipVerify
	}

	switch query.Get("packetEncoding") {
	case "none":
	case "packet":
		node["packet-addr"] = true
	default:
		node["xudp"] = true
	}

	applyVShareLinkTransport(node, query)
	return node, nil
}

// applyVShareLinkTransport 映射 Xray 传输层参数到 Mihomo 的 network / *-opts。
func applyVShareLinkTransport(node map[string]interface{}, query url.Values) {
	network := strings.ToLower(query.Get("type"))
	if network == "" {
		network = "tcp"
	}
	fakeType := strings.ToLower(query.Get("headerType"))
	if network == "tcp" && fakeType == "http" {
		network = "http"
	} else if network == "http" {
		network = "h2"
	}
	node["network"] = network

	switch network {
	case "tcp":
	case "http":
		httpOpts := map[string]interface{}{"path": []string{"/"}}
		headers := map[string]interface{}{}
		if host := query.Get("host"); host != "" {
			headers["Host"] = []string{host}
		}
		if method := query.Get("method"); method != "" {
			httpOpts["method"] = method
		}
		if path := query.Get("path"); path != "" {
			httpOpts["path"] = []string{path}
		}
		httpOpts["headers"] = headers
		node["http-opts"] = httpOpts
	case "h2":
		h2Opts := map[string]interface{}{"path": "/"}
		if path := query.Get("path"); path != "" {
			h2Opts["path"] = path
		}
		if host := query.Get("host"); host != "" {
			h2Opts["host"] = []string{host}
		}
		node["h2-opts"] = h2Opts
	case "ws", "httpupgrade":
		wsOpts := map[string]interface{}{
			"path": query.Get("path"),
			"headers": map[string]interface{}{
				"User-Agent": shareLinkUserAgent,
				"Host":       query.Get("host"),
			},
		}
		if earlyData := query.Get("ed"); earlyData != "" {
			switch network {
			case "ws":
				if size, err := strconv.Atoi(earlyData); err == nil {
					wsOpts["max-early-data"] = size
					wsOpts["early-data-header-name"] = "Sec-WebSocket-Protocol"
				}
			case "httpupgrade":
				wsOpts["v2ray-http-upgrade-fast-open"] = true
			}
		}
		if earlyDataHeader := query.Get("eh"); earlyDataHeader != "" {
			wsOpts["early-data-header-name"] = earlyDataHeader
		}
		node["ws-opts"] = wsOpts
	case "grpc":
		node["grpc-opts"] = map[string]interface{}{
			"grpc-service-name": query.Get("serviceName"),
		}
	case "xhttp":
		xhttpOpts := map[string]interface{}{}
		if path := query.Get("path"); path != "" {
			xhttpOpts["path"] = path
		}
		if host := query.Get("host"); host != "" {
			xhttpOpts["host"] = host
		}
		if mode := query.Get("mode"); mode != "" {
			xhttpOpts["mode"] = mode
		}
		if len(xhttpOpts) > 0 {
			node["xhttp-opts"] = xhttpOpts
		}
	}
}

func applyVLessExtras(node map[string]interface{}, line string) map[string]interface{} {
	parsed, err := url.Parse(line)
	if err != nil {
		return node
	}
	query := parsed.Query()
	if flow := query.Get("flow"); flow != "" {
		node["flow"] = strings.ToLower(flow)
	}
	if encryption := query.Get("encryption"); encryption != "" {
		node["encryption"] = encryption
	}
	return node
}

// ==================== vmess（V2RayN 风格） ====================

func parseVMessShareLinkNode(names map[string]int, line, body string) (map[string]interface{}, error) {
	decoded, err := tryDecodeShareLinkBase64(body)
	if err != nil {
		// 退化为 Xray VMessAEAD 分享链接
		node, vErr := parseVShareLinkNode(names, line, "vmess")
		if vErr != nil {
			return nil, vErr
		}
		node["alterId"] = 0
		node["cipher"] = "auto"
		if parsed, pErr := url.Parse(line); pErr == nil {
			if encryption := parsed.Query().Get("encryption"); encryption != "" {
				node["cipher"] = encryption
			}
		}
		return node, nil
	}

	values := make(map[string]interface{}, 20)
	if err := json.NewDecoder(bytes.NewReader(decoded)).Decode(&values); err != nil {
		return nil, fmt.Errorf("Base64 内容不是有效的 JSON")
	}
	remark, ok := values["ps"].(string)
	if !ok {
		return nil, fmt.Errorf("缺少节点名称（ps）")
	}

	node := map[string]interface{}{
		"name":             uniqueProxyName(names, remark),
		"type":             "vmess",
		"server":           values["add"],
		"port":             normalizeNodePortValue(values["port"]),
		"uuid":             values["id"],
		"udp":              true,
		"xudp":             true,
		"tls":              false,
		"skip-cert-verify": false,
		"cipher":           "auto",
	}
	if alterID, exists := values["aid"]; exists {
		node["alterId"] = alterID
	} else {
		node["alterId"] = 0
	}
	if cipher, ok := values["scy"].(string); ok && cipher != "" {
		node["cipher"] = cipher
	}
	if sni, ok := values["sni"].(string); ok && sni != "" {
		node["servername"] = sni
	}
	// V2RayN 的 JSON 没有标准的"跳过证书验证"字段，但部分面板会额外带上，
	// 带了就按链接写生效（值可能是 true 也可能是 "1"）。
	for _, key := range []string{"allowInsecure", "insecure"} {
		if skipVerify, ok := readJSONBool(values[key]); ok {
			node["skip-cert-verify"] = skipVerify
			break
		}
	}

	network, hasNetwork := values["net"].(string)
	if hasNetwork {
		network = strings.ToLower(network)
		if values["type"] == "http" {
			network = "http"
		} else if network == "http" {
			network = "h2"
		}
		node["network"] = network
	}

	if tls, ok := values["tls"].(string); ok {
		if strings.HasSuffix(strings.ToLower(tls), "tls") {
			node["tls"] = true
		}
		if alpn, ok := values["alpn"].(string); ok {
			if list := splitShareLinkList(alpn); len(list) > 0 {
				node["alpn"] = list
			}
		}
	}
	// fp 是链接里的 TLS 指纹，对应 Mihomo 的 client-fingerprint。
	if fingerprint, ok := values["fp"].(string); ok && fingerprint != "" {
		node["client-fingerprint"] = fingerprint
	}

	switch network {
	case "http":
		httpOpts := map[string]interface{}{"path": []string{"/"}}
		headers := map[string]interface{}{}
		if host, ok := values["host"].(string); ok && host != "" {
			headers["Host"] = []string{host}
		}
		if path, ok := values["path"].(string); ok && path != "" {
			httpOpts["path"] = []string{path}
		}
		httpOpts["headers"] = headers
		node["http-opts"] = httpOpts
	case "h2":
		h2Opts := map[string]interface{}{"path": "/"}
		if path, ok := values["path"].(string); ok && path != "" {
			h2Opts["path"] = path
		}
		if host, ok := values["host"].(string); ok && host != "" {
			h2Opts["host"] = []string{host}
		}
		node["h2-opts"] = h2Opts
	case "ws", "httpupgrade":
		path, _ := values["path"].(string)
		if path == "" {
			path = "/"
		}
		wsOpts := map[string]interface{}{"path": path}
		headers := map[string]interface{}{}
		if host, ok := values["host"].(string); ok && host != "" {
			headers["Host"] = host
		}
		if pathURL, err := url.Parse(path); err == nil && pathURL.RawQuery != "" {
			pathQuery := pathURL.Query()
			if earlyData := pathQuery.Get("ed"); earlyData != "" {
				if size, err := strconv.Atoi(earlyData); err == nil {
					switch network {
					case "ws":
						wsOpts["max-early-data"] = size
						wsOpts["early-data-header-name"] = "Sec-WebSocket-Protocol"
					case "httpupgrade":
						wsOpts["v2ray-http-upgrade-fast-open"] = true
					}
					pathQuery.Del("ed")
					pathURL.RawQuery = pathQuery.Encode()
					wsOpts["path"] = pathURL.String()
				}
			}
			if earlyDataHeader := pathQuery.Get("eh"); earlyDataHeader != "" {
				if _, exists := wsOpts["early-data-header-name"]; !exists {
					wsOpts["early-data-header-name"] = earlyDataHeader
				}
			}
		}
		wsOpts["headers"] = headers
		node["ws-opts"] = wsOpts
	case "grpc":
		node["grpc-opts"] = map[string]interface{}{"grpc-service-name": values["path"]}
	}

	if readNodeString(node, "server") == "" {
		return nil, fmt.Errorf("缺少服务器地址（add）")
	}
	if readNodeString(node, "uuid") == "" {
		return nil, fmt.Errorf("缺少用户 ID（id）")
	}
	if _, ok := readNodePort(node["port"]); !ok {
		return nil, fmt.Errorf("缺少端口（port）")
	}
	return node, nil
}

// ==================== trojan ====================

func parseTrojanShareLinkNode(names map[string]int, line string) (map[string]interface{}, error) {
	parsed, err := url.Parse(line)
	if err != nil {
		return nil, fmt.Errorf("链接格式错误")
	}
	if parsed.Hostname() == "" || parsed.Port() == "" {
		return nil, fmt.Errorf("缺少服务器地址或端口")
	}

	query := parsed.Query()
	node := map[string]interface{}{
		"name":     uniqueProxyName(names, parsed.Fragment),
		"type":     "trojan",
		"server":   parsed.Hostname(),
		"port":     normalizeNodePortValue(parsed.Port()),
		"password": parsed.User.Username(),
		"udp":      true,
	}
	// trojan-go 用 allowInsecure，也有面板发 insecure。
	if skipVerify, ok := readShareLinkBool(query, "allowInsecure", "insecure"); ok {
		node["skip-cert-verify"] = skipVerify
	}
	if sni := query.Get("sni"); sni != "" {
		node["sni"] = sni
	}
	if alpn := splitShareLinkList(query.Get("alpn")); len(alpn) > 0 {
		node["alpn"] = alpn
	}

	network := strings.ToLower(query.Get("type"))
	if network != "" {
		node["network"] = network
	}
	switch network {
	case "ws":
		node["ws-opts"] = map[string]interface{}{
			"path": query.Get("path"),
			"headers": map[string]interface{}{
				"User-Agent": shareLinkUserAgent,
			},
		}
	case "grpc":
		node["grpc-opts"] = map[string]interface{}{
			"grpc-service-name": query.Get("serviceName"),
		}
	}

	if fingerprint := query.Get("fp"); fingerprint == "" {
		node["client-fingerprint"] = "chrome"
	} else {
		node["client-fingerprint"] = fingerprint
	}
	if pcs := query.Get("pcs"); pcs != "" {
		node["fingerprint"] = pcs
	}
	return node, nil
}

// ==================== shadowsocks ====================

func parseShadowsocksShareLinkNode(names map[string]int, line, body string) (map[string]interface{}, error) {
	parsed, err := url.Parse(line)
	if err != nil {
		return nil, fmt.Errorf("链接格式错误")
	}
	name := uniqueProxyName(names, parsed.Fragment)

	if parsed.Port() == "" {
		// 旧格式：ss://base64(method:password@host:port)，直接解 body 可以避免
		// Base64 里出现 / 时被 url.Parse 当成路径截断。
		payload := body
		if cut := strings.IndexAny(payload, "#?"); cut >= 0 {
			payload = payload[:cut]
		}
		decoded, dErr := tryDecodeShareLinkBase64(payload)
		if dErr != nil {
			return nil, fmt.Errorf("无法解析 SS 主体内容")
		}
		parsed, err = url.Parse("ss://" + string(decoded))
		if err != nil {
			return nil, fmt.Errorf("SS 主体内容格式错误")
		}
		if parsed.Hostname() == "" || parsed.Port() == "" {
			return nil, fmt.Errorf("缺少服务器地址或端口")
		}
	}

	cipherRaw := parsed.User.Username()
	cipher := cipherRaw
	password, hasPassword := parsed.User.Password()
	if !hasPassword {
		if cipher, password, hasPassword = decodeShadowsocksUserInfo(cipherRaw); !hasPassword {
			return nil, fmt.Errorf("无法解析加密方式与密码")
		}
	}

	node := map[string]interface{}{
		"name":     name,
		"type":     "ss",
		"server":   parsed.Hostname(),
		"port":     normalizeNodePortValue(parsed.Port()),
		"cipher":   cipher,
		"password": password,
		"udp":      true,
	}

	query := parsed.Query()
	if query.Get("udp-over-tcp") == "true" || query.Get("uot") == "1" {
		node["udp-over-tcp"] = true
	}
	applyShadowsocksPlugin(node, query.Get("plugin"))
	return node, nil
}

func decodeShadowsocksUserInfo(value string) (string, string, bool) {
	candidates := []*base64.Encoding{base64.RawURLEncoding, shareLinkEnc, shareLinkEncRaw, base64.URLEncoding}
	for _, encoding := range candidates {
		decoded, err := encoding.DecodeString(value)
		if err != nil {
			continue
		}
		cipher, password, found := strings.Cut(string(decoded), ":")
		if found && cipher != "" {
			return cipher, password, true
		}
	}
	return "", "", false
}

func applyShadowsocksPlugin(node map[string]interface{}, plugin string) {
	if !strings.Contains(plugin, ";") {
		return
	}
	// 与上游一致：即使解析报错也使用已解析出的部分参数。
	pluginInfo, _ := url.ParseQuery("pluginName=" + strings.ReplaceAll(plugin, ";", "&"))
	pluginName := pluginInfo.Get("pluginName")
	switch {
	case strings.Contains(pluginName, "obfs"):
		node["plugin"] = "obfs"
		node["plugin-opts"] = map[string]interface{}{
			"mode": pluginInfo.Get("obfs"),
			"host": pluginInfo.Get("obfs-host"),
		}
	case strings.Contains(pluginName, "v2ray-plugin"):
		mode := pluginInfo.Get("mode")
		if mode == "" {
			mode = pluginInfo.Get("obfs")
		}
		host := pluginInfo.Get("host")
		if host == "" {
			host = pluginInfo.Get("obfs-host")
		}
		node["plugin"] = "v2ray-plugin"
		node["plugin-opts"] = map[string]interface{}{
			"mode": mode,
			"host": host,
			"path": pluginInfo.Get("path"),
			"tls":  strings.Contains(plugin, "tls"),
		}
	}
}

// ==================== shadowsocksr ====================

func parseShadowsocksRShareLinkNode(names map[string]int, body string) (map[string]interface{}, error) {
	decoded, err := tryDecodeShareLinkBase64(body)
	if err != nil {
		return nil, fmt.Errorf("Base64 主体内容无效")
	}

	before, after, ok := strings.Cut(string(decoded), "/?")
	if !ok {
		return nil, fmt.Errorf("缺少 SSR 参数分隔符")
	}
	segments := strings.Split(before, ":")
	if len(segments) != 6 {
		return nil, fmt.Errorf("SSR 主体字段数量不正确")
	}

	query, err := url.ParseQuery(ssrURLSafe(after))
	if err != nil {
		return nil, fmt.Errorf("SSR 参数格式错误")
	}

	node := map[string]interface{}{
		"name":     uniqueProxyName(names, ssrDecodeParam(query.Get("remarks"))),
		"type":     "ssr",
		"server":   segments[0],
		"port":     normalizeNodePortValue(segments[1]),
		"protocol": segments[2],
		"cipher":   segments[3],
		"obfs":     segments[4],
		"password": ssrDecodeParam(ssrURLSafe(segments[5])),
		"udp":      true,
	}
	if obfsParam := ssrDecodeParam(query.Get("obfsparam")); obfsParam != "" {
		node["obfs-param"] = obfsParam
	}
	if protocolParam := ssrDecodeParam(query.Get("protoparam")); protocolParam != "" {
		node["protocol-param"] = protocolParam
	}
	return node, nil
}

func ssrURLSafe(value string) string {
	return strings.NewReplacer("+", "-", "/", "_").Replace(value)
}

func ssrDecodeParam(value string) string {
	if value == "" {
		return ""
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return ""
	}
	return string(decoded)
}

// ==================== hysteria ====================

func parseHysteriaShareLinkNode(names map[string]int, line string) (map[string]interface{}, error) {
	parsed, err := url.Parse(line)
	if err != nil {
		return nil, fmt.Errorf("链接格式错误")
	}
	if parsed.Hostname() == "" || parsed.Port() == "" {
		return nil, fmt.Errorf("缺少服务器地址或端口")
	}

	query := parsed.Query()
	up := query.Get("up")
	if up == "" {
		up = query.Get("upmbps")
	}
	down := query.Get("down")
	if down == "" {
		down = query.Get("downmbps")
	}

	node := map[string]interface{}{
		"name":   uniqueProxyName(names, parsed.Fragment),
		"type":   "hysteria",
		"server": parsed.Hostname(),
		"port":   normalizeNodePortValue(parsed.Port()),
	}
	// 空值直接省略，交给 Mihomo 使用默认行为，避免档案里出现大量空字符串字段。
	if sni := query.Get("peer"); sni != "" {
		node["sni"] = sni
	}
	if obfs := query.Get("obfs"); obfs != "" {
		node["obfs"] = obfs
	}
	if auth := query.Get("auth"); auth != "" {
		node["auth_str"] = auth
	}
	if protocol := query.Get("protocol"); protocol != "" {
		node["protocol"] = protocol
	}
	if up != "" {
		node["up"] = up
	}
	if down != "" {
		node["down"] = down
	}
	if alpn := splitShareLinkList(query.Get("alpn")); len(alpn) > 0 {
		node["alpn"] = alpn
	}
	// hysteria 官方 URI 用 insecure；也有面板发 allowInsecure。
	if skipVerify, ok := readShareLinkBool(query, "insecure", "allowInsecure"); ok {
		node["skip-cert-verify"] = skipVerify
	}
	return node, nil
}

// ==================== hysteria2 ====================

func parseHysteria2ShareLinkNode(names map[string]int, line string) (map[string]interface{}, error) {
	hopLine, ports := splitHysteria2Ports(line)
	parsed, err := url.Parse(hopLine)
	if err != nil {
		return nil, fmt.Errorf("链接格式错误")
	}
	if parsed.Hostname() == "" {
		return nil, fmt.Errorf("缺少服务器地址")
	}

	query := parsed.Query()
	port := parsed.Port()
	if port == "" {
		port = "443"
	}

	node := map[string]interface{}{
		"name":   uniqueProxyName(names, parsed.Fragment),
		"type":   "hysteria2",
		"server": parsed.Hostname(),
		"port":   normalizeNodePortValue(port),
	}
	if ports != "" {
		node["ports"] = ports
	}
	if obfs := query.Get("obfs"); obfs != "" {
		node["obfs"] = obfs
	}
	if obfsPassword := query.Get("obfs-password"); obfsPassword != "" {
		node["obfs-password"] = obfsPassword
	}
	if sni := query.Get("sni"); sni != "" {
		node["sni"] = sni
	}
	if alpn := splitShareLinkList(query.Get("alpn")); len(alpn) > 0 {
		node["alpn"] = alpn
	}
	if pinSHA256 := query.Get("pinSHA256"); pinSHA256 != "" {
		node["fingerprint"] = pinSHA256
	}
	if up := query.Get("up"); up != "" {
		node["up"] = up
	}
	if down := query.Get("down"); down != "" {
		node["down"] = down
	}
	// hysteria2 官方 URI 用 insecure；也有面板发 allowInsecure。
	if skipVerify, ok := readShareLinkBool(query, "insecure", "allowInsecure"); ok {
		node["skip-cert-verify"] = skipVerify
	}
	if auth := parsed.User.String(); auth != "" {
		node["password"] = auth
	}
	return node, nil
}

// splitHysteria2Ports 处理 hysteria2://pass@host:1000-2000/?... 的端口段写法。
func splitHysteria2Ports(line string) (string, string) {
	index := strings.Index(line, "://")
	if index < 0 {
		return line, ""
	}
	head, rest := line[:index+3], line[index+3:]

	auth, tail := rest, ""
	if cut := strings.IndexAny(rest, "/?#"); cut >= 0 {
		auth, tail = rest[:cut], rest[cut:]
	}
	userinfo := ""
	if at := strings.LastIndex(auth, "@"); at >= 0 {
		userinfo, auth = auth[:at+1], auth[at+1:]
	}
	if strings.Contains(auth, "]") {
		return line, ""
	}
	colon := strings.LastIndex(auth, ":")
	if colon < 0 {
		return line, ""
	}
	host, ports := auth[:colon], auth[colon+1:]
	if !strings.ContainsAny(ports, ",-") {
		return line, ""
	}
	first := ports
	if cut := strings.IndexAny(ports, ",-"); cut >= 0 {
		first = ports[:cut]
	}
	if first == "" {
		return line, ""
	}
	return head + userinfo + host + ":" + first + tail, ports
}

// ==================== tuic ====================

func parseTUICShareLinkNode(names map[string]int, line string) (map[string]interface{}, error) {
	parsed, err := url.Parse(line)
	if err != nil {
		return nil, fmt.Errorf("链接格式错误")
	}
	if parsed.Hostname() == "" || parsed.Port() == "" {
		return nil, fmt.Errorf("缺少服务器地址或端口")
	}

	query := parsed.Query()
	node := map[string]interface{}{
		"name":   uniqueProxyName(names, parsed.Fragment),
		"type":   "tuic",
		"server": parsed.Hostname(),
		"port":   normalizeNodePortValue(parsed.Port()),
		"udp":    true,
	}
	if password, isV5 := parsed.User.Password(); isV5 {
		node["uuid"] = parsed.User.Username()
		node["password"] = password
	} else {
		node["token"] = parsed.User.Username()
	}
	if congestion := query.Get("congestion_control"); congestion != "" {
		node["congestion-controller"] = congestion
	}
	if alpn := splitShareLinkList(query.Get("alpn")); len(alpn) > 0 {
		node["alpn"] = alpn
	}
	if sni := query.Get("sni"); sni != "" {
		node["sni"] = sni
	}
	if query.Get("disable_sni") == "1" {
		node["disable-sni"] = true
	}
	if udpRelayMode := query.Get("udp_relay_mode"); udpRelayMode != "" {
		node["udp-relay-mode"] = udpRelayMode
	}
	return node, nil
}

// normalizeNodePortValue 把端口归一化成数字，便于档案 YAML 保持可读。
func normalizeNodePortValue(value interface{}) interface{} {
	if port, ok := readNodePort(value); ok {
		return port
	}
	return value
}
