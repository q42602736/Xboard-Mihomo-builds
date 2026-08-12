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
	Exists      bool   `json:"exists"`
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
	profileRoot := getMapValueNode(root, "nexgen")
	if profileRoot == nil {
		profileRoot = getMapValueNode(root, "xboard")
	}
	if profileRoot == nil {
		return displayName
	}
	if title := strings.TrimSpace(readMapStringValue(profileRoot, "title")); title != "" {
		return title
	}
	app := getMapValueNode(profileRoot, "app")
	if app != nil {
		if title := strings.TrimSpace(readMapStringValue(app, "title")); title != "" {
			return title
		}
	}
	return displayName
}

func stripGeneratedProfileKeySuffix(value string) string {
	value = strings.TrimSpace(value)
	index := strings.LastIndex(value, "--")
	if index < 0 || len(value)-index-2 < 6 {
		return value
	}
	suffix := value[index+2:]
	for _, ch := range suffix {
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')) {
			return value
		}
	}
	return strings.TrimSpace(value[:index])
}

func nexGenProfileDisplayNameFromYaml(_ string, fallback string) string {
	return stripGeneratedProfileKeySuffix(fallback)
}

func randomProfileKeySuffix() string {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	return hex.EncodeToString(buf)
}

func (h *Handlers) createUniqueProfileKey(displayName string) (string, error) {
	return h.createUniqueProfileKeyForClient(buildClientLegacy, displayName)
}

func (h *Handlers) createUniqueProfileKeyForClient(client, displayName string) (string, error) {
	base := strings.TrimSpace(displayName)
	if _, err := profileFilePath(base); err != nil {
		return "", err
	}

	_, _, _, exists, err := h.getStoredProfileForClient(client, base)
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
		_, _, _, exists, err := h.getStoredProfileForClient(client, key)
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
  settings:
    dns_override_default: false
    auto_connect_on_startup: false
    log_file_enabled: true
  auto_offline:
    enabled: false
    force_on_startup: false
    auto_enter_interval_hours: 0
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
  registration_invite:
    enabled: false
    mode: default_when_empty
    invite_code: ""
  remote_config:
    api_path_prefix: /api/v1
    sources:
      - name: redirect
        url: ""
  online_support:
    items: []
  ui:
    hide_traffic_details: false
    hide_node_status: false
    hide_purchase_coupon: false
    hide_plan_speed: false
    hide_current_node_label: false
    hide_page_header_text: false
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
      auto_open_interval_hours: 24
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
      show_button: false
    utilities:
      tools:
        speed:
          show_button: true
        ip_lookup:
          show_button: true
        media_unlock:
          show_button: true
        google_services:
          show_button: false
      popular_apps:
        show_section: false
        items: []`, displayName)
}

func (h *Handlers) createManualProfile(displayName string) (StoredProfile, error) {
	return h.createManualProfileForClient(buildClientLegacy, displayName)
}

func (h *Handlers) profileGitHubClient(client string) *GitHubClient {
	if normalizeBuildClient(client) == buildClientNexGenReact && h.nexGenProfileGH != nil {
		return h.nexGenProfileGH
	}
	return h.profileGH
}

func (h *Handlers) profileBranchForClient(client string) string {
	return h.profileGitHubClient(client).Branch
}

func (h *Handlers) ensureProfileBranchForClient(client string) error {
	return h.profileGitHubClient(client).EnsureBranchFromDefault()
}

func (h *Handlers) createManualProfileForClient(client, displayName string) (StoredProfile, error) {
	client = normalizeBuildClient(client)
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		return StoredProfile{}, fmt.Errorf("档案名称不能为空")
	}
	key, err := h.createUniqueProfileKeyForClient(client, displayName)
	if err != nil {
		return StoredProfile{}, err
	}
	filePath, err := profileFilePath(key)
	if err != nil {
		return StoredProfile{}, err
	}
	var yamlContent string
	if client == buildClientNexGenReact {
		yamlContent = bindNexGenProfileProvider(normalizeNexGenProfileConfig(defaultProfileYaml(displayName)), displayName)
	} else {
		yamlContent = normalizeSubscriptionConfig(defaultProfileYaml(displayName))
	}
	if err := validateYamlContent(yamlContent); err != nil {
		return StoredProfile{}, err
	}
	if err := h.ensureProfileBranchForClient(client); err != nil {
		return StoredProfile{}, err
	}
	if _, err := h.profileGitHubClient(client).SaveFile(filePath, yamlContent, "", "创建配置档案: "+key); err != nil {
		return StoredProfile{}, err
	}
	invalidateProfileCacheForClient(client)
	return StoredProfile{
		Name:        key,
		Key:         key,
		DisplayName: displayName,
		Exists:      true,
	}, nil
}

func (h *Handlers) listStoredProfiles() (map[string]StoredProfile, error) {
	return h.listStoredProfilesForClient(buildClientLegacy)
}

func (h *Handlers) listStoredProfilesForClient(client string) (map[string]StoredProfile, error) {
	client = normalizeBuildClient(client)
	if cached, ok := storedProfilesCache.get(client); ok {
		return cloneStoredProfileMap(cached), nil
	}

	if err := h.ensureProfileBranchForClient(client); err != nil {
		return nil, err
	}
	profileGH := h.profileGitHubClient(client)
	items, err := profileGH.ListDirectory(profilesDir)
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
		content, _, err := profileGH.GetFile(item.Path)
		displayName := name
		if err == nil {
			if normalizeBuildClient(client) == buildClientNexGenReact {
				displayName = nexGenProfileDisplayNameFromYaml(content, name)
			} else {
				displayName = profileDisplayNameFromYaml(content, name)
			}
		}
		lastUpdated, err := profileGH.GetLatestCommitTime(item.Path)
		if err != nil {
			lastUpdated = ""
		}
		profiles[name] = StoredProfile{
			Name:        name,
			Key:         name,
			DisplayName: displayName,
			LastUpdated: lastUpdated,
			Exists:      true,
		}
	}
	storedProfilesCache.set(client, cloneStoredProfileMap(profiles), profileListCacheTTL)
	return profiles, nil
}

func (h *Handlers) listStoredProfileKeysForClient(client string) (map[string]StoredProfile, error) {
	client = normalizeBuildClient(client)
	if cached, ok := storedProfileKeysCache.get(client); ok {
		return cloneStoredProfileMap(cached), nil
	}

	if err := h.ensureProfileBranchForClient(client); err != nil {
		return nil, err
	}
	profileGH := h.profileGitHubClient(client)
	items, err := profileGH.ListDirectory(profilesDir)
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
		profiles[name] = StoredProfile{
			Name:        name,
			Key:         name,
			DisplayName: name,
			Exists:      true,
		}
	}
	storedProfileKeysCache.set(client, cloneStoredProfileMap(profiles), profileListCacheTTL)
	return profiles, nil
}

func (h *Handlers) getStoredProfile(name string) (yamlContent string, sha string, lastUpdated string, exists bool, err error) {
	return h.getStoredProfileForClient(buildClientLegacy, name)
}

func (h *Handlers) getStoredProfileForClient(client, name string) (yamlContent string, sha string, lastUpdated string, exists bool, err error) {
	client = normalizeBuildClient(client)
	filePath, err := profileFilePath(name)
	if err != nil {
		return "", "", "", false, err
	}

	if err := h.ensureProfileBranchForClient(client); err != nil {
		return "", "", "", false, err
	}
	profileGH := h.profileGitHubClient(client)
	content, sha, err := profileGH.GetFile(filePath)
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			return "", "", "", false, nil
		}
		return "", "", "", false, err
	}

	lastUpdated, err = profileGH.GetLatestCommitTime(filePath)
	if err != nil {
		lastUpdated = ""
	}
	return content, sha, lastUpdated, true, nil
}
