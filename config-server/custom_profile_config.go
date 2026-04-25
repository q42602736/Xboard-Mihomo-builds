package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

type UIColorCustomConfig struct {
	TrafficBarEnabled                    bool   `json:"traffic_bar_enabled"`
	TrafficBarColor                      string `json:"traffic_bar_color"`
	NoticeDialogIconBackgroundColor      string `json:"notice_dialog_icon_background_color"`
	SubscriptionWebsiteIconColor         string `json:"subscription_website_icon_color"`
	SubscriptionRefreshIconColor         string `json:"subscription_refresh_icon_color"`
	SubscriptionNoticeIconColor          string `json:"subscription_notice_icon_color"`
	LoginButtonColor                     string `json:"login_button_color"`
	PlansSubscribeButtonColor            string `json:"plans_subscribe_button_color"`
	PlansFilterTabColor                  string `json:"plans_filter_tab_color"`
	InviteCodeTextColor                  string `json:"invite_code_text_color"`
	InviteCodeBackgroundColor            string `json:"invite_code_background_color"`
	CommissionBalanceCardBackgroundColor string `json:"commission_balance_card_background_color"`
	InviteStatsTotalInvitesIconColor     string `json:"invite_stats_total_invites_icon_color"`
	InviteStatsCommissionRateIconColor   string `json:"invite_stats_commission_rate_icon_color"`
	InviteStatsTotalCommissionIconColor  string `json:"invite_stats_total_commission_icon_color"`
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
	ui := ensureMapValueNode(xboard, "ui")
	subscriptionUsage := ensureMapValueNode(ui, "subscription_usage")
	customColors := ensureMapValueNode(ui, "custom_colors")

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

	yamlContent, _, _, exists, err := h.getStoredProfile(profileName)
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
	})
}

func (h *Handlers) SavePublicUIColorCustomConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Profile                              string `json:"profile"`
		IntegrationCode                      string `json:"integration_code"`
		TrafficBarEnabled                    bool   `json:"traffic_bar_enabled"`
		TrafficBarColor                      string `json:"traffic_bar_color"`
		NoticeDialogIconBackgroundColor      string `json:"notice_dialog_icon_background_color"`
		SubscriptionWebsiteIconColor         string `json:"subscription_website_icon_color"`
		SubscriptionRefreshIconColor         string `json:"subscription_refresh_icon_color"`
		SubscriptionNoticeIconColor          string `json:"subscription_notice_icon_color"`
		LoginButtonColor                     string `json:"login_button_color"`
		PlansSubscribeButtonColor            string `json:"plans_subscribe_button_color"`
		PlansFilterTabColor                  string `json:"plans_filter_tab_color"`
		InviteCodeTextColor                  string `json:"invite_code_text_color"`
		InviteCodeBackgroundColor            string `json:"invite_code_background_color"`
		CommissionBalanceCardBackgroundColor string `json:"commission_balance_card_background_color"`
		InviteStatsTotalInvitesIconColor     string `json:"invite_stats_total_invites_icon_color"`
		InviteStatsCommissionRateIconColor   string `json:"invite_stats_commission_rate_icon_color"`
		InviteStatsTotalCommissionIconColor  string `json:"invite_stats_total_commission_icon_color"`
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
	if isUIColorFeatureAllowed(allowedFeatureKeys, customFeatureTrafficBarColor) && req.TrafficBarEnabled && normalizedTrafficBarColor == "" {
		jsonError(w, "开启自定义颜色时必须填写颜色值", http.StatusBadRequest)
		return
	}

	yamlContent, _, _, exists, err := h.getStoredProfile(profileName)
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

	var updatedYaml string
	var patchErr error
	if err := h.profileGH.SaveFileWithRetry(filePath, func(existing string) string {
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
	if err := validateYamlContent(updatedYaml); err != nil {
		jsonError(w, "保存后的配置校验失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	claims := getClaims(r)
	logAudit(claims.CodeID, claims.CodeName, "save_public_ui_color_config", fmt.Sprintf("%s|features=%d", profileName, len(allowedFeatureKeys)), r.RemoteAddr)

	jsonResponse(w, map[string]interface{}{
		"message":                                  "自定义颜色配置已保存",
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
	})
}
