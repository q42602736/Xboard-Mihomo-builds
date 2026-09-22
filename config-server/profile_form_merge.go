package main

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ProfileFormState 是页面表单提交的配置。
//
// 两个域名列表字段用指针：为 nil 表示页面没有提交该字段（旧版页面缓存），
// 此时保持档案原有内容不动，避免保存时误清空已经配置好的值。
type ProfileFormState struct {
	Provider                           string                   `json:"provider"`
	AppTitle                           string                   `json:"app_title"`
	LogoType                           string                   `json:"logo_type"`
	LogoImageURL                       string                   `json:"logo_image_url"`
	AppIconURL                         string                   `json:"app_icon_url"`
	AuthBackgroundEnabled              bool                     `json:"auth_background_enabled"`
	AuthBackgroundImageURL             string                   `json:"auth_background_image_url"`
	PreferEncrypt                      bool                     `json:"prefer_encrypt"`
	SubscriptionUserAgent              string                   `json:"user_agent"`
	SubscriptionExclusiveUserAgent     string                   `json:"exclusive_user_agent"`
	SubscriptionCustomDomain           string                   `json:"custom_domain"`
	SubscriptionCustomSubscribeDomain  string                   `json:"custom_subscribe_domain"`
	SubscriptionCustomQuerySuffix      string                   `json:"custom_query_suffix"`
	UseExclusiveMode                   bool                     `json:"use_exclusive_mode"`
	DecryptKey                         string                   `json:"decrypt_key"`
	APIEncryptedUserAgent              *string                  `json:"api_encrypted_user_agent"`
	AutoOfflineEnabled                 bool                     `json:"auto_offline_enabled"`
	AutoOfflineForceOnStartup          bool                     `json:"auto_offline_force_on_startup"`
	AutoOfflineIntervalHours           int                      `json:"auto_offline_interval_hours"`
	CloudDispatchEnabled               bool                     `json:"cloud_dispatch_enabled"`
	CloudDispatchQueryURL              string                   `json:"cloud_dispatch_query_url"`
	CloudDispatchQuerySecret           string                   `json:"cloud_dispatch_query_secret"`
	CloudDispatchTargetHost            string                   `json:"cloud_dispatch_target_host"`
	CloudDispatchTargetHosts           []string                 `json:"cloud_dispatch_target_hosts"`
	CloudDispatchAutoEnabled           bool                     `json:"cloud_dispatch_auto_enabled"`
	CloudDispatchAutoInterval          int                      `json:"cloud_dispatch_auto_interval_minutes"`
	CloudDispatchFallbackRetry         int                      `json:"cloud_dispatch_fallback_retry_minutes"`
	DNSOverrideDefault                 bool                     `json:"dns_override_default"`
	AutoTestAfterLogin                 bool                     `json:"auto_test_after_login"`
	AutoConnectOnStartup               bool                     `json:"auto_connect_on_startup"`
	LogFileEnabled                     bool                     `json:"log_file_enabled"`
	PanelAPIPathPrefix                 string                   `json:"api_path_prefix"`
	RegistrationInviteEnabled          bool                     `json:"registration_invite_enabled"`
	RegistrationInviteMode             string                   `json:"registration_invite_mode"`
	RegistrationInviteCode             string                   `json:"registration_invite_code"`
	RegistrationInviteLinkEnabled      bool                     `json:"registration_invite_link_enabled"`
	RegistrationInviteLinkBaseURL      string                   `json:"registration_invite_link_base_url"`
	SubscriptionCacheEnabled           bool                     `json:"subscription_cache_enabled"`
	SubscriptionCacheTTL               int                      `json:"subscription_cache_ttl"`
	UiVariant                          string                   `json:"ui_variant"`
	UiColorScheme                      string                   `json:"ui_color_scheme"`
	HideColorSchemeButton              bool                     `json:"hide_color_scheme_button"`
	HideOnlineSupportButton            bool                     `json:"hide_online_support_button"`
	HideTrafficDetails                 bool                     `json:"hide_traffic_details"`
	HideNodeStatus                     bool                     `json:"hide_node_status"`
	HideInvitePromotion                bool                     `json:"hide_invite_promotion"`
	HideDedicatedNodes                 bool                     `json:"hide_dedicated_nodes"`
	HideCurrentNodeLabel               bool                     `json:"hide_current_node_label"`
	HidePageHeaderText                 bool                     `json:"hide_page_header_text"`
	HidePurchaseCoupon                 *bool                    `json:"hide_purchase_coupon"`
	HidePlanSpeed                      bool                     `json:"hide_plan_speed"`
	ShowIPInfo                         *bool                    `json:"show_ip_info"`
	HomePanelDefaultLayout             string                   `json:"home_panel_default_layout"`
	LatencyReductionEnabled            bool                     `json:"latency_reduction_enabled"`
	LatencyReductionValue              int                      `json:"latency_reduction_value"`
	NoticeAutoOpenOnStartup            bool                     `json:"notice_auto_open_on_startup"`
	NoticeAutoOpenIntervalHours        *int                     `json:"notice_auto_open_interval_hours"`
	CheckinShowButton                  bool                     `json:"checkin_show_button"`
	GiftCardShowButton                 bool                     `json:"gift_card_show_button"`
	TelegramShowButton                 bool                     `json:"telegram_show_button"`
	TelegramURL                        string                   `json:"telegram_url"`
	UtilitySpeedShowButton             *bool                    `json:"utility_speed_show_button"`
	UtilityCfSpeedShowButton           *bool                    `json:"utility_cf_speed_show_button"`
	UtilityCfSpeedTargetDomains        []string                 `json:"utility_cf_speed_target_domains"`
	UtilityCfSpeedAutoReplaceEnabled   *bool                    `json:"utility_cf_speed_auto_replace_enabled"`
	UtilityCfSpeedAutoReplaceInterval  *int                     `json:"utility_cf_speed_auto_replace_interval_minutes"`
	UtilityIPLookupShowButton          *bool                    `json:"utility_ip_lookup_show_button"`
	UtilityMediaUnlockShowButton       *bool                    `json:"utility_media_unlock_show_button"`
	UtilityGoogleServicesShowButton    *bool                    `json:"utility_google_services_show_button"`
	UtilityPopularAppsShowSection      *bool                    `json:"utility_popular_apps_show_section"`
	UtilityPopularApps                 []ProfilePopularAppState `json:"utility_popular_apps"`
	UtilityChainProxyShowButton        *bool                    `json:"utility_chain_proxy_show_button"`
	ShowCustomRuleEntry                bool                     `json:"show_custom_rule_entry"`
	AuthPagesSupportShowButton         bool                     `json:"auth_pages_support_show_button"`
	Sources                            []ProfileSourceFormState `json:"sources"`
	SubscriptionCustomDomains          *[]string                `json:"custom_domains"`
	SubscriptionCustomSubscribeDomains *[]string                `json:"custom_subscribe_domains"`
}

type ProfileSourceFormState struct {
	MergeKey      string `json:"merge_key,omitempty"`
	Name          string `json:"name"`
	URL           string `json:"url"`
	EncryptionKey string `json:"encryption_key"`
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
	proxyGroups := ensureMapValueNode(ui, "proxy_groups")
	uiOnlineSupport := ensureMapValueNode(ui, "online_support")
	authPages := ensureMapValueNode(uiOnlineSupport, "auth_pages")
	remoteConfig := ensureMapValueNode(profileRoot, "remote_config")
	security := ensureMapValueNode(profileRoot, "security")
	securityUserAgents := ensureMapValueNode(security, "user_agents")

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
	if rootKey == "nexgen" {
		mergeCustomSubscribeDomains(subscription, "custom_domains", "custom_domain", form.SubscriptionCustomDomains, strings.TrimSpace(form.SubscriptionCustomDomain))
		removeMapKeys(subscription, "customDomain", "custom_subscribe_domain", "customSubscribeDomain", "customDomains", "custom_subscribe_domains")
	} else {
		mergeCustomSubscribeDomains(subscription, "custom_subscribe_domains", "custom_subscribe_domain", form.SubscriptionCustomSubscribeDomains, strings.TrimSpace(form.SubscriptionCustomSubscribeDomain))
		removeMapKeys(subscription, "customSubscribeDomain", "custom_domain", "customDomain", "customSubscribeDomains")
	}
	setMapStringValue(subscription, "decrypt_key", form.DecryptKey)
	if form.APIEncryptedUserAgent != nil {
		setMapStringValue(securityUserAgents, "api_encrypted", strings.TrimSpace(*form.APIEncryptedUserAgent))
		removeMapKeys(securityUserAgents, "apiEncrypted")
	}
	if rootKey == "nexgen" {
		setMapBoolValue(settings, "dns_override_default", form.DNSOverrideDefault)
		setMapBoolValue(settings, "auto_test_after_login", form.AutoTestAfterLogin)
		setMapBoolValue(settings, "auto_connect_on_startup", form.AutoConnectOnStartup)
		setMapBoolValue(settings, "log_file_enabled", form.LogFileEnabled)
		removeMapKeys(settings, "dnsOverrideDefault", "autoTestAfterLogin", "autoConnectOnStartup", "logFileEnabled")
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
	normalizedRegistrationInviteLinkBaseURL, err := normalizeRegistrationInviteLinkBaseURL(form.RegistrationInviteLinkBaseURL)
	if err != nil {
		return "", err
	}
	if form.RegistrationInviteLinkEnabled && normalizedRegistrationInviteLinkBaseURL == "" {
		return "", fmt.Errorf("开启自定义邀请链接时必须填写邀请链接地址")
	}
	setMapBoolValue(registrationInvite, "link_enabled", form.RegistrationInviteLinkEnabled)
	if normalizedRegistrationInviteLinkBaseURL == "" {
		removeMapKeys(registrationInvite, "link_base_url")
	} else {
		setMapStringValue(registrationInvite, "link_base_url", normalizedRegistrationInviteLinkBaseURL)
	}
	removeMapKeys(registrationInvite, "inviteCode", "invite_link", "inviteLink", "linkEnabled", "linkBaseUrl", "invite_link_enabled", "inviteLinkEnabled", "invite_link_base_url", "inviteLinkBaseUrl")
	removeMapKeys(profileRoot, "registrationInvite")
	setMapBoolValue(subscriptionCache, "enabled", form.SubscriptionCacheEnabled)
	setMapIntValue(subscriptionCache, "ttl_hours", form.SubscriptionCacheTTL)
	uiVariant := strings.TrimSpace(form.UiVariant)
	if uiVariant == "" {
		if existingVariant := getMapValueNode(ui, "variant"); existingVariant != nil {
			uiVariant = existingVariant.Value
		}
	}
	if uiVariant != "" || rootKey == "nexgen" {
		setMapStringValue(ui, "variant", normalizeNexGenUiVariant(uiVariant))
	}
	if rootKey == "nexgen" {
		uiColorScheme := strings.TrimSpace(form.UiColorScheme)
		if uiColorScheme == "" {
			if existingColorScheme := getMapValueNode(ui, "color_scheme"); existingColorScheme != nil {
				uiColorScheme = existingColorScheme.Value
			}
		}
		setMapStringValue(ui, "color_scheme", normalizeNexGenUiColorScheme(uiColorScheme))
		setMapBoolValue(ui, "hide_color_scheme_button", form.HideColorSchemeButton)
		setMapBoolValue(ui, "hide_online_support_button", form.HideOnlineSupportButton)
	} else {
		removeMapKeys(ui, "uiVariant", "colorScheme", "hideColorSchemeButton", "hideOnlineSupportButton", "show_color_scheme_button", "showColorSchemeButton", "show_online_support_button", "showOnlineSupportButton")
	}
	setMapBoolValue(ui, "hide_traffic_details", form.HideTrafficDetails)
	setMapBoolValue(ui, "hide_node_status", form.HideNodeStatus)
	if rootKey == "nexgen" {
		setMapBoolValue(ui, "hide_invite_promotion", form.HideInvitePromotion)
	} else {
		removeMapKeys(ui, "hide_invite_promotion", "hideInvitePromotion")
	}
	setMapBoolValue(ui, "hide_dedicated_nodes", form.HideDedicatedNodes)
	removeMapKeys(ui, "hideDedicatedNodes", "show_dedicated_nodes", "showDedicatedNodes")
	setMapBoolValue(ui, "hide_current_node_label", form.HideCurrentNodeLabel)
	setMapBoolValue(ui, "hide_page_header_text", form.HidePageHeaderText)
	if form.HidePurchaseCoupon != nil {
		setMapBoolValue(ui, "hide_purchase_coupon", *form.HidePurchaseCoupon)
		removeMapKeys(ui, "hidePurchaseCoupon")
	}
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
	// 未提交的工具字段保留原值，兼容浏览器缓存中的旧编辑页。
	if form.UtilitySpeedShowButton != nil {
		tool := ensureMapValueNode(ensureMapValueNode(ensureMapValueNode(ui, "utilities"), "tools"), "speed")
		setMapBoolValue(tool, "show_button", *form.UtilitySpeedShowButton)
	}
	if form.UtilityCfSpeedShowButton != nil {
		tool := ensureMapValueNode(ensureMapValueNode(ensureMapValueNode(ui, "utilities"), "tools"), "cf_speed")
		setMapBoolValue(tool, "show_button", *form.UtilityCfSpeedShowButton)
	}
	if form.UtilityIPLookupShowButton != nil {
		tool := ensureMapValueNode(ensureMapValueNode(ensureMapValueNode(ui, "utilities"), "tools"), "ip_lookup")
		setMapBoolValue(tool, "show_button", *form.UtilityIPLookupShowButton)
	}
	if form.UtilityMediaUnlockShowButton != nil {
		tool := ensureMapValueNode(ensureMapValueNode(ensureMapValueNode(ui, "utilities"), "tools"), "media_unlock")
		setMapBoolValue(tool, "show_button", *form.UtilityMediaUnlockShowButton)
	}
	if form.UtilityGoogleServicesShowButton != nil {
		tool := ensureMapValueNode(ensureMapValueNode(ensureMapValueNode(ui, "utilities"), "tools"), "google_services")
		setMapBoolValue(tool, "show_button", *form.UtilityGoogleServicesShowButton)
	}
	if form.UtilityChainProxyShowButton != nil {
		tool := ensureMapValueNode(ensureMapValueNode(ensureMapValueNode(ui, "utilities"), "tools"), "chain_proxy")
		setMapBoolValue(tool, "show_button", *form.UtilityChainProxyShowButton)
	}
	if form.UtilityCfSpeedTargetDomains != nil || form.UtilityCfSpeedAutoReplaceEnabled != nil || form.UtilityCfSpeedAutoReplaceInterval != nil {
		cfSpeed := ensureMapValueNode(ensureMapValueNode(ensureMapValueNode(ui, "utilities"), "tools"), "cf_speed")
		if form.UtilityCfSpeedTargetDomains != nil {
			setMapNodeValue(cfSpeed, "target_domains", newStringSequenceYamlNode(normalizeStringList(form.UtilityCfSpeedTargetDomains)))
		}
		if form.UtilityCfSpeedAutoReplaceEnabled != nil || form.UtilityCfSpeedAutoReplaceInterval != nil {
			autoReplace := ensureMapValueNode(cfSpeed, "auto_replace")
			if form.UtilityCfSpeedAutoReplaceEnabled != nil {
				setMapBoolValue(autoReplace, "enabled", *form.UtilityCfSpeedAutoReplaceEnabled)
			}
			if form.UtilityCfSpeedAutoReplaceInterval != nil {
				interval := normalizeCloudDispatchIntervalValue(*form.UtilityCfSpeedAutoReplaceInterval)
				if rootKey == "xboard" {
					// Flutter 客户端支持最长七天的自动优选间隔。
					interval = max(1, min(*form.UtilityCfSpeedAutoReplaceInterval, 7*24*60))
				}
				setMapIntValue(autoReplace, "interval_minutes", interval)
			}
		}
	}
	if form.UtilityPopularAppsShowSection != nil || form.UtilityPopularApps != nil {
		popularApps := ensureMapValueNode(ensureMapValueNode(ui, "utilities"), "popular_apps")
		if form.UtilityPopularAppsShowSection != nil {
			setMapBoolValue(popularApps, "show_section", *form.UtilityPopularAppsShowSection)
		}
		if form.UtilityPopularApps != nil {
			setMapNodeValue(popularApps, "items", mergeProfilePopularApps(getSequenceValueNode(popularApps, "items"), form.UtilityPopularApps))
		}
	}
	setMapBoolValue(proxyGroups, "show_custom_rule_entry", form.ShowCustomRuleEntry)
	setMapBoolValue(authPages, "show_button", form.AuthPagesSupportShowButton)
	removeMapKeys(profileRoot, "proxy_groups", "proxyGroups")
	// 客服条目已改为 OSS 顶层 onlineSupport 下发，档案里的旧块一并清掉。
	removeMapKeys(profileRoot, "online_support", "onlineSupport")

	setMapStringValue(remoteConfig, "api_path_prefix", normalizePanelAPIPathPrefix(form.PanelAPIPathPrefix))
	removeMapKeys(remoteConfig, "apiPathPrefix")
	setMapNodeValue(remoteConfig, "sources", mergeProfileSources(getSequenceValueNode(remoteConfig, "sources"), form.Sources))

	// 内置代理已整体迁移到 OSS 远程配置（顶层 clientProxy）下发，档案里不再保留。
	// 这里顺手清掉历史档案里的配置块，避免旧节点变成看不见的兜底。
	removeMapKeys(profileRoot, "client_proxy", "clientProxy")

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

// mergeCustomSubscribeDomains 写入自定义订阅域名。
//
// listKey 是列表键（新客户端 custom_domains、老客户端 custom_subscribe_domains），
// singleKey 是旧版单值键。list 为 nil 表示页面没提交列表（旧版页面缓存），
// 此时只写单值；提交了列表就以列表为准，并把第一个域名同步到单值键，
// 让尚未升级、只认单值键的旧版客户端仍能生效。
func mergeCustomSubscribeDomains(subscription *yaml.Node, listKey, singleKey string, list *[]string, single string) {
	if list == nil {
		setMapStringValue(subscription, singleKey, single)
		return
	}

	domains := normalizeCustomSubscribeDomains(*list)
	if len(domains) == 0 {
		removeMapKeys(subscription, listKey)
		setMapStringValue(subscription, singleKey, "")
		return
	}
	setMapNodeValue(subscription, listKey, newStringListYamlNode(domains))
	setMapStringValue(subscription, singleKey, domains[0])
}

// normalizeCustomSubscribeDomains 归一化自定义订阅域名列表。
//
// 与两个客户端的解析保持一致：按空白与半角/全角逗号拆分，去空白、转小写、去重并保持顺序。
// 条目本身允许是纯域名、带端口的域名或带 http(s):// 前缀的地址，不做进一步改写。
func normalizeCustomSubscribeDomains(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
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

func newStringListYamlNode(values []string) *yaml.Node {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, value := range values {
		seq.Content = append(seq.Content, newStringYamlNode(value))
	}
	return seq
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
