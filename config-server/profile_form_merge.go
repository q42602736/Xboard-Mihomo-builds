package main

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type ProfileFormState struct {
	Provider                          string                    `json:"provider"`
	AppTitle                          string                    `json:"app_title"`
	LogoType                          string                    `json:"logo_type"`
	LogoImageURL                      string                    `json:"logo_image_url"`
	AppIconURL                        string                    `json:"app_icon_url"`
	AuthBackgroundEnabled             bool                      `json:"auth_background_enabled"`
	AuthBackgroundImageURL            string                    `json:"auth_background_image_url"`
	PreferEncrypt                     bool                      `json:"prefer_encrypt"`
	SubscriptionUserAgent             string                    `json:"user_agent"`
	SubscriptionExclusiveUserAgent    string                    `json:"exclusive_user_agent"`
	SubscriptionCustomQuerySuffix     string                    `json:"custom_query_suffix"`
	UseExclusiveMode                  bool                      `json:"use_exclusive_mode"`
	DecryptKey                        string                    `json:"decrypt_key"`
	AutoOfflineEnabled                bool                      `json:"auto_offline_enabled"`
	AutoOfflineForceOnStartup         bool                      `json:"auto_offline_force_on_startup"`
	AutoOfflineIntervalHours          int                       `json:"auto_offline_interval_hours"`
	CloudDispatchEnabled              bool                      `json:"cloud_dispatch_enabled"`
	CloudDispatchQueryURL             string                    `json:"cloud_dispatch_query_url"`
	CloudDispatchQuerySecret          string                    `json:"cloud_dispatch_query_secret"`
	CloudDispatchTargetHost           string                    `json:"cloud_dispatch_target_host"`
	CloudDispatchTargetHosts          []string                  `json:"cloud_dispatch_target_hosts"`
	CloudDispatchAutoEnabled          bool                      `json:"cloud_dispatch_auto_enabled"`
	CloudDispatchAutoInterval         int                       `json:"cloud_dispatch_auto_interval_minutes"`
	CloudDispatchFallbackRetry        int                       `json:"cloud_dispatch_fallback_retry_minutes"`
	DNSOverrideDefault                bool                      `json:"dns_override_default"`
	AutoConnectOnStartup              bool                      `json:"auto_connect_on_startup"`
	LogFileEnabled                    bool                      `json:"log_file_enabled"`
	PanelAPIPathPrefix                string                    `json:"api_path_prefix"`
	RegistrationInviteEnabled         bool                      `json:"registration_invite_enabled"`
	RegistrationInviteMode            string                    `json:"registration_invite_mode"`
	RegistrationInviteCode            string                    `json:"registration_invite_code"`
	RegistrationInviteLinkEnabled     bool                      `json:"registration_invite_link_enabled"`
	RegistrationInviteLinkBaseURL     string                    `json:"registration_invite_link_base_url"`
	SubscriptionCacheEnabled          bool                      `json:"subscription_cache_enabled"`
	SubscriptionCacheTTL              int                       `json:"subscription_cache_ttl"`
	UiVariant                         string                    `json:"ui_variant"`
	UiColorScheme                     string                    `json:"ui_color_scheme"`
	HideTrafficDetails                bool                      `json:"hide_traffic_details"`
	HideNodeStatus                    bool                      `json:"hide_node_status"`
	HideInvitePromotion               bool                      `json:"hide_invite_promotion"`
	HideCurrentNodeLabel              bool                      `json:"hide_current_node_label"`
	HidePageHeaderText                bool                      `json:"hide_page_header_text"`
	HidePlanSpeed                     bool                      `json:"hide_plan_speed"`
	ShowIPInfo                        *bool                     `json:"show_ip_info"`
	HomePanelDefaultLayout            string                    `json:"home_panel_default_layout"`
	LatencyReductionEnabled           bool                      `json:"latency_reduction_enabled"`
	LatencyReductionValue             int                       `json:"latency_reduction_value"`
	NoticeAutoOpenOnStartup           bool                      `json:"notice_auto_open_on_startup"`
	NoticeAutoOpenIntervalHours       *int                      `json:"notice_auto_open_interval_hours"`
	CheckinShowButton                 bool                      `json:"checkin_show_button"`
	GiftCardShowButton                bool                      `json:"gift_card_show_button"`
	TelegramShowButton                bool                      `json:"telegram_show_button"`
	TelegramURL                       string                    `json:"telegram_url"`
	UtilitySpeedShowButton            bool                      `json:"utility_speed_show_button"`
	UtilityCfSpeedShowButton          bool                      `json:"utility_cf_speed_show_button"`
	UtilityCfSpeedTargetDomains       []string                  `json:"utility_cf_speed_target_domains"`
	UtilityCfSpeedAutoReplaceEnabled  bool                      `json:"utility_cf_speed_auto_replace_enabled"`
	UtilityCfSpeedAutoReplaceInterval int                       `json:"utility_cf_speed_auto_replace_interval_minutes"`
	UtilityIPLookupShowButton         bool                      `json:"utility_ip_lookup_show_button"`
	UtilityMediaUnlockShowButton      bool                      `json:"utility_media_unlock_show_button"`
	UtilityGoogleServicesShowButton   bool                      `json:"utility_google_services_show_button"`
	UtilityPopularAppsShowSection     bool                      `json:"utility_popular_apps_show_section"`
	UtilityPopularApps                []ProfilePopularAppState  `json:"utility_popular_apps"`
	ShowCustomRuleEntry               bool                      `json:"show_custom_rule_entry"`
	AuthPagesSupportShowButton        bool                      `json:"auth_pages_support_show_button"`
	Sources                           []ProfileSourceFormState  `json:"sources"`
	OnlineSupportItems                []ProfileSupportFormState `json:"online_support_items"`
}

type ProfileSourceFormState struct {
	MergeKey      string `json:"merge_key,omitempty"`
	Name          string `json:"name"`
	URL           string `json:"url"`
	EncryptionKey string `json:"encryption_key"`
}

type ProfileSupportFormState struct {
	MergeKey     string `json:"merge_key,omitempty"`
	Type         string `json:"type"`
	Description  string `json:"description"`
	URL          string `json:"url"`
	WebsiteID    string `json:"website_id"`
	ChatraID     string `json:"chatra_id"`
	WidgetCode   string `json:"widget_code"`
	WidgetID     string `json:"widget_id"`
	PropertyID   string `json:"property_id"`
	WebsiteToken string `json:"website_token"`
	BaseURL      string `json:"base_url"`
}

type ProfilePopularAppState struct {
	MergeKey    string `json:"merge_key,omitempty"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	IconURL     string `json:"icon_url"`
	DownloadURL string `json:"download_url"`
	ShowButton  bool   `json:"show_button"`
}

func mergeProfileYamlWithForm(baseYaml string, form ProfileFormState) (string, error) {
	return mergeProfileYamlWithFormForRoot(baseYaml, form, "xboard")
}

func mergeNexGenProfileYamlWithForm(baseYaml string, form ProfileFormState) (string, error) {
	return mergeProfileYamlWithFormForRoot(baseYaml, form, "nexgen")
}

func mergeProfileYamlWithFormForRoot(baseYaml string, form ProfileFormState, rootKey string) (string, error) {
	baseYaml = strings.ReplaceAll(baseYaml, "config_cache:", "subscription_cache:")

	doc, err := parseProfileYamlDocument(baseYaml)
	if err != nil {
		return "", err
	}

	root := ensureDocumentMappingNode(doc)
	profileRoot := ensureMapValueNode(root, rootKey)
	app := ensureMapValueNode(profileRoot, "app")
	logo := ensureMapValueNode(app, "logo")
	appIcon := ensureMapValueNode(app, "app_icon")
	authBackground := ensureMapValueNode(app, "auth_background")
	subscription := ensureMapValueNode(profileRoot, "subscription")
	settings := ensureMapValueNode(profileRoot, "settings")
	autoOffline := ensureMapValueNode(profileRoot, "auto_offline")
	cloudDispatch := ensureMapValueNode(profileRoot, "cloud_dispatch")
	cloudDispatchAuto := ensureMapValueNode(cloudDispatch, "auto")
	registrationInvite := ensureMapValueNode(profileRoot, "registration_invite")
	subscriptionCache := ensureMapValueNode(profileRoot, "subscription_cache")
	ui := ensureMapValueNode(profileRoot, "ui")
	latencyReduction := ensureMapValueNode(ui, "latency_reduction")
	notice := ensureMapValueNode(ui, "notice")
	checkin := ensureMapValueNode(ui, "checkin")
	giftCard := ensureMapValueNode(ui, "gift_card")
	utilities := ensureMapValueNode(ui, "utilities")
	utilityTools := ensureMapValueNode(utilities, "tools")
	utilitySpeed := ensureMapValueNode(utilityTools, "speed")
	utilityIPLookup := ensureMapValueNode(utilityTools, "ip_lookup")
	utilityMediaUnlock := ensureMapValueNode(utilityTools, "media_unlock")
	utilityGoogleServices := ensureMapValueNode(utilityTools, "google_services")
	utilityPopularApps := ensureMapValueNode(utilities, "popular_apps")
	proxyGroups := ensureMapValueNode(ui, "proxy_groups")
	uiOnlineSupport := ensureMapValueNode(ui, "online_support")
	authPages := ensureMapValueNode(uiOnlineSupport, "auth_pages")
	remoteConfig := ensureMapValueNode(profileRoot, "remote_config")
	onlineSupport := ensureMapValueNode(profileRoot, "online_support")

	setMapStringValue(profileRoot, "provider", strings.TrimSpace(form.Provider))
	setMapStringValue(profileRoot, "title", strings.TrimSpace(form.AppTitle))
	setMapStringValue(app, "title", strings.TrimSpace(form.AppTitle))
	setMapStringValue(logo, "type", strings.TrimSpace(form.LogoType))
	setMapStringValue(logo, "image_url", strings.TrimSpace(form.LogoImageURL))
	setMapStringValue(appIcon, "image_url", strings.TrimSpace(form.AppIconURL))
	setMapBoolValue(authBackground, "enabled", form.AuthBackgroundEnabled)
	setMapStringValue(authBackground, "image_url", strings.TrimSpace(form.AuthBackgroundImageURL))
	setMapBoolValue(subscription, "prefer_encrypt", form.PreferEncrypt)
	setMapStringValue(subscription, "user_agent", strings.TrimSpace(form.SubscriptionUserAgent))
	setMapStringValue(subscription, "exclusive_user_agent", strings.TrimSpace(form.SubscriptionExclusiveUserAgent))
	if strings.TrimSpace(form.SubscriptionCustomQuerySuffix) == "" {
		removeMapKeys(subscription, "custom_query_suffix")
	} else {
		setMapStringValue(subscription, "custom_query_suffix", strings.TrimSpace(form.SubscriptionCustomQuerySuffix))
	}
	setMapBoolValue(subscription, "use_exclusive_mode", form.UseExclusiveMode)
	setMapStringValue(subscription, "decrypt_key", form.DecryptKey)
	if rootKey == "nexgen" {
		setMapBoolValue(settings, "dns_override_default", form.DNSOverrideDefault)
		setMapBoolValue(settings, "auto_connect_on_startup", form.AutoConnectOnStartup)
		setMapBoolValue(settings, "log_file_enabled", form.LogFileEnabled)
		removeMapKeys(settings, "dnsOverrideDefault", "autoConnectOnStartup", "logFileEnabled")
	} else {
		removeMapKeys(profileRoot, "settings")
	}
	setMapBoolValue(autoOffline, "enabled", form.AutoOfflineEnabled)
	setMapBoolValue(autoOffline, "force_on_startup", form.AutoOfflineForceOnStartup)
	setMapIntValue(autoOffline, "auto_enter_interval_hours", normalizeAutoOfflineIntervalHours(form.AutoOfflineIntervalHours))
	setMapBoolValue(cloudDispatch, "enabled", form.CloudDispatchEnabled)
	normalizedCloudDispatchQueryURL, err := normalizeCloudDispatchQueryURL(form.CloudDispatchQueryURL)
	if err != nil {
		return "", err
	}
	if normalizedCloudDispatchQueryURL == "" {
		removeMapKeys(cloudDispatch, "query_url")
	} else {
		setMapStringValue(cloudDispatch, "query_url", normalizedCloudDispatchQueryURL)
	}
	if strings.TrimSpace(form.CloudDispatchQuerySecret) == "" {
		removeMapKeys(cloudDispatch, "query_secret")
	} else {
		setMapStringValue(cloudDispatch, "query_secret", strings.TrimSpace(form.CloudDispatchQuerySecret))
	}
	setMapIntValue(cloudDispatch, "fallback_retry_minutes", normalizeCloudDispatchIntervalValue(form.CloudDispatchFallbackRetry))
	setMapBoolValue(cloudDispatchAuto, "enabled", form.CloudDispatchAutoEnabled)
	setMapIntValue(cloudDispatchAuto, "interval_minutes", normalizeCloudDispatchIntervalValue(form.CloudDispatchAutoInterval))
	removeMapKeys(cloudDispatch, "target_host", "target_hosts", "queryUrl", "querySecret", "fallbackRetryMinutes", "targetHost", "targetHosts")
	removeMapKeys(cloudDispatchAuto, "intervalMinutes")
	removeMapKeys(profileRoot, "cloudDispatch")
	normalizedRegistrationInviteCode, err := normalizeRegistrationInviteCode(form.RegistrationInviteCode)
	if err != nil {
		return "", err
	}
	if form.RegistrationInviteEnabled && normalizedRegistrationInviteCode == "" {
		return "", fmt.Errorf("开启注册邀请绑定时必须填写邀请码")
	}
	setMapBoolValue(registrationInvite, "enabled", form.RegistrationInviteEnabled)
	setMapStringValue(registrationInvite, "mode", normalizeRegistrationInviteMode(form.RegistrationInviteMode))
	if normalizedRegistrationInviteCode == "" {
		removeMapKeys(registrationInvite, "invite_code")
	} else {
		setMapStringValue(registrationInvite, "invite_code", normalizedRegistrationInviteCode)
	}
	if rootKey == "nexgen" {
		normalizedRegistrationInviteLinkBaseURL, err := normalizeRegistrationInviteLinkBaseURL(form.RegistrationInviteLinkBaseURL)
		if err != nil {
			return "", err
		}
		if form.RegistrationInviteLinkEnabled && normalizedRegistrationInviteLinkBaseURL == "" {
			return "", fmt.Errorf("开启复制完整邀请链接时必须填写注册地址根地址")
		}
		setMapBoolValue(registrationInvite, "link_enabled", form.RegistrationInviteLinkEnabled)
		if normalizedRegistrationInviteLinkBaseURL == "" {
			removeMapKeys(registrationInvite, "link_base_url")
		} else {
			setMapStringValue(registrationInvite, "link_base_url", normalizedRegistrationInviteLinkBaseURL)
		}
	} else {
		removeMapKeys(registrationInvite, "link_enabled", "link_base_url")
	}
	removeMapKeys(registrationInvite, "inviteCode", "invite_link", "inviteLink", "linkEnabled", "linkBaseUrl", "invite_link_enabled", "inviteLinkEnabled", "invite_link_base_url", "inviteLinkBaseUrl")
	removeMapKeys(profileRoot, "registrationInvite")
	setMapBoolValue(subscriptionCache, "enabled", form.SubscriptionCacheEnabled)
	setMapIntValue(subscriptionCache, "ttl_hours", form.SubscriptionCacheTTL)
	if rootKey == "nexgen" {
		uiVariant := strings.TrimSpace(form.UiVariant)
		if uiVariant == "" {
			if existingVariant := getMapValueNode(ui, "variant"); existingVariant != nil {
				uiVariant = existingVariant.Value
			}
		}
		setMapStringValue(ui, "variant", normalizeNexGenUiVariant(uiVariant))
		uiColorScheme := strings.TrimSpace(form.UiColorScheme)
		if uiColorScheme == "" {
			if existingColorScheme := getMapValueNode(ui, "color_scheme"); existingColorScheme != nil {
				uiColorScheme = existingColorScheme.Value
			}
		}
		setMapStringValue(ui, "color_scheme", normalizeNexGenUiColorScheme(uiColorScheme))
	} else {
		removeMapKeys(ui, "variant", "uiVariant", "color_scheme", "colorScheme")
	}
	setMapBoolValue(ui, "hide_traffic_details", form.HideTrafficDetails)
	setMapBoolValue(ui, "hide_node_status", form.HideNodeStatus)
	if rootKey == "nexgen" {
		setMapBoolValue(ui, "hide_invite_promotion", form.HideInvitePromotion)
	} else {
		removeMapKeys(ui, "hide_invite_promotion", "hideInvitePromotion")
	}
	setMapBoolValue(ui, "hide_current_node_label", form.HideCurrentNodeLabel)
	setMapBoolValue(ui, "hide_page_header_text", form.HidePageHeaderText)
	setMapBoolValue(ui, "hide_plan_speed", form.HidePlanSpeed)
	showIPInfo := true
	if form.ShowIPInfo != nil {
		showIPInfo = *form.ShowIPInfo
	}
	setMapBoolValue(ui, "show_ip_info", showIPInfo)
	homePanelDefaultLayout := strings.TrimSpace(form.HomePanelDefaultLayout)
	if homePanelDefaultLayout == "" {
		homePanelDefaultLayout = "default"
	}
	setMapStringValue(ui, "home_panel_default_layout", homePanelDefaultLayout)
	setMapBoolValue(latencyReduction, "enabled", form.LatencyReductionEnabled)
	setMapIntValue(latencyReduction, "value", normalizeLatencyReductionValue(form.LatencyReductionValue))
	setMapBoolValue(notice, "auto_open_on_startup", form.NoticeAutoOpenOnStartup)
	if form.NoticeAutoOpenIntervalHours != nil {
		setMapIntValue(notice, "auto_open_interval_hours", normalizeNoticeAutoOpenIntervalHours(*form.NoticeAutoOpenIntervalHours))
	}
	setMapBoolValue(checkin, "show_button", form.CheckinShowButton)
	setMapBoolValue(giftCard, "show_button", form.GiftCardShowButton)
	if rootKey == "nexgen" {
		telegram := ensureMapValueNode(ui, "telegram")
		setMapBoolValue(telegram, "show_button", form.TelegramShowButton)
		setMapStringValue(telegram, "url", strings.TrimSpace(form.TelegramURL))
	}
	setMapBoolValue(utilitySpeed, "show_button", form.UtilitySpeedShowButton)
	if rootKey == "nexgen" {
		utilityCfSpeed := ensureMapValueNode(utilityTools, "cf_speed")
		utilityCfSpeedAutoReplace := ensureMapValueNode(utilityCfSpeed, "auto_replace")
		setMapBoolValue(utilityCfSpeed, "show_button", form.UtilityCfSpeedShowButton)
		setMapNodeValue(utilityCfSpeed, "target_domains", newStringSequenceYamlNode(normalizeStringList(form.UtilityCfSpeedTargetDomains)))
		setMapBoolValue(utilityCfSpeedAutoReplace, "enabled", form.UtilityCfSpeedAutoReplaceEnabled)
		setMapIntValue(utilityCfSpeedAutoReplace, "interval_minutes", normalizeCloudDispatchIntervalValue(form.UtilityCfSpeedAutoReplaceInterval))
		removeMapKeys(utilityCfSpeed, "showButton", "targetDomains", "autoReplace")
		removeMapKeys(utilityCfSpeedAutoReplace, "intervalMinutes")
	} else {
		removeMapKeys(utilityTools, "cf_speed", "cfSpeed")
	}
	setMapBoolValue(utilityIPLookup, "show_button", form.UtilityIPLookupShowButton)
	setMapBoolValue(utilityMediaUnlock, "show_button", form.UtilityMediaUnlockShowButton)
	setMapBoolValue(utilityGoogleServices, "show_button", form.UtilityGoogleServicesShowButton)
	setMapBoolValue(utilityPopularApps, "show_section", form.UtilityPopularAppsShowSection)
	setMapNodeValue(utilityPopularApps, "items", mergeProfilePopularApps(getSequenceValueNode(utilityPopularApps, "items"), form.UtilityPopularApps))
	setMapBoolValue(proxyGroups, "show_custom_rule_entry", form.ShowCustomRuleEntry)
	setMapBoolValue(authPages, "show_button", form.AuthPagesSupportShowButton)
	removeMapKeys(profileRoot, "proxy_groups", "proxyGroups")
	removeMapKeys(onlineSupport, "auth_pages", "authPages")

	setMapStringValue(remoteConfig, "api_path_prefix", normalizePanelAPIPathPrefix(form.PanelAPIPathPrefix))
	removeMapKeys(remoteConfig, "apiPathPrefix")
	setMapNodeValue(remoteConfig, "sources", mergeProfileSources(getSequenceValueNode(remoteConfig, "sources"), form.Sources))
	setMapNodeValue(onlineSupport, "items", mergeProfileSupportItems(getSequenceValueNode(onlineSupport, "items"), form.OnlineSupportItems))

	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(doc); err != nil {
		return "", err
	}
	_ = encoder.Close()

	return strings.TrimRight(buf.String(), "\n"), nil
}

func parseProfileYamlDocument(content string) (*yaml.Node, error) {
	if strings.TrimSpace(content) == "" {
		return &yaml.Node{
			Kind: yaml.DocumentNode,
			Content: []*yaml.Node{{
				Kind: yaml.MappingNode,
				Tag:  "!!map",
			}},
		}, nil
	}

	var doc yaml.Node
	decoder := yaml.NewDecoder(strings.NewReader(content))
	decoder.KnownFields(false)
	if err := decoder.Decode(&doc); err != nil {
		return nil, err
	}
	if doc.Kind == 0 {
		doc.Kind = yaml.DocumentNode
	}
	if doc.Kind != yaml.DocumentNode {
		return nil, fmt.Errorf("配置根节点必须是 YAML 文档")
	}
	if len(doc.Content) == 0 || doc.Content[0] == nil {
		doc.Content = []*yaml.Node{{
			Kind: yaml.MappingNode,
			Tag:  "!!map",
		}}
	}
	return &doc, nil
}

func ensureDocumentMappingNode(doc *yaml.Node) *yaml.Node {
	if len(doc.Content) == 0 || doc.Content[0] == nil {
		doc.Content = []*yaml.Node{{
			Kind: yaml.MappingNode,
			Tag:  "!!map",
		}}
	}
	if doc.Content[0].Kind != yaml.MappingNode {
		doc.Content[0] = &yaml.Node{
			Kind: yaml.MappingNode,
			Tag:  "!!map",
		}
	}
	if doc.Content[0].Tag == "" {
		doc.Content[0].Tag = "!!map"
	}
	return doc.Content[0]
}

func ensureMapValueNode(parent *yaml.Node, key string) *yaml.Node {
	if parent == nil || parent.Kind != yaml.MappingNode {
		return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	}
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			valueNode := parent.Content[i+1]
			if valueNode.Kind != yaml.MappingNode {
				valueNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
				parent.Content[i+1] = valueNode
			}
			if valueNode.Tag == "" {
				valueNode.Tag = "!!map"
			}
			return valueNode
		}
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	valueNode := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	parent.Content = append(parent.Content, keyNode, valueNode)
	return valueNode
}

func getMapValueNode(parent *yaml.Node, key string) *yaml.Node {
	if parent == nil || parent.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			return parent.Content[i+1]
		}
	}
	return nil
}

func getSequenceValueNode(parent *yaml.Node, key string) *yaml.Node {
	valueNode := getMapValueNode(parent, key)
	if valueNode == nil || valueNode.Kind != yaml.SequenceNode {
		return nil
	}
	return valueNode
}

func setMapNodeValue(parent *yaml.Node, key string, valueNode *yaml.Node) {
	if parent == nil || parent.Kind != yaml.MappingNode {
		return
	}
	if valueNode == nil {
		valueNode = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: ""}
	}
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			parent.Content[i+1] = valueNode
			return
		}
	}
	parent.Content = append(parent.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		valueNode,
	)
}

func setMapStringValue(parent *yaml.Node, key, value string) {
	setMapNodeValue(parent, key, newStringYamlNode(value))
}

func setMapBoolValue(parent *yaml.Node, key string, value bool) {
	boolValue := "false"
	if value {
		boolValue = "true"
	}
	setMapNodeValue(parent, key, &yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   "!!bool",
		Value: boolValue,
	})
}

func setMapIntValue(parent *yaml.Node, key string, value int) {
	setMapNodeValue(parent, key, &yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   "!!int",
		Value: strconv.Itoa(value),
	})
}

func normalizeLatencyReductionValue(value int) int {
	if value < 0 {
		return 0
	}
	if value > 90 {
		return 90
	}
	return value
}

func normalizeNexGenUiVariant(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "legacy":
		return "legacy"
	case "new":
		return "new"
	default:
		return "legacy"
	}
}

func normalizeNexGenUiColorScheme(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "green", "blue", "purple", "orange", "teal", "cyan", "rose", "indigo":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "green"
	}
}

func normalizeNoticeAutoOpenIntervalHours(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

func normalizeAutoOfflineIntervalHours(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

func normalizeCloudDispatchIntervalValue(value int) int {
	if value < 1 {
		return 1
	}
	if value > 1440 {
		return 1440
	}
	return value
}

func normalizeStringList(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func newStringYamlNode(value string) *yaml.Node {
	node := &yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   "!!str",
		Value: value,
	}
	if strings.Contains(value, "\n") {
		node.Style = yaml.LiteralStyle
	}
	return node
}

func removeMapKeys(parent *yaml.Node, keys ...string) {
	if parent == nil || parent.Kind != yaml.MappingNode || len(keys) == 0 {
		return
	}
	keySet := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		keySet[key] = struct{}{}
	}

	newContent := make([]*yaml.Node, 0, len(parent.Content))
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if _, exists := keySet[parent.Content[i].Value]; exists {
			continue
		}
		newContent = append(newContent, parent.Content[i], parent.Content[i+1])
	}
	parent.Content = newContent
}

func cloneYamlNode(node *yaml.Node) *yaml.Node {
	if node == nil {
		return nil
	}
	cloned := *node
	if len(node.Content) > 0 {
		cloned.Content = make([]*yaml.Node, len(node.Content))
		for i, child := range node.Content {
			cloned.Content[i] = cloneYamlNode(child)
		}
	}
	return &cloned
}

func pickExistingSequenceItem(seq *yaml.Node, mergeKey string, fallbackIndex int) *yaml.Node {
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return nil
	}

	if idx, err := strconv.Atoi(strings.TrimSpace(mergeKey)); err == nil && idx >= 0 && idx < len(seq.Content) {
		return cloneYamlNode(seq.Content[idx])
	}
	if fallbackIndex >= 0 && fallbackIndex < len(seq.Content) {
		return cloneYamlNode(seq.Content[fallbackIndex])
	}
	return nil
}

func mergeProfileSources(existing *yaml.Node, sources []ProfileSourceFormState) *yaml.Node {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for index, source := range sources {
		itemNode := pickExistingSequenceItem(existing, source.MergeKey, index)
		if itemNode == nil || itemNode.Kind != yaml.MappingNode {
			itemNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		}
		setMapStringValue(itemNode, "name", normalizeProfileSourceName(source.Name))
		setMapStringValue(itemNode, "url", strings.TrimSpace(source.URL))
		if strings.TrimSpace(source.EncryptionKey) == "" {
			removeMapKeys(itemNode, "encryption_key")
		} else {
			setMapStringValue(itemNode, "encryption_key", strings.TrimSpace(source.EncryptionKey))
		}
		seq.Content = append(seq.Content, itemNode)
	}
	return seq
}

func mergeProfileSupportItems(existing *yaml.Node, items []ProfileSupportFormState) *yaml.Node {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for index, item := range items {
		itemNode := pickExistingSequenceItem(existing, item.MergeKey, index)
		if itemNode == nil || itemNode.Kind != yaml.MappingNode {
			itemNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		}

		removeMapKeys(itemNode,
			"url",
			"website_id",
			"chatra_id",
			"widget_code",
			"widget_id",
			"property_id",
			"website_token",
			"base_url",
			"description",
		)

		setMapStringValue(itemNode, "type", strings.TrimSpace(item.Type))
		if strings.TrimSpace(item.Description) != "" {
			setMapStringValue(itemNode, "description", strings.TrimSpace(item.Description))
		}

		switch strings.TrimSpace(item.Type) {
		case "browser":
			setMapStringValue(itemNode, "url", strings.TrimSpace(item.URL))
		case "crisp":
			setMapStringValue(itemNode, "website_id", strings.TrimSpace(item.WebsiteID))
		case "chatra":
			if strings.TrimSpace(item.ChatraID) != "" {
				setMapStringValue(itemNode, "chatra_id", strings.TrimSpace(item.ChatraID))
			}
			if strings.TrimSpace(item.WidgetCode) != "" {
				setMapStringValue(itemNode, "widget_code", item.WidgetCode)
			}
		case "chatway":
			setMapStringValue(itemNode, "widget_id", strings.TrimSpace(item.WidgetID))
		case "tawkto":
			setMapStringValue(itemNode, "property_id", strings.TrimSpace(item.PropertyID))
			setMapStringValue(itemNode, "widget_id", strings.TrimSpace(item.WidgetID))
		case "chatwoot":
			setMapStringValue(itemNode, "website_token", strings.TrimSpace(item.WebsiteToken))
			setMapStringValue(itemNode, "base_url", strings.TrimSpace(item.BaseURL))
		}

		seq.Content = append(seq.Content, itemNode)
	}
	return seq
}

func mergeProfilePopularApps(existing *yaml.Node, items []ProfilePopularAppState) *yaml.Node {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for index, item := range items {
		itemNode := pickExistingSequenceItem(existing, item.MergeKey, index)
		if itemNode == nil || itemNode.Kind != yaml.MappingNode {
			itemNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		}

		id := strings.TrimSpace(item.ID)
		if id == "" {
			id = fmt.Sprintf("app-%d", index+1)
		}

		setMapStringValue(itemNode, "id", id)
		setMapStringValue(itemNode, "name", strings.TrimSpace(item.Name))
		setMapStringValue(itemNode, "description", strings.TrimSpace(item.Description))
		setMapStringValue(itemNode, "icon_url", strings.TrimSpace(item.IconURL))
		setMapStringValue(itemNode, "download_url", strings.TrimSpace(item.DownloadURL))
		setMapBoolValue(itemNode, "show_button", item.ShowButton)

		seq.Content = append(seq.Content, itemNode)
	}
	return seq
}

func normalizeProfileSourceName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "local":
		return "local"
	case "gitee":
		return "gitee"
	case "redirect":
		return "redirect"
	default:
		return "redirect"
	}
}

func readLegacyBool(parent *yaml.Node, key string, legacy bool) (bool, bool) {
	node := getMapValueNode(parent, key)
	if node == nil {
		return false, legacy
	}
	switch node.Tag {
	case "!!bool":
		return node.Value == "true", true
	default:
		v := strings.TrimSpace(strings.ToLower(node.Value))
		if v == "true" {
			return true, true
		}
		if v == "false" {
			return false, true
		}
	}
	return false, legacy
}

func readLegacyString(parent *yaml.Node, key string, legacy bool) (string, bool) {
	node := getMapValueNode(parent, key)
	if node == nil {
		return "", legacy
	}
	return node.Value, true
}
