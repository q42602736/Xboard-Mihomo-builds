package main

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type ProfileFormState struct {
	Provider                       string                    `json:"provider"`
	AppTitle                       string                    `json:"app_title"`
	LogoType                       string                    `json:"logo_type"`
	LogoImageURL                   string                    `json:"logo_image_url"`
	AppIconURL                     string                    `json:"app_icon_url"`
	AuthBackgroundEnabled          bool                      `json:"auth_background_enabled"`
	AuthBackgroundImageURL         string                    `json:"auth_background_image_url"`
	PreferEncrypt                  bool                      `json:"prefer_encrypt"`
	SubscriptionUserAgent          string                    `json:"user_agent"`
	SubscriptionExclusiveUserAgent string                    `json:"exclusive_user_agent"`
	SubscriptionCustomQuerySuffix  string                    `json:"custom_query_suffix"`
	UseExclusiveMode               bool                      `json:"use_exclusive_mode"`
	DecryptKey                     string                    `json:"decrypt_key"`
	AutoOfflineEnabled             bool                      `json:"auto_offline_enabled"`
	SubscriptionCacheEnabled       bool                      `json:"subscription_cache_enabled"`
	SubscriptionCacheTTL           int                       `json:"subscription_cache_ttl"`
	HideTrafficDetails             bool                      `json:"hide_traffic_details"`
	HideNodeStatus                 bool                      `json:"hide_node_status"`
	HomePanelDefaultLayout         string                    `json:"home_panel_default_layout"`
	LatencyReductionEnabled        bool                      `json:"latency_reduction_enabled"`
	LatencyReductionValue          int                       `json:"latency_reduction_value"`
	NoticeAutoOpenOnStartup        bool                      `json:"notice_auto_open_on_startup"`
	CheckinShowButton              bool                      `json:"checkin_show_button"`
	GiftCardShowButton             bool                      `json:"gift_card_show_button"`
	ShowCustomRuleEntry            bool                      `json:"show_custom_rule_entry"`
	AuthPagesSupportShowButton     bool                      `json:"auth_pages_support_show_button"`
	Sources                        []ProfileSourceFormState  `json:"sources"`
	OnlineSupportItems             []ProfileSupportFormState `json:"online_support_items"`
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

func mergeProfileYamlWithForm(baseYaml string, form ProfileFormState) (string, error) {
	baseYaml = strings.ReplaceAll(baseYaml, "config_cache:", "subscription_cache:")

	doc, err := parseProfileYamlDocument(baseYaml)
	if err != nil {
		return "", err
	}

	root := ensureDocumentMappingNode(doc)
	xboard := ensureMapValueNode(root, "xboard")
	app := ensureMapValueNode(xboard, "app")
	logo := ensureMapValueNode(app, "logo")
	appIcon := ensureMapValueNode(app, "app_icon")
	authBackground := ensureMapValueNode(app, "auth_background")
	subscription := ensureMapValueNode(xboard, "subscription")
	autoOffline := ensureMapValueNode(xboard, "auto_offline")
	subscriptionCache := ensureMapValueNode(xboard, "subscription_cache")
	ui := ensureMapValueNode(xboard, "ui")
	latencyReduction := ensureMapValueNode(ui, "latency_reduction")
	notice := ensureMapValueNode(ui, "notice")
	checkin := ensureMapValueNode(ui, "checkin")
	giftCard := ensureMapValueNode(ui, "gift_card")
	proxyGroups := ensureMapValueNode(ui, "proxy_groups")
	uiOnlineSupport := ensureMapValueNode(ui, "online_support")
	authPages := ensureMapValueNode(uiOnlineSupport, "auth_pages")
	remoteConfig := ensureMapValueNode(xboard, "remote_config")
	onlineSupport := ensureMapValueNode(xboard, "online_support")

	setMapStringValue(xboard, "provider", strings.TrimSpace(form.Provider))
	setMapStringValue(xboard, "title", strings.TrimSpace(form.AppTitle))
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
	setMapBoolValue(autoOffline, "enabled", form.AutoOfflineEnabled)
	setMapBoolValue(subscriptionCache, "enabled", form.SubscriptionCacheEnabled)
	setMapIntValue(subscriptionCache, "ttl_hours", form.SubscriptionCacheTTL)
	setMapBoolValue(ui, "hide_traffic_details", form.HideTrafficDetails)
	setMapBoolValue(ui, "hide_node_status", form.HideNodeStatus)
	homePanelDefaultLayout := strings.TrimSpace(form.HomePanelDefaultLayout)
	if homePanelDefaultLayout == "" {
		homePanelDefaultLayout = "default"
	}
	setMapStringValue(ui, "home_panel_default_layout", homePanelDefaultLayout)
	setMapBoolValue(latencyReduction, "enabled", form.LatencyReductionEnabled)
	setMapIntValue(latencyReduction, "value", normalizeLatencyReductionValue(form.LatencyReductionValue))
	setMapBoolValue(notice, "auto_open_on_startup", form.NoticeAutoOpenOnStartup)
	setMapBoolValue(checkin, "show_button", form.CheckinShowButton)
	setMapBoolValue(giftCard, "show_button", form.GiftCardShowButton)
	setMapBoolValue(proxyGroups, "show_custom_rule_entry", form.ShowCustomRuleEntry)
	setMapBoolValue(authPages, "show_button", form.AuthPagesSupportShowButton)
	removeMapKeys(xboard, "proxy_groups", "proxyGroups")
	removeMapKeys(onlineSupport, "auth_pages", "authPages")

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
