package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

type UIColorCustomConfig struct {
	TrafficBarEnabled                    bool     `json:"traffic_bar_enabled"`
	TrafficBarColor                      string   `json:"traffic_bar_color"`
	NoticeDialogIconBackgroundColor      string   `json:"notice_dialog_icon_background_color"`
	SubscriptionWebsiteIconColor         string   `json:"subscription_website_icon_color"`
	SubscriptionRefreshIconColor         string   `json:"subscription_refresh_icon_color"`
	SubscriptionNoticeIconColor          string   `json:"subscription_notice_icon_color"`
	LoginButtonColor                     string   `json:"login_button_color"`
	PlansSubscribeButtonColor            string   `json:"plans_subscribe_button_color"`
	PlansFilterTabColor                  string   `json:"plans_filter_tab_color"`
	InviteCodeTextColor                  string   `json:"invite_code_text_color"`
	InviteCodeBackgroundColor            string   `json:"invite_code_background_color"`
	CommissionBalanceCardBackgroundColor string   `json:"commission_balance_card_background_color"`
	InviteStatsTotalInvitesIconColor     string   `json:"invite_stats_total_invites_icon_color"`
	InviteStatsCommissionRateIconColor   string   `json:"invite_stats_commission_rate_icon_color"`
	InviteStatsTotalCommissionIconColor  string   `json:"invite_stats_total_commission_icon_color"`
	SubscriptionStatusPopupEnabled       bool     `json:"subscription_status_popup_enabled"`
	SubscriptionStatusOfficialURL        string   `json:"subscription_status_official_url"`
	ProxyGroupsMainPolicyNodesOnly       bool     `json:"proxy_groups_main_policy_nodes_only"`
	CloudDispatchEnabled                 bool     `json:"cloud_dispatch_enabled"`
	CloudDispatchQueryURL                string   `json:"cloud_dispatch_query_url"`
	CloudDispatchQuerySecret             string   `json:"cloud_dispatch_query_secret"`
	CloudDispatchTargetHost              string   `json:"cloud_dispatch_target_host"`
	CloudDispatchTargetHosts             []string `json:"cloud_dispatch_target_hosts"`
	CloudDispatchAutoEnabled             bool     `json:"cloud_dispatch_auto_enabled"`
	CloudDispatchAutoIntervalMinutes     int      `json:"cloud_dispatch_auto_interval_minutes"`
	CloudDispatchFallbackRetryMinutes    int      `json:"cloud_dispatch_fallback_retry_minutes"`
	SSPanelNodePageParseEnabled          bool     `json:"sspanel_node_page_parse_enabled"`
}

const (
	customFeatureTrafficBarColor                      = "traffic_bar_color"
	customFeatureNoticeDialogIconBackground           = "notice_dialog_icon_background_color"
	customFeatureSubscriptionWebsiteIconColor         = "subscription_website_icon_color"
	customFeatureSubscriptionRefreshIconColor         = "subscription_refresh_icon_color"
	customFeatureSubscriptionNoticeIconColor          = "subscription_notice_icon_color"
	customFeatureLoginButtonColor                     = "login_button_color"
	customFeaturePlansSubscribeButtonColor            = "plans_subscribe_button_color"
	customFeaturePlansFilterTabColor                  = "plans_filter_tab_color"
	customFeatureInviteCodeTextColor                  = "invite_code_text_color"
	customFeatureInviteCodeBackgroundColor            = "invite_code_background_color"
	customFeatureCommissionBalanceCardBackgroundColor = "commission_balance_card_background_color"
	customFeatureInviteStatsTotalInvitesIconColor     = "invite_stats_total_invites_icon_color"
	customFeatureInviteStatsCommissionRateIconColor   = "invite_stats_commission_rate_icon_color"
	customFeatureInviteStatsTotalCommissionIconColor  = "invite_stats_total_commission_icon_color"
	customFeatureSubscriptionStatusPopup              = "subscription_status_popup"
	customFeatureMainPolicyNodesOnly                  = "main_policy_nodes_only"
	customFeatureCloudDispatch                        = "cloud_dispatch"
	customFeatureSSPanelNodePageParse                 = "sspanel_node_page_parse"
)

var (
	uiColorPattern     = regexp.MustCompile(`^#(?:[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`)
	uiColorFeatureKeys = []string{
		customFeatureTrafficBarColor,
		customFeatureNoticeDialogIconBackground,
		customFeatureSubscriptionWebsiteIconColor,
		customFeatureSubscriptionRefreshIconColor,
		customFeatureSubscriptionNoticeIconColor,
		customFeatureLoginButtonColor,
		customFeaturePlansSubscribeButtonColor,
		customFeaturePlansFilterTabColor,
		customFeatureInviteCodeTextColor,
		customFeatureInviteCodeBackgroundColor,
		customFeatureCommissionBalanceCardBackgroundColor,
		customFeatureInviteStatsTotalInvitesIconColor,
		customFeatureInviteStatsCommissionRateIconColor,
		customFeatureInviteStatsTotalCommissionIconColor,
		customFeatureSubscriptionStatusPopup,
		customFeatureMainPolicyNodesOnly,
		customFeatureCloudDispatch,
		customFeatureSSPanelNodePageParse,
	}
)

func normalizeUIColorValue(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if !strings.HasPrefix(value, "#") {
		value = "#" + value
	}
	if !uiColorPattern.MatchString(value) {
		return "", fmt.Errorf("颜色格式错误，仅支持 #RRGGBB 或 #AARRGGBB")
	}
	return strings.ToUpper(value), nil
}

func normalizeOptionalHTTPURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if strings.ContainsAny(value, " \t\r\n") {
		return "", fmt.Errorf("官网跳转网址格式错误，仅支持 http:// 或 https://")
	}
	parsedURL, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("官网跳转网址格式错误，仅支持 http:// 或 https://")
	}
	scheme := strings.ToLower(strings.TrimSpace(parsedURL.Scheme))
	if (scheme != "http" && scheme != "https") || strings.TrimSpace(parsedURL.Host) == "" {
		return "", fmt.Errorf("官网跳转网址格式错误，仅支持 http:// 或 https://")
	}
	return value, nil
}

func normalizeCloudDispatchQueryURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if strings.ContainsAny(value, " \t\r\n") {
		return "", fmt.Errorf("GTM 服务地址格式错误，仅支持 http:// 或 https://")
	}
	parsedURL, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("GTM 服务地址格式错误，仅支持 http:// 或 https://")
	}
	scheme := strings.ToLower(strings.TrimSpace(parsedURL.Scheme))
	if (scheme != "http" && scheme != "https") || strings.TrimSpace(parsedURL.Host) == "" {
		return "", fmt.Errorf("GTM 服务地址格式错误，仅支持 http:// 或 https://")
	}
	normalized := url.URL{
		Scheme:   scheme,
		User:     parsedURL.User,
		Host:     parsedURL.Host,
		RawQuery: "",
		Fragment: "",
	}
	return normalized.String(), nil
}

func normalizeCloudDispatchTargetHost(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizeCloudDispatchTargetHosts(values []string) []string {
	return normalizeCloudDispatchTargetHostsOptional(values)
}

func normalizeCloudDispatchTargetHostsOptional(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		for _, part := range strings.FieldsFunc(value, func(r rune) bool {
			return r == ',' || r == '，' || r == '\n' || r == '\r' || r == '\t' || r == ' '
		}) {
			part = strings.ToLower(strings.TrimSpace(part))
			if part == "" {
				continue
			}
			if _, ok := seen[part]; ok {
				continue
			}
			seen[part] = struct{}{}
			result = append(result, part)
		}
	}
	return result
}

func firstCloudDispatchTargetHost(values []string) string {
	normalized := normalizeCloudDispatchTargetHosts(values)
	if len(normalized) == 0 {
		return ""
	}
	return normalized[0]
}

func readMapStringListValue(mapNode *yaml.Node, keys ...string) []string {
	for _, key := range keys {
		node := getMapValueNode(mapNode, key)
		if node == nil {
			continue
		}
		if node.Kind == yaml.SequenceNode {
			values := make([]string, 0, len(node.Content))
			for _, item := range node.Content {
				values = append(values, strings.TrimSpace(item.Value))
			}
			return normalizeCloudDispatchTargetHostsOptional(values)
		}
		return normalizeCloudDispatchTargetHostsOptional([]string{node.Value})
	}
	return []string{}
}

func newStringSequenceYamlNode(values []string) *yaml.Node {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, value := range normalizeCloudDispatchTargetHosts(values) {
		seq.Content = append(seq.Content, newStringYamlNode(value))
	}
	return seq
}

func normalizeCloudDispatchInterval(value int) int {
	if value < 1 {
		return 1
	}
	if value > 1440 {
		return 1440
	}
	return value
}

func normalizeCloudDispatchFallbackRetryMinutes(value int) int {
	if value < 1 {
		return 5
	}
	if value > 1440 {
		return 1440
	}
	return value
}

func hasCustomFeatureBinding(featureKey string) bool {
	featureKey = strings.TrimSpace(featureKey)
	if featureKey == "" {
		return false
	}
	for _, group := range getCustomFeatureGroups() {
		if strings.TrimSpace(group.IntegrationCode) == "" {
			continue
		}
		for _, currentFeatureKey := range group.FeatureKeys {
			if strings.TrimSpace(currentFeatureKey) == featureKey {
				return true
			}
		}
	}
	return false
}

func hasAnyCustomFeatureBinding(featureKeys ...string) bool {
	for _, featureKey := range featureKeys {
		if hasCustomFeatureBinding(featureKey) {
			return true
		}
	}
	return false
}

func findCustomFeatureGroupByCode(code, featureKey string) *CustomFeatureGroup {
	code = strings.TrimSpace(code)
	featureKey = strings.TrimSpace(featureKey)
	if code == "" {
		return nil
	}
	for _, group := range getCustomFeatureGroups() {
		if strings.TrimSpace(group.IntegrationCode) == "" {
			continue
		}
		if !secureCompareString(group.IntegrationCode, code) {
			continue
		}
		if featureKey == "" {
			matched := group
			return &matched
		}
		for _, currentFeatureKey := range group.FeatureKeys {
			if strings.TrimSpace(currentFeatureKey) == featureKey {
				matched := group
				return &matched
			}
		}
	}
	return nil
}

func filterAllowedUIColorFeatureKeys(featureKeys []string) []string {
	if len(featureKeys) == 0 {
		return []string{}
	}
	allowedMap := map[string]struct{}{}
	for _, featureKey := range uiColorFeatureKeys {
		allowedMap[featureKey] = struct{}{}
	}
	result := make([]string, 0, len(featureKeys))
	seen := map[string]struct{}{}
	for _, featureKey := range featureKeys {
		featureKey = strings.TrimSpace(featureKey)
		if featureKey == "" {
			continue
		}
		if _, ok := allowedMap[featureKey]; !ok {
			continue
		}
		if _, ok := seen[featureKey]; ok {
			continue
		}
		seen[featureKey] = struct{}{}
		result = append(result, featureKey)
	}
	return result
}

func isUIColorFeatureAllowed(featureKeys []string, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	for _, featureKey := range featureKeys {
		if strings.TrimSpace(featureKey) == target {
			return true
		}
	}
	return false
}

func readMapStringValue(mapNode *yaml.Node, keys ...string) string {
	for _, key := range keys {
		if node := getMapValueNode(mapNode, key); node != nil {
			return strings.TrimSpace(node.Value)
		}
	}
	return ""
}

func setOrRemoveMapStringValue(mapNode *yaml.Node, key, value string, legacyKeys ...string) {
	value = strings.TrimSpace(value)
	if value == "" {
		removeMapKeys(mapNode, append([]string{key}, legacyKeys...)...)
		return
	}
	setMapStringValue(mapNode, key, value)
	if len(legacyKeys) > 0 {
		removeMapKeys(mapNode, legacyKeys...)
	}
}

func readProfileUIColorCustomConfig(yamlContent string) (UIColorCustomConfig, error) {
	var result UIColorCustomConfig
	if strings.TrimSpace(yamlContent) == "" {
		return result, nil
	}

	doc, err := parseProfileYamlDocument(yamlContent)
	if err != nil {
		return result, err
	}

	root := ensureDocumentMappingNode(doc)
	xboard := getMapValueNode(root, "xboard")
	subscription := getMapValueNode(xboard, "subscription")
	if subscription != nil {
		if enabledNode := getMapValueNode(subscription, "sspanel_node_page_parse_enabled"); enabledNode != nil {
			result.SSPanelNodePageParseEnabled = strings.EqualFold(strings.TrimSpace(enabledNode.Value), "true")
		} else if enabledNode := getMapValueNode(subscription, "sspanelNodePageParseEnabled"); enabledNode != nil {
			result.SSPanelNodePageParseEnabled = strings.EqualFold(strings.TrimSpace(enabledNode.Value), "true")
		}
	}
	ui := getMapValueNode(xboard, "ui")
	subscriptionUsage := getMapValueNode(ui, "subscription_usage")
	if subscriptionUsage == nil {
		subscriptionUsage = getMapValueNode(ui, "subscriptionUsage")
	}
	if subscriptionUsage != nil {
		if enabledNode := getMapValueNode(subscriptionUsage, "traffic_bar_color_enabled"); enabledNode != nil {
			result.TrafficBarEnabled = strings.EqualFold(strings.TrimSpace(enabledNode.Value), "true")
		} else if enabledNode := getMapValueNode(subscriptionUsage, "trafficBarColorEnabled"); enabledNode != nil {
			result.TrafficBarEnabled = strings.EqualFold(strings.TrimSpace(enabledNode.Value), "true")
		}

		if colorNode := getMapValueNode(subscriptionUsage, "traffic_bar_color"); colorNode != nil {
			result.TrafficBarColor = strings.TrimSpace(colorNode.Value)
		} else if colorNode := getMapValueNode(subscriptionUsage, "trafficBarColor"); colorNode != nil {
			result.TrafficBarColor = strings.TrimSpace(colorNode.Value)
		}
	}

	customColors := getMapValueNode(ui, "custom_colors")
	if customColors == nil {
		customColors = getMapValueNode(ui, "customColors")
	}
	if customColors != nil {
		result.NoticeDialogIconBackgroundColor = readMapStringValue(customColors, "notice_dialog_icon_background_color", "noticeDialogIconBackgroundColor")
		result.SubscriptionWebsiteIconColor = readMapStringValue(customColors, "subscription_website_icon_color", "subscriptionWebsiteIconColor")
		result.SubscriptionRefreshIconColor = readMapStringValue(customColors, "subscription_refresh_icon_color", "subscriptionRefreshIconColor")
		result.SubscriptionNoticeIconColor = readMapStringValue(customColors, "subscription_notice_icon_color", "subscriptionNoticeIconColor")
		result.LoginButtonColor = readMapStringValue(customColors, "login_button_color", "loginButtonColor")
		result.PlansSubscribeButtonColor = readMapStringValue(customColors, "plans_subscribe_button_color", "plansSubscribeButtonColor")
		result.PlansFilterTabColor = readMapStringValue(customColors, "plans_filter_tab_color", "plansFilterTabColor")
		result.InviteCodeTextColor = readMapStringValue(customColors, "invite_code_text_color", "inviteCodeTextColor")
		result.InviteCodeBackgroundColor = readMapStringValue(customColors, "invite_code_background_color", "inviteCodeBackgroundColor")
		result.CommissionBalanceCardBackgroundColor = readMapStringValue(customColors, "commission_balance_card_background_color", "commissionBalanceCardBackgroundColor")
		result.InviteStatsTotalInvitesIconColor = readMapStringValue(customColors, "invite_stats_total_invites_icon_color", "inviteStatsTotalInvitesIconColor")
		result.InviteStatsCommissionRateIconColor = readMapStringValue(customColors, "invite_stats_commission_rate_icon_color", "inviteStatsCommissionRateIconColor")
		result.InviteStatsTotalCommissionIconColor = readMapStringValue(customColors, "invite_stats_total_commission_icon_color", "inviteStatsTotalCommissionIconColor")
	}

	subscriptionStatusPopup := getMapValueNode(ui, "subscription_status_popup")
	if subscriptionStatusPopup == nil {
		subscriptionStatusPopup = getMapValueNode(ui, "subscriptionStatusPopup")
	}
	if subscriptionStatusPopup != nil {
		if enabledNode := getMapValueNode(subscriptionStatusPopup, "enabled"); enabledNode != nil {
			result.SubscriptionStatusPopupEnabled = strings.EqualFold(strings.TrimSpace(enabledNode.Value), "true")
		}
		result.SubscriptionStatusOfficialURL = readMapStringValue(subscriptionStatusPopup, "official_url", "officialUrl")
	}

	proxyGroups := getMapValueNode(ui, "proxy_groups")
	if proxyGroups == nil {
		proxyGroups = getMapValueNode(ui, "proxyGroups")
	}
	if proxyGroups != nil {
		if enabledNode := getMapValueNode(proxyGroups, "main_policy_nodes_only"); enabledNode != nil {
			result.ProxyGroupsMainPolicyNodesOnly = strings.EqualFold(strings.TrimSpace(enabledNode.Value), "true")
		} else if enabledNode := getMapValueNode(proxyGroups, "mainPolicyNodesOnly"); enabledNode != nil {
			result.ProxyGroupsMainPolicyNodesOnly = strings.EqualFold(strings.TrimSpace(enabledNode.Value), "true")
		}
	}

	cloudDispatch := getMapValueNode(xboard, "cloud_dispatch")
	if cloudDispatch == nil {
		cloudDispatch = getMapValueNode(xboard, "cloudDispatch")
	}
	if cloudDispatch != nil {
		if enabledNode := getMapValueNode(cloudDispatch, "enabled"); enabledNode != nil {
			result.CloudDispatchEnabled = strings.EqualFold(strings.TrimSpace(enabledNode.Value), "true")
		}
		result.CloudDispatchQueryURL = readMapStringValue(cloudDispatch, "query_url", "queryUrl")
		result.CloudDispatchQuerySecret = readMapStringValue(cloudDispatch, "query_secret", "querySecret")
		if fallbackRetryNode := getMapValueNode(cloudDispatch, "fallback_retry_minutes"); fallbackRetryNode != nil {
			fmt.Sscanf(strings.TrimSpace(fallbackRetryNode.Value), "%d", &result.CloudDispatchFallbackRetryMinutes)
		} else if fallbackRetryNode := getMapValueNode(cloudDispatch, "fallbackRetryMinutes"); fallbackRetryNode != nil {
			fmt.Sscanf(strings.TrimSpace(fallbackRetryNode.Value), "%d", &result.CloudDispatchFallbackRetryMinutes)
		}
		result.CloudDispatchTargetHosts = readMapStringListValue(cloudDispatch, "target_hosts", "targetHosts")
		if len(result.CloudDispatchTargetHosts) == 0 {
			result.CloudDispatchTargetHosts = readMapStringListValue(cloudDispatch, "target_host", "targetHost")
		}
		auto := getMapValueNode(cloudDispatch, "auto")
		if auto != nil {
			if enabledNode := getMapValueNode(auto, "enabled"); enabledNode != nil {
				result.CloudDispatchAutoEnabled = strings.EqualFold(strings.TrimSpace(enabledNode.Value), "true")
			}
			if intervalNode := getMapValueNode(auto, "interval_minutes"); intervalNode != nil {
				fmt.Sscanf(strings.TrimSpace(intervalNode.Value), "%d", &result.CloudDispatchAutoIntervalMinutes)
			} else if intervalNode := getMapValueNode(auto, "intervalMinutes"); intervalNode != nil {
				fmt.Sscanf(strings.TrimSpace(intervalNode.Value), "%d", &result.CloudDispatchAutoIntervalMinutes)
			}
		}
	}
	result.CloudDispatchTargetHosts = normalizeCloudDispatchTargetHosts(result.CloudDispatchTargetHosts)
	result.CloudDispatchTargetHost = firstCloudDispatchTargetHost(result.CloudDispatchTargetHosts)
	result.CloudDispatchAutoIntervalMinutes = normalizeCloudDispatchInterval(result.CloudDispatchAutoIntervalMinutes)
	result.CloudDispatchFallbackRetryMinutes = normalizeCloudDispatchFallbackRetryMinutes(result.CloudDispatchFallbackRetryMinutes)

	return result, nil
}

func writeProfileUIColorCustomConfig(yamlContent string, config UIColorCustomConfig) (string, error) {
	if strings.TrimSpace(yamlContent) == "" {
		return "", fmt.Errorf("配置内容为空")
	}

	doc, err := parseProfileYamlDocument(yamlContent)
	if err != nil {
		return "", err
	}

	root := ensureDocumentMappingNode(doc)
	xboard := ensureMapValueNode(root, "xboard")
	subscription := ensureMapValueNode(xboard, "subscription")
	ui := ensureMapValueNode(xboard, "ui")
	subscriptionUsage := ensureMapValueNode(ui, "subscription_usage")
	customColors := ensureMapValueNode(ui, "custom_colors")
	subscriptionStatusPopup := ensureMapValueNode(ui, "subscription_status_popup")
	proxyGroups := ensureMapValueNode(ui, "proxy_groups")
	cloudDispatch := ensureMapValueNode(xboard, "cloud_dispatch")
	cloudDispatchAuto := ensureMapValueNode(cloudDispatch, "auto")

	setMapBoolValue(subscriptionUsage, "traffic_bar_color_enabled", config.TrafficBarEnabled)
	removeMapKeys(subscriptionUsage, "trafficBarColorEnabled")
	if strings.TrimSpace(config.TrafficBarColor) == "" {
		removeMapKeys(subscriptionUsage, "traffic_bar_color", "trafficBarColor")
	} else {
		setMapStringValue(subscriptionUsage, "traffic_bar_color", strings.TrimSpace(config.TrafficBarColor))
		removeMapKeys(subscriptionUsage, "trafficBarColor")
	}
	setOrRemoveMapStringValue(customColors, "notice_dialog_icon_background_color", config.NoticeDialogIconBackgroundColor, "noticeDialogIconBackgroundColor")
	setOrRemoveMapStringValue(customColors, "subscription_website_icon_color", config.SubscriptionWebsiteIconColor, "subscriptionWebsiteIconColor")
	setOrRemoveMapStringValue(customColors, "subscription_refresh_icon_color", config.SubscriptionRefreshIconColor, "subscriptionRefreshIconColor")
	setOrRemoveMapStringValue(customColors, "subscription_notice_icon_color", config.SubscriptionNoticeIconColor, "subscriptionNoticeIconColor")
	setOrRemoveMapStringValue(customColors, "login_button_color", config.LoginButtonColor, "loginButtonColor")
	setOrRemoveMapStringValue(customColors, "plans_subscribe_button_color", config.PlansSubscribeButtonColor, "plansSubscribeButtonColor")
	setOrRemoveMapStringValue(customColors, "plans_filter_tab_color", config.PlansFilterTabColor, "plansFilterTabColor")
	setOrRemoveMapStringValue(customColors, "invite_code_text_color", config.InviteCodeTextColor, "inviteCodeTextColor")
	setOrRemoveMapStringValue(customColors, "invite_code_background_color", config.InviteCodeBackgroundColor, "inviteCodeBackgroundColor")
	setOrRemoveMapStringValue(customColors, "commission_balance_card_background_color", config.CommissionBalanceCardBackgroundColor, "commissionBalanceCardBackgroundColor")
	setOrRemoveMapStringValue(customColors, "invite_stats_total_invites_icon_color", config.InviteStatsTotalInvitesIconColor, "inviteStatsTotalInvitesIconColor")
	setOrRemoveMapStringValue(customColors, "invite_stats_commission_rate_icon_color", config.InviteStatsCommissionRateIconColor, "inviteStatsCommissionRateIconColor")
	setOrRemoveMapStringValue(customColors, "invite_stats_total_commission_icon_color", config.InviteStatsTotalCommissionIconColor, "inviteStatsTotalCommissionIconColor")
	setMapBoolValue(subscriptionStatusPopup, "enabled", config.SubscriptionStatusPopupEnabled)
	setOrRemoveMapStringValue(subscriptionStatusPopup, "official_url", config.SubscriptionStatusOfficialURL, "officialUrl")
	removeMapKeys(ui, "subscriptionStatusPopup")
	setMapBoolValue(proxyGroups, "main_policy_nodes_only", config.ProxyGroupsMainPolicyNodesOnly)
	removeMapKeys(proxyGroups, "mainPolicyNodesOnly")
	removeMapKeys(ui, "proxyGroups")
	setMapBoolValue(cloudDispatch, "enabled", config.CloudDispatchEnabled)
	setOrRemoveMapStringValue(cloudDispatch, "query_url", config.CloudDispatchQueryURL, "queryUrl")
	setOrRemoveMapStringValue(cloudDispatch, "query_secret", config.CloudDispatchQuerySecret, "querySecret")
	setMapIntValue(cloudDispatch, "fallback_retry_minutes", normalizeCloudDispatchFallbackRetryMinutes(config.CloudDispatchFallbackRetryMinutes))
	setMapBoolValue(cloudDispatchAuto, "enabled", config.CloudDispatchAutoEnabled)
	setMapIntValue(cloudDispatchAuto, "interval_minutes", normalizeCloudDispatchInterval(config.CloudDispatchAutoIntervalMinutes))
	removeMapKeys(cloudDispatchAuto, "intervalMinutes")
	removeMapKeys(cloudDispatch, "target_host", "target_hosts", "queryUrl", "querySecret", "fallbackRetryMinutes", "targetHost", "targetHosts")
	removeMapKeys(xboard, "cloudDispatch")
	setMapBoolValue(subscription, "sspanel_node_page_parse_enabled", config.SSPanelNodePageParseEnabled)
	removeMapKeys(subscription, "sspanelNodePageParseEnabled")

	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(doc); err != nil {
		return "", err
	}
	_ = encoder.Close()

	return strings.TrimRight(buf.String(), "\n"), nil
}

func (h *Handlers) verifyUIColorConfigAccess(r *http.Request, profileName, integrationCode string) ([]string, error) {
	claims := getClaims(r)
	if !claims.canAccessProfile(profileName) {
		return nil, fmt.Errorf("无权操作该档案")
	}
	if claims.Permissions == "admin" {
		return append([]string{}, uiColorFeatureKeys...), nil
	}

	group := findCustomFeatureGroupByCode(integrationCode, "")
	if group == nil {
		if !hasAnyCustomFeatureBinding(uiColorFeatureKeys...) {
			return nil, fmt.Errorf("后台尚未为该功能配置对接码")
		}
		return nil, fmt.Errorf("对接码不正确")
	}
	allowed := filterAllowedUIColorFeatureKeys(group.FeatureKeys)
	if len(allowed) == 0 {
		return nil, fmt.Errorf("该对接码未绑定任何可用的自定义功能")
	}
	return allowed, nil
}

func (h *Handlers) GetPublicUIColorCustomConfig(w http.ResponseWriter, r *http.Request) {
	client := normalizeBuildClient(r.URL.Query().Get("client"))
	var req struct {
		Profile         string `json:"profile"`
		IntegrationCode string `json:"integration_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "请求格式错误", http.StatusBadRequest)
		return
	}

	profileName := strings.TrimSpace(req.Profile)
	if profileName == "" {
		jsonError(w, "请选择配置档案", http.StatusBadRequest)
		return
	}

	allowedFeatureKeys, err := h.verifyUIColorConfigAccess(r, profileName, req.IntegrationCode)
	if err != nil {
		jsonError(w, err.Error(), http.StatusForbidden)
		return
	}

	yamlContent, _, _, exists, err := h.getStoredProfileForClient(client, profileName)
	if err != nil {
		jsonError(w, "加载档案失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if !exists {
		jsonError(w, "档案不存在，请先创建并保存基础配置", http.StatusNotFound)
		return
	}

	config, err := readProfileUIColorCustomConfig(yamlContent)
	if err != nil {
		jsonError(w, "读取自定义颜色配置失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	jsonResponse(w, map[string]interface{}{
		"profile":                                  profileName,
		"feature_keys":                             allowedFeatureKeys,
		"traffic_bar_enabled":                      config.TrafficBarEnabled,
		"traffic_bar_color":                        config.TrafficBarColor,
		"notice_dialog_icon_background_color":      config.NoticeDialogIconBackgroundColor,
		"subscription_website_icon_color":          config.SubscriptionWebsiteIconColor,
		"subscription_refresh_icon_color":          config.SubscriptionRefreshIconColor,
		"subscription_notice_icon_color":           config.SubscriptionNoticeIconColor,
		"login_button_color":                       config.LoginButtonColor,
		"plans_subscribe_button_color":             config.PlansSubscribeButtonColor,
		"plans_filter_tab_color":                   config.PlansFilterTabColor,
		"invite_code_text_color":                   config.InviteCodeTextColor,
		"invite_code_background_color":             config.InviteCodeBackgroundColor,
		"commission_balance_card_background_color": config.CommissionBalanceCardBackgroundColor,
		"invite_stats_total_invites_icon_color":    config.InviteStatsTotalInvitesIconColor,
		"invite_stats_commission_rate_icon_color":  config.InviteStatsCommissionRateIconColor,
		"invite_stats_total_commission_icon_color": config.InviteStatsTotalCommissionIconColor,
		"subscription_status_popup_enabled":        config.SubscriptionStatusPopupEnabled,
		"subscription_status_official_url":         config.SubscriptionStatusOfficialURL,
		"proxy_groups_main_policy_nodes_only":      config.ProxyGroupsMainPolicyNodesOnly,
		"cloud_dispatch_enabled":                   config.CloudDispatchEnabled,
		"cloud_dispatch_query_url":                 config.CloudDispatchQueryURL,
		"cloud_dispatch_query_secret":              config.CloudDispatchQuerySecret,
		"cloud_dispatch_target_host":               config.CloudDispatchTargetHost,
		"cloud_dispatch_target_hosts":              config.CloudDispatchTargetHosts,
		"cloud_dispatch_auto_enabled":              config.CloudDispatchAutoEnabled,
		"cloud_dispatch_auto_interval_minutes":     config.CloudDispatchAutoIntervalMinutes,
		"cloud_dispatch_fallback_retry_minutes":    config.CloudDispatchFallbackRetryMinutes,
		"sspanel_node_page_parse_enabled":          config.SSPanelNodePageParseEnabled,
	})
}

func (h *Handlers) SavePublicUIColorCustomConfig(w http.ResponseWriter, r *http.Request) {
	client := normalizeBuildClient(r.URL.Query().Get("client"))
	var req struct {
		Profile                              string   `json:"profile"`
		IntegrationCode                      string   `json:"integration_code"`
		TrafficBarEnabled                    bool     `json:"traffic_bar_enabled"`
		TrafficBarColor                      string   `json:"traffic_bar_color"`
		NoticeDialogIconBackgroundColor      string   `json:"notice_dialog_icon_background_color"`
		SubscriptionWebsiteIconColor         string   `json:"subscription_website_icon_color"`
		SubscriptionRefreshIconColor         string   `json:"subscription_refresh_icon_color"`
		SubscriptionNoticeIconColor          string   `json:"subscription_notice_icon_color"`
		LoginButtonColor                     string   `json:"login_button_color"`
		PlansSubscribeButtonColor            string   `json:"plans_subscribe_button_color"`
		PlansFilterTabColor                  string   `json:"plans_filter_tab_color"`
		InviteCodeTextColor                  string   `json:"invite_code_text_color"`
		InviteCodeBackgroundColor            string   `json:"invite_code_background_color"`
		CommissionBalanceCardBackgroundColor string   `json:"commission_balance_card_background_color"`
		InviteStatsTotalInvitesIconColor     string   `json:"invite_stats_total_invites_icon_color"`
		InviteStatsCommissionRateIconColor   string   `json:"invite_stats_commission_rate_icon_color"`
		InviteStatsTotalCommissionIconColor  string   `json:"invite_stats_total_commission_icon_color"`
		SubscriptionStatusPopupEnabled       bool     `json:"subscription_status_popup_enabled"`
		SubscriptionStatusOfficialURL        string   `json:"subscription_status_official_url"`
		ProxyGroupsMainPolicyNodesOnly       bool     `json:"proxy_groups_main_policy_nodes_only"`
		CloudDispatchEnabled                 bool     `json:"cloud_dispatch_enabled"`
		CloudDispatchQueryURL                string   `json:"cloud_dispatch_query_url"`
		CloudDispatchQuerySecret             string   `json:"cloud_dispatch_query_secret"`
		CloudDispatchTargetHost              string   `json:"cloud_dispatch_target_host"`
		CloudDispatchTargetHosts             []string `json:"cloud_dispatch_target_hosts"`
		CloudDispatchAutoEnabled             bool     `json:"cloud_dispatch_auto_enabled"`
		CloudDispatchAutoIntervalMinutes     int      `json:"cloud_dispatch_auto_interval_minutes"`
		CloudDispatchFallbackRetryMinutes    int      `json:"cloud_dispatch_fallback_retry_minutes"`
		SSPanelNodePageParseEnabled          bool     `json:"sspanel_node_page_parse_enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "请求格式错误", http.StatusBadRequest)
		return
	}

	profileName := strings.TrimSpace(req.Profile)
	if profileName == "" {
		jsonError(w, "请选择配置档案", http.StatusBadRequest)
		return
	}

	allowedFeatureKeys, err := h.verifyUIColorConfigAccess(r, profileName, req.IntegrationCode)
	if err != nil {
		jsonError(w, err.Error(), http.StatusForbidden)
		return
	}

	normalizedTrafficBarColor, err := normalizeUIColorValue(req.TrafficBarColor)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	normalizedNoticeDialogIconBackgroundColor, err := normalizeUIColorValue(req.NoticeDialogIconBackgroundColor)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	normalizedSubscriptionWebsiteIconColor, err := normalizeUIColorValue(req.SubscriptionWebsiteIconColor)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	normalizedSubscriptionRefreshIconColor, err := normalizeUIColorValue(req.SubscriptionRefreshIconColor)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	normalizedSubscriptionNoticeIconColor, err := normalizeUIColorValue(req.SubscriptionNoticeIconColor)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	normalizedLoginButtonColor, err := normalizeUIColorValue(req.LoginButtonColor)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	normalizedPlansSubscribeButtonColor, err := normalizeUIColorValue(req.PlansSubscribeButtonColor)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	normalizedPlansFilterTabColor, err := normalizeUIColorValue(req.PlansFilterTabColor)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	normalizedInviteCodeTextColor, err := normalizeUIColorValue(req.InviteCodeTextColor)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	normalizedInviteCodeBackgroundColor, err := normalizeUIColorValue(req.InviteCodeBackgroundColor)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	normalizedCommissionBalanceCardBackgroundColor, err := normalizeUIColorValue(req.CommissionBalanceCardBackgroundColor)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	normalizedInviteStatsTotalInvitesIconColor, err := normalizeUIColorValue(req.InviteStatsTotalInvitesIconColor)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	normalizedInviteStatsCommissionRateIconColor, err := normalizeUIColorValue(req.InviteStatsCommissionRateIconColor)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	normalizedInviteStatsTotalCommissionIconColor, err := normalizeUIColorValue(req.InviteStatsTotalCommissionIconColor)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	normalizedSubscriptionStatusOfficialURL, err := normalizeOptionalHTTPURL(req.SubscriptionStatusOfficialURL)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	normalizedCloudDispatchQueryURL, err := normalizeCloudDispatchQueryURL(req.CloudDispatchQueryURL)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	normalizedCloudDispatchAutoIntervalMinutes := normalizeCloudDispatchInterval(req.CloudDispatchAutoIntervalMinutes)
	normalizedCloudDispatchFallbackRetryMinutes := normalizeCloudDispatchFallbackRetryMinutes(req.CloudDispatchFallbackRetryMinutes)
	if isUIColorFeatureAllowed(allowedFeatureKeys, customFeatureTrafficBarColor) && req.TrafficBarEnabled && normalizedTrafficBarColor == "" {
		jsonError(w, "开启自定义颜色时必须填写颜色值", http.StatusBadRequest)
		return
	}
	if isUIColorFeatureAllowed(allowedFeatureKeys, customFeatureSubscriptionStatusPopup) && req.SubscriptionStatusPopupEnabled && normalizedSubscriptionStatusOfficialURL == "" {
		jsonError(w, "开启登录窗口套餐状态拦截时必须填写官网跳转地址", http.StatusBadRequest)
		return
	}
	if isUIColorFeatureAllowed(allowedFeatureKeys, customFeatureCloudDispatch) && req.CloudDispatchEnabled {
		if normalizedCloudDispatchQueryURL == "" {
			jsonError(w, "开启云端调度时必须填写 GTM 服务地址", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(req.CloudDispatchQuerySecret) == "" {
			jsonError(w, "开启云端调度时必须填写查询密钥", http.StatusBadRequest)
			return
		}
	}

	yamlContent, _, _, exists, err := h.getStoredProfileForClient(client, profileName)
	if err != nil {
		jsonError(w, "加载档案失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if !exists {
		jsonError(w, "档案不存在，请先创建并保存基础配置", http.StatusNotFound)
		return
	}

	currentConfig, err := readProfileUIColorCustomConfig(yamlContent)
	if err != nil {
		jsonError(w, "读取现有配置失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	filePath, err := profileFilePath(profileName)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	targetConfig := currentConfig
	if isUIColorFeatureAllowed(allowedFeatureKeys, customFeatureTrafficBarColor) {
		targetConfig.TrafficBarEnabled = req.TrafficBarEnabled
		targetConfig.TrafficBarColor = normalizedTrafficBarColor
	}
	if isUIColorFeatureAllowed(allowedFeatureKeys, customFeatureNoticeDialogIconBackground) {
		targetConfig.NoticeDialogIconBackgroundColor = normalizedNoticeDialogIconBackgroundColor
	}
	if isUIColorFeatureAllowed(allowedFeatureKeys, customFeatureSubscriptionWebsiteIconColor) {
		targetConfig.SubscriptionWebsiteIconColor = normalizedSubscriptionWebsiteIconColor
	}
	if isUIColorFeatureAllowed(allowedFeatureKeys, customFeatureSubscriptionRefreshIconColor) {
		targetConfig.SubscriptionRefreshIconColor = normalizedSubscriptionRefreshIconColor
	}
	if isUIColorFeatureAllowed(allowedFeatureKeys, customFeatureSubscriptionNoticeIconColor) {
		targetConfig.SubscriptionNoticeIconColor = normalizedSubscriptionNoticeIconColor
	}
	if isUIColorFeatureAllowed(allowedFeatureKeys, customFeatureLoginButtonColor) {
		targetConfig.LoginButtonColor = normalizedLoginButtonColor
	}
	if isUIColorFeatureAllowed(allowedFeatureKeys, customFeaturePlansSubscribeButtonColor) {
		targetConfig.PlansSubscribeButtonColor = normalizedPlansSubscribeButtonColor
	}
	if isUIColorFeatureAllowed(allowedFeatureKeys, customFeaturePlansFilterTabColor) {
		targetConfig.PlansFilterTabColor = normalizedPlansFilterTabColor
	}
	if isUIColorFeatureAllowed(allowedFeatureKeys, customFeatureInviteCodeTextColor) {
		targetConfig.InviteCodeTextColor = normalizedInviteCodeTextColor
	}
	if isUIColorFeatureAllowed(allowedFeatureKeys, customFeatureInviteCodeBackgroundColor) {
		targetConfig.InviteCodeBackgroundColor = normalizedInviteCodeBackgroundColor
	}
	if isUIColorFeatureAllowed(allowedFeatureKeys, customFeatureCommissionBalanceCardBackgroundColor) {
		targetConfig.CommissionBalanceCardBackgroundColor = normalizedCommissionBalanceCardBackgroundColor
	}
	if isUIColorFeatureAllowed(allowedFeatureKeys, customFeatureInviteStatsTotalInvitesIconColor) {
		targetConfig.InviteStatsTotalInvitesIconColor = normalizedInviteStatsTotalInvitesIconColor
	}
	if isUIColorFeatureAllowed(allowedFeatureKeys, customFeatureInviteStatsCommissionRateIconColor) {
		targetConfig.InviteStatsCommissionRateIconColor = normalizedInviteStatsCommissionRateIconColor
	}
	if isUIColorFeatureAllowed(allowedFeatureKeys, customFeatureInviteStatsTotalCommissionIconColor) {
		targetConfig.InviteStatsTotalCommissionIconColor = normalizedInviteStatsTotalCommissionIconColor
	}
	if isUIColorFeatureAllowed(allowedFeatureKeys, customFeatureSubscriptionStatusPopup) {
		targetConfig.SubscriptionStatusPopupEnabled = req.SubscriptionStatusPopupEnabled
		targetConfig.SubscriptionStatusOfficialURL = normalizedSubscriptionStatusOfficialURL
	}
	if isUIColorFeatureAllowed(allowedFeatureKeys, customFeatureMainPolicyNodesOnly) {
		targetConfig.ProxyGroupsMainPolicyNodesOnly = req.ProxyGroupsMainPolicyNodesOnly
	}
	if isUIColorFeatureAllowed(allowedFeatureKeys, customFeatureCloudDispatch) {
		targetConfig.CloudDispatchEnabled = req.CloudDispatchEnabled
		targetConfig.CloudDispatchQueryURL = normalizedCloudDispatchQueryURL
		targetConfig.CloudDispatchQuerySecret = strings.TrimSpace(req.CloudDispatchQuerySecret)
		targetConfig.CloudDispatchTargetHost = ""
		targetConfig.CloudDispatchTargetHosts = []string{}
		targetConfig.CloudDispatchAutoEnabled = req.CloudDispatchAutoEnabled
		targetConfig.CloudDispatchAutoIntervalMinutes = normalizedCloudDispatchAutoIntervalMinutes
		targetConfig.CloudDispatchFallbackRetryMinutes = normalizedCloudDispatchFallbackRetryMinutes
	}
	if isUIColorFeatureAllowed(allowedFeatureKeys, customFeatureSSPanelNodePageParse) {
		targetConfig.SSPanelNodePageParseEnabled = req.SSPanelNodePageParseEnabled
	}

	var updatedYaml string
	var patchErr error
	if err := h.profileGitHubClient(client).SaveFileWithRetry(filePath, func(existing string) string {
		updated, updateErr := writeProfileUIColorCustomConfig(existing, targetConfig)
		patchErr = updateErr
		if updateErr != nil {
			return existing
		}
		updatedYaml = updated
		return updated
	}, "更新公共自定义颜色配置: "+profileName, 3); err != nil {
		jsonError(w, "保存公共配置失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if patchErr != nil {
		jsonError(w, "保存公共配置失败: "+patchErr.Error(), http.StatusInternalServerError)
		return
	}
	if strings.TrimSpace(updatedYaml) == "" {
		jsonError(w, "保存公共配置失败: 未生成有效配置内容", http.StatusInternalServerError)
		return
	}
	invalidateProfileCacheForClient(client)
	if err := validateYamlContent(updatedYaml); err != nil {
		jsonError(w, "保存后的配置校验失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	claims := getClaims(r)
	logAudit(claims.CodeID, claims.CodeName, "save_public_ui_color_config", fmt.Sprintf("%s|features=%d", profileName, len(allowedFeatureKeys)), r.RemoteAddr)

	jsonResponse(w, map[string]interface{}{
		"message":                                  "自定义功能配置已保存",
		"profile":                                  profileName,
		"feature_keys":                             allowedFeatureKeys,
		"traffic_bar_enabled":                      targetConfig.TrafficBarEnabled,
		"traffic_bar_color":                        targetConfig.TrafficBarColor,
		"notice_dialog_icon_background_color":      targetConfig.NoticeDialogIconBackgroundColor,
		"subscription_website_icon_color":          targetConfig.SubscriptionWebsiteIconColor,
		"subscription_refresh_icon_color":          targetConfig.SubscriptionRefreshIconColor,
		"subscription_notice_icon_color":           targetConfig.SubscriptionNoticeIconColor,
		"login_button_color":                       targetConfig.LoginButtonColor,
		"plans_subscribe_button_color":             targetConfig.PlansSubscribeButtonColor,
		"plans_filter_tab_color":                   targetConfig.PlansFilterTabColor,
		"invite_code_text_color":                   targetConfig.InviteCodeTextColor,
		"invite_code_background_color":             targetConfig.InviteCodeBackgroundColor,
		"commission_balance_card_background_color": targetConfig.CommissionBalanceCardBackgroundColor,
		"invite_stats_total_invites_icon_color":    targetConfig.InviteStatsTotalInvitesIconColor,
		"invite_stats_commission_rate_icon_color":  targetConfig.InviteStatsCommissionRateIconColor,
		"invite_stats_total_commission_icon_color": targetConfig.InviteStatsTotalCommissionIconColor,
		"subscription_status_popup_enabled":        targetConfig.SubscriptionStatusPopupEnabled,
		"subscription_status_official_url":         targetConfig.SubscriptionStatusOfficialURL,
		"proxy_groups_main_policy_nodes_only":      targetConfig.ProxyGroupsMainPolicyNodesOnly,
		"cloud_dispatch_enabled":                   targetConfig.CloudDispatchEnabled,
		"cloud_dispatch_query_url":                 targetConfig.CloudDispatchQueryURL,
		"cloud_dispatch_query_secret":              targetConfig.CloudDispatchQuerySecret,
		"cloud_dispatch_target_host":               targetConfig.CloudDispatchTargetHost,
		"cloud_dispatch_target_hosts":              targetConfig.CloudDispatchTargetHosts,
		"cloud_dispatch_auto_enabled":              targetConfig.CloudDispatchAutoEnabled,
		"cloud_dispatch_auto_interval_minutes":     targetConfig.CloudDispatchAutoIntervalMinutes,
		"cloud_dispatch_fallback_retry_minutes":    targetConfig.CloudDispatchFallbackRetryMinutes,
		"sspanel_node_page_parse_enabled":          targetConfig.SSPanelNodePageParseEnabled,
	})
}
