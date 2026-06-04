package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

const profilesDir = "profiles"

type StoredProfile struct {
	Name        string `json:"name"`
	Key         string `json:"key"`
	DisplayName string `json:"display_name"`
	LastUpdated string `json:"last_updated,omitempty"`
}

func profileFilePath(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("档案名称不能为空")
	}
	if name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
		return "", fmt.Errorf("档案名称非法")
	}
	return profilesDir + "/" + name + ".yaml", nil
}

func cloneStoredProfileMap(src map[string]StoredProfile) map[string]StoredProfile {
	if src == nil {
		return nil
	}
	dst := make(map[string]StoredProfile, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func profileDisplayNameFromYaml(yamlContent, fallback string) string {
	displayName := strings.TrimSpace(fallback)
	doc, err := parseProfileYamlDocument(yamlContent)
	if err != nil {
		return displayName
	}
	root := ensureDocumentMappingNode(doc)
	xboard := getMapValueNode(root, "xboard")
	if xboard == nil {
		return displayName
	}
	if title := strings.TrimSpace(readMapStringValue(xboard, "title")); title != "" {
		return title
	}
	app := getMapValueNode(xboard, "app")
	if app != nil {
		if title := strings.TrimSpace(readMapStringValue(app, "title")); title != "" {
			return title
		}
	}
	return displayName
}

func randomProfileKeySuffix() string {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	return hex.EncodeToString(buf)
}

func (h *Handlers) createUniqueProfileKey(displayName string) (string, error) {
	base := strings.TrimSpace(displayName)
	if _, err := profileFilePath(base); err != nil {
		return "", err
	}

	_, _, _, exists, err := h.getStoredProfile(base)
	if err != nil {
		return "", err
	}
	if !exists {
		return base, nil
	}

	for i := 0; i < 8; i++ {
		suffix := randomProfileKeySuffix()
		if suffix == "" {
			continue
		}
		key := base + "--" + suffix
		_, _, _, exists, err := h.getStoredProfile(key)
		if err != nil {
			return "", err
		}
		if !exists {
			return key, nil
		}
	}
	return "", fmt.Errorf("生成唯一档案键失败")
}

func defaultProfileYaml(displayName string) string {
	return bindProfileTitle(`xboard:
  provider: ""
  app:
    title: ""
    logo:
      type: text
      image_url: ""
    auth_background:
      enabled: false
      image_url: ""
    app_icon:
      image_url: ""
  subscription:
    prefer_encrypt: false
    user_agent: Clash Meta
    exclusive_user_agent: XBoardMihomo/1.0
    use_exclusive_mode: false
    sspanel_node_page_parse_enabled: false
    decrypt_key: ""
  auto_offline:
    enabled: false
  subscription_cache:
    enabled: false
    ttl_hours: 24
  cloud_dispatch:
    enabled: false
    query_url: ""
    query_secret: ""
    fallback_retry_minutes: 5
    auto:
      enabled: false
      interval_minutes: 5
  remote_config:
    sources:
      - name: redirect
        url: ""
  online_support:
    items: []
  ui:
    hide_traffic_details: false
    hide_node_status: false
    show_ip_info: true
    custom_colors:
      notice_dialog_icon_background_color: ""
      subscription_website_icon_color: ""
      subscription_refresh_icon_color: ""
      subscription_notice_icon_color: ""
      login_button_color: ""
      plans_subscribe_button_color: ""
      plans_filter_tab_color: ""
      invite_code_text_color: ""
      invite_code_background_color: ""
      commission_balance_card_background_color: ""
      invite_stats_total_invites_icon_color: ""
      invite_stats_commission_rate_icon_color: ""
      invite_stats_total_commission_icon_color: ""
    home_panel_default_layout: default
    latency_reduction:
      enabled: false
      value: 0
    notice:
      auto_open_on_startup: false
    subscription_status_popup:
      enabled: false
      official_url: ""
    proxy_groups:
      show_custom_rule_entry: true
      main_policy_nodes_only: false
    online_support:
      auth_pages:
        show_button: false
    checkin:
      show_button: false
    gift_card:
      show_button: false`, displayName)
}

func (h *Handlers) createManualProfile(displayName string) (StoredProfile, error) {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		return StoredProfile{}, fmt.Errorf("档案名称不能为空")
	}
	key, err := h.createUniqueProfileKey(displayName)
	if err != nil {
		return StoredProfile{}, err
	}
	filePath, err := profileFilePath(key)
	if err != nil {
		return StoredProfile{}, err
	}
	yamlContent := normalizeSubscriptionConfig(defaultProfileYaml(displayName))
	if err := validateYamlContent(yamlContent); err != nil {
		return StoredProfile{}, err
	}
	if _, err := h.profileGH.SaveFile(filePath, yamlContent, "", "创建配置档案: "+key); err != nil {
		return StoredProfile{}, err
	}
	invalidateProfileCache()
	return StoredProfile{
		Name:        key,
		Key:         key,
		DisplayName: displayName,
	}, nil
}

func (h *Handlers) listStoredProfiles() (map[string]StoredProfile, error) {
	if cached, ok := storedProfilesCache.get(); ok {
		return cloneStoredProfileMap(cached), nil
	}

	items, err := h.profileGH.ListDirectory(profilesDir)
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			return map[string]StoredProfile{}, nil
		}
		return nil, err
	}

	profiles := make(map[string]StoredProfile)
	for _, item := range items {
		if item.Type != "file" || !strings.HasSuffix(item.Name, ".yaml") {
			continue
		}

		name := strings.TrimSuffix(item.Name, ".yaml")
		content, _, err := h.profileGH.GetFile(item.Path)
		displayName := name
		if err == nil {
			displayName = profileDisplayNameFromYaml(content, name)
		}
		lastUpdated, err := h.profileGH.GetLatestCommitTime(item.Path)
		if err != nil {
			lastUpdated = ""
		}
		profiles[name] = StoredProfile{
			Name:        name,
			Key:         name,
			DisplayName: displayName,
			LastUpdated: lastUpdated,
		}
	}
	storedProfilesCache.set(cloneStoredProfileMap(profiles), profileListCacheTTL)
	return profiles, nil
}

func (h *Handlers) getStoredProfile(name string) (yamlContent string, sha string, lastUpdated string, exists bool, err error) {
	filePath, err := profileFilePath(name)
	if err != nil {
		return "", "", "", false, err
	}

	content, sha, err := h.profileGH.GetFile(filePath)
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			return "", "", "", false, nil
		}
		return "", "", "", false, err
	}

	lastUpdated, err = h.profileGH.GetLatestCommitTime(filePath)
	if err != nil {
		lastUpdated = ""
	}
	return content, sha, lastUpdated, true, nil
}
