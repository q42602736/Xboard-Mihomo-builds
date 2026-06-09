package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"gopkg.in/yaml.v3"
)

const maxBuildRecordHistory = 5
const buildAssetDownloadLinkTTL = 10 * time.Minute

var buildRequestIDPattern = regexp.MustCompile(`BR-[0-9a-fA-F]{24}`)

const (
	profileListCacheTTL              = 5 * time.Minute
	buildQueueSnapshotTTL            = 15 * time.Second
	buildStatusActiveCacheTTL        = 8 * time.Second
	buildStatusDoneCacheTTL          = 2 * time.Minute
	buildStatusGitHubSyncMinInterval = 3 * time.Minute
)

var (
	storedProfilesCache     keyedTTLCache[map[string]StoredProfile]
	storedProfileKeysCache  keyedTTLCache[map[string]StoredProfile]
	buildQueueSnapshotCache keyedTTLCache[BuildQueueSnapshot]
	buildStatusCache        keyedTTLCache[map[string]interface{}]
	buildCacheMu            sync.Mutex
)

func cloneInterfaceMap(src map[string]interface{}) map[string]interface{} {
	if src == nil {
		return nil
	}
	dst := make(map[string]interface{}, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func invalidateProfileCache() {
	storedProfilesCache.clear()
	storedProfileKeysCache.clear()
}

func invalidateProfileCacheForClient(client string) {
	client = normalizeBuildClient(client)
	storedProfilesCache.delete(client)
	storedProfileKeysCache.delete(client)
}

func invalidateBuildCachesForRecord(record *BuildRecord) {
	buildQueueSnapshotCache.clear()
	if record == nil {
		buildStatusCache.clear()
		return
	}
	if record.RequestID != "" {
		buildStatusCache.delete("request:" + strings.TrimSpace(record.RequestID))
	}
	if record.ID > 0 {
		buildStatusCache.delete(fmt.Sprintf("record:%d", record.ID))
	}
	buildStatusCache.delete(buildStatusFallbackCacheKey(record.CodeID, record.Client, record.Profile, buildRecordRequestID(record), record.Tag, record.Branch, record.Core, record.Platforms))
}

func buildStatusFallbackCacheKey(codeID int, client, profile, requestID, tag, branch, core, platforms string) string {
	if strings.TrimSpace(requestID) != "" {
		return "request:" + strings.TrimSpace(requestID)
	}
	client = normalizeBuildClient(client)
	return fmt.Sprintf("fallback:%d|client:%s|%s|%s|%s|%s|%s", codeID, client, profile, tag, branch, normalizeBuildCore(core), platforms)
}

func buildStatusCacheKey(record *BuildRecord, codeID int, client, profile, requestID, tag, branch, core, platforms string) string {
	if record != nil && record.ID > 0 {
		return fmt.Sprintf("record:%d", record.ID)
	}
	if strings.TrimSpace(requestID) != "" {
		return "request:" + strings.TrimSpace(requestID)
	}
	return buildStatusFallbackCacheKey(codeID, client, profile, requestID, tag, branch, core, platforms)
}

func buildStatusCacheTTL(result map[string]interface{}) time.Duration {
	if status, _ := result["status"].(string); status == "completed" {
		return buildStatusDoneCacheTTL
	}
	if found, _ := result["found"].(bool); !found {
		return buildStatusActiveCacheTTL
	}
	return buildStatusActiveCacheTTL
}

func writeBuildStatusResponse(w http.ResponseWriter, cacheKey string, result map[string]interface{}) {
	if cacheKey != "" {
		buildStatusCache.set(cacheKey, cloneInterfaceMap(result), buildStatusCacheTTL(result))
	}
	jsonResponse(w, result)
}

type Handlers struct {
	gh              *GitHubClient
	profileGH       *GitHubClient
	nexGenProfileGH *GitHubClient
}

func NewHandlers(gh *GitHubClient, profileGH *GitHubClient, nexGenProfileGH *GitHubClient) *Handlers {
	return &Handlers{
		gh:              gh,
		profileGH:       profileGH,
		nexGenProfileGH: nexGenProfileGH,
	}
}

func jsonResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func requestScheme(r *http.Request) string {
	forwardedProto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if forwardedProto != "" {
		return strings.TrimSpace(strings.Split(forwardedProto, ",")[0])
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func requestHost(r *http.Request) string {
	forwardedHost := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if forwardedHost != "" {
		return strings.TrimSpace(strings.Split(forwardedHost, ",")[0])
	}
	return r.Host
}

func buildAssetSignedDownloadURL(r *http.Request, recordID, assetID int64, token string) string {
	values := url.Values{}
	values.Set("token", token)
	return (&url.URL{
		Scheme:   requestScheme(r),
		Host:     requestHost(r),
		Path:     fmt.Sprintf("/download/build/records/%d/assets/%d", recordID, assetID),
		RawQuery: values.Encode(),
	}).String()
}

func writeDownloadLinkStatusPage(w http.ResponseWriter, statusCode int, title, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.WriteHeader(statusCode)
	_, _ = io.WriteString(w, fmt.Sprintf(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>%s</title>
  <style>
    :root { color-scheme: dark; }
    body {
      margin: 0;
      min-height: 100vh;
      display: flex;
      align-items: center;
      justify-content: center;
      background: linear-gradient(180deg, #0b1220 0%%, #111827 100%%);
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      color: #e5eefc;
      padding: 24px;
      box-sizing: border-box;
    }
    .card {
      width: min(520px, 100%%);
      padding: 28px 24px;
      border-radius: 16px;
      background: rgba(17, 24, 39, 0.92);
      border: 1px solid rgba(255,255,255,0.08);
      box-shadow: 0 24px 80px rgba(0,0,0,0.45);
    }
    h1 {
      margin: 0 0 12px;
      font-size: 22px;
      color: #fff;
    }
    p {
      margin: 0;
      line-height: 1.8;
      color: #c7d2e3;
      font-size: 14px;
      white-space: pre-wrap;
    }
    .hint {
      margin-top: 14px;
      color: #8fb4ff;
      font-size: 13px;
    }
  </style>
</head>
<body>
  <div class="card">
    <h1>%s</h1>
    <p>%s</p>
    <p class="hint">请返回“我的打包记录”重新获取新的下载链接。</p>
  </div>
</body>
</html>`,
		html.EscapeString(title),
		html.EscapeString(title),
		html.EscapeString(message),
	))
}

func bindProfileTitle(yamlContent, profileName string) string {
	return bindProfileTitleForRoot(yamlContent, profileName, "xboard")
}

func bindNexGenProfileTitle(yamlContent, profileName string) string {
	return bindProfileTitleForRoot(yamlContent, profileName, "nexgen")
}

func bindProfileTitleForRoot(yamlContent, profileName, rootKey string) string {
	if yamlContent == "" || profileName == "" {
		return yamlContent
	}

	doc, err := parseProfileYamlDocument(yamlContent)
	if err != nil {
		return yamlContent
	}

	root := ensureDocumentMappingNode(doc)
	profileRoot := ensureMapValueNode(root, rootKey)
	app := ensureMapValueNode(profileRoot, "app")
	setMapStringValue(profileRoot, "title", strings.TrimSpace(profileName))
	setMapStringValue(app, "title", strings.TrimSpace(profileName))

	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(doc); err != nil {
		return yamlContent
	}
	_ = encoder.Close()
	return strings.TrimRight(buf.String(), "\n")
}

func normalizeSubscriptionConfig(yamlContent string) string {
	if strings.TrimSpace(yamlContent) == "" {
		return yamlContent
	}

	doc, err := parseProfileYamlDocument(yamlContent)
	if err != nil {
		return yamlContent
	}

	root := ensureDocumentMappingNode(doc)
	xboard := ensureMapValueNode(root, "xboard")
	subscription := ensureMapValueNode(xboard, "subscription")

	var preferEncrypt, useExclusiveMode bool
	var sspanelNodePageParseEnabled bool
	var decryptKey, subscriptionUserAgent, subscriptionExclusiveUserAgent, subscriptionCustomQuerySuffix string
	var hasLegacyPreferEncrypt bool
	var hasLegacyUseExclusiveMode bool
	var hasLegacySSPanelNodePageParse bool
	var hasLegacyDecryptKey bool
	var hasLegacySubscriptionUserAgent bool
	var hasLegacySubscriptionExclusiveUserAgent bool
	var hasLegacySubscriptionCustomQuerySuffix bool

	preferEncrypt, hasLegacyPreferEncrypt = readLegacyBool(xboard, "prefer_encrypt", false)
	useExclusiveMode, hasLegacyUseExclusiveMode = readLegacyBool(xboard, "use_exclusive_mode", false)
	sspanelNodePageParseEnabled, hasLegacySSPanelNodePageParse = readLegacyBool(xboard, "sspanel_node_page_parse_enabled", false)
	decryptKey, hasLegacyDecryptKey = readLegacyString(xboard, "decrypt_key", false)
	subscriptionUserAgent, hasLegacySubscriptionUserAgent = readLegacyString(xboard, "user_agent", false)
	subscriptionExclusiveUserAgent, hasLegacySubscriptionExclusiveUserAgent = readLegacyString(xboard, "exclusive_user_agent", false)
	subscriptionCustomQuerySuffix, hasLegacySubscriptionCustomQuerySuffix = readLegacyString(xboard, "custom_query_suffix", false)

	hasLegacy := hasLegacyPreferEncrypt ||
		hasLegacyUseExclusiveMode ||
		hasLegacySSPanelNodePageParse ||
		hasLegacyDecryptKey ||
		hasLegacySubscriptionUserAgent ||
		hasLegacySubscriptionExclusiveUserAgent ||
		hasLegacySubscriptionCustomQuerySuffix

	if hasLegacy {
		removeMapKeys(xboard, "prefer_encrypt", "use_exclusive_mode", "sspanel_node_page_parse_enabled", "decrypt_key", "user_agent", "exclusive_user_agent", "custom_query_suffix")
		if hasLegacyPreferEncrypt {
			setMapBoolValue(subscription, "prefer_encrypt", preferEncrypt)
		}
		if hasLegacyUseExclusiveMode {
			setMapBoolValue(subscription, "use_exclusive_mode", useExclusiveMode)
		}
		if hasLegacySSPanelNodePageParse {
			setMapBoolValue(subscription, "sspanel_node_page_parse_enabled", sspanelNodePageParseEnabled)
		}
		if hasLegacyDecryptKey && strings.TrimSpace(decryptKey) != "" {
			setMapStringValue(subscription, "decrypt_key", decryptKey)
		}
		if hasLegacySubscriptionUserAgent && strings.TrimSpace(subscriptionUserAgent) != "" {
			setMapStringValue(subscription, "user_agent", strings.TrimSpace(subscriptionUserAgent))
		}
		if hasLegacySubscriptionExclusiveUserAgent && strings.TrimSpace(subscriptionExclusiveUserAgent) != "" {
			setMapStringValue(subscription, "exclusive_user_agent", strings.TrimSpace(subscriptionExclusiveUserAgent))
		}
		if hasLegacySubscriptionCustomQuerySuffix && strings.TrimSpace(subscriptionCustomQuerySuffix) != "" {
			setMapStringValue(subscription, "custom_query_suffix", strings.TrimSpace(subscriptionCustomQuerySuffix))
		}
	}

	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(doc); err != nil {
		return yamlContent
	}
	_ = encoder.Close()

	return strings.TrimRight(buf.String(), "\n")
}

func stripNexGenProfileUnsupportedConfig(yamlContent string) string {
	if strings.TrimSpace(yamlContent) == "" {
		return yamlContent
	}
	doc, err := parseProfileYamlDocument(yamlContent)
	if err != nil {
		return yamlContent
	}
	root := ensureDocumentMappingNode(doc)
	nexgen := getMapValueNode(root, "nexgen")
	if nexgen == nil {
		return yamlContent
	}
	removeMapKeys(nexgen, "online_support", "cloud_dispatch", "cloudDispatch")
	app := getMapValueNode(nexgen, "app")
	if app != nil {
		logo := getMapValueNode(app, "logo")
		if logo != nil {
			setMapStringValue(logo, "type", "text")
			removeMapKeys(logo, "image_url", "imageUrl")
		}
	}
	ui := getMapValueNode(nexgen, "ui")
	if ui != nil {
		removeMapKeys(ui, "online_support", "home_panel_default_layout", "custom_colors", "customColors", "subscription_status_popup", "subscriptionStatusPopup", "show_ip_info", "showIpInfo")
	}
	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(doc); err != nil {
		return yamlContent
	}
	_ = encoder.Close()
	return strings.TrimRight(buf.String(), "\n")
}

func normalizeNexGenProfileConfig(yamlContent string) string {
	if strings.TrimSpace(yamlContent) == "" {
		return yamlContent
	}

	doc, err := parseProfileYamlDocument(yamlContent)
	if err != nil {
		return yamlContent
	}

	root := ensureDocumentMappingNode(doc)
	nexgen := getMapValueNode(root, "nexgen")
	if nexgen == nil {
		xboard := getMapValueNode(root, "xboard")
		if xboard == nil {
			nexgen = ensureMapValueNode(root, "nexgen")
		} else {
			nexgen = cloneYamlNode(xboard)
			removeMapKeys(root, "xboard")
			setMapNodeValue(root, "nexgen", nexgen)
		}
	} else {
		removeMapKeys(root, "xboard")
	}

	subscription := ensureMapValueNode(nexgen, "subscription")

	var preferEncrypt, useExclusiveMode bool
	var sspanelNodePageParseEnabled bool
	var decryptKey, subscriptionUserAgent, subscriptionExclusiveUserAgent, subscriptionCustomQuerySuffix string
	var hasLegacyPreferEncrypt bool
	var hasLegacyUseExclusiveMode bool
	var hasLegacySSPanelNodePageParse bool
	var hasLegacyDecryptKey bool
	var hasLegacySubscriptionUserAgent bool
	var hasLegacySubscriptionExclusiveUserAgent bool
	var hasLegacySubscriptionCustomQuerySuffix bool

	preferEncrypt, hasLegacyPreferEncrypt = readLegacyBool(nexgen, "prefer_encrypt", false)
	useExclusiveMode, hasLegacyUseExclusiveMode = readLegacyBool(nexgen, "use_exclusive_mode", false)
	sspanelNodePageParseEnabled, hasLegacySSPanelNodePageParse = readLegacyBool(nexgen, "sspanel_node_page_parse_enabled", false)
	decryptKey, hasLegacyDecryptKey = readLegacyString(nexgen, "decrypt_key", false)
	subscriptionUserAgent, hasLegacySubscriptionUserAgent = readLegacyString(nexgen, "user_agent", false)
	subscriptionExclusiveUserAgent, hasLegacySubscriptionExclusiveUserAgent = readLegacyString(nexgen, "exclusive_user_agent", false)
	subscriptionCustomQuerySuffix, hasLegacySubscriptionCustomQuerySuffix = readLegacyString(nexgen, "custom_query_suffix", false)

	hasLegacy := hasLegacyPreferEncrypt ||
		hasLegacyUseExclusiveMode ||
		hasLegacySSPanelNodePageParse ||
		hasLegacyDecryptKey ||
		hasLegacySubscriptionUserAgent ||
		hasLegacySubscriptionExclusiveUserAgent ||
		hasLegacySubscriptionCustomQuerySuffix

	if hasLegacy {
		removeMapKeys(nexgen, "prefer_encrypt", "use_exclusive_mode", "sspanel_node_page_parse_enabled", "decrypt_key", "user_agent", "exclusive_user_agent", "custom_query_suffix")
		if hasLegacyPreferEncrypt {
			setMapBoolValue(subscription, "prefer_encrypt", preferEncrypt)
		}
		if hasLegacyUseExclusiveMode {
			setMapBoolValue(subscription, "use_exclusive_mode", useExclusiveMode)
		}
		if hasLegacySSPanelNodePageParse {
			setMapBoolValue(subscription, "sspanel_node_page_parse_enabled", sspanelNodePageParseEnabled)
		}
		if hasLegacyDecryptKey && strings.TrimSpace(decryptKey) != "" {
			setMapStringValue(subscription, "decrypt_key", decryptKey)
		}
		if hasLegacySubscriptionUserAgent && strings.TrimSpace(subscriptionUserAgent) != "" {
			setMapStringValue(subscription, "user_agent", strings.TrimSpace(subscriptionUserAgent))
		}
		if hasLegacySubscriptionExclusiveUserAgent && strings.TrimSpace(subscriptionExclusiveUserAgent) != "" {
			setMapStringValue(subscription, "exclusive_user_agent", strings.TrimSpace(subscriptionExclusiveUserAgent))
		}
		if hasLegacySubscriptionCustomQuerySuffix && strings.TrimSpace(subscriptionCustomQuerySuffix) != "" {
			setMapStringValue(subscription, "custom_query_suffix", strings.TrimSpace(subscriptionCustomQuerySuffix))
		}
	}

	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(doc); err != nil {
		return yamlContent
	}
	_ = encoder.Close()

	return stripNexGenProfileUnsupportedConfig(strings.TrimRight(buf.String(), "\n"))
}

func mergeProfileKeys(existing []string, additions ...string) []string {
	result := []string{}
	seen := map[string]struct{}{}
	for _, profile := range existing {
		profile = strings.TrimSpace(profile)
		if profile == "" {
			continue
		}
		if _, ok := seen[profile]; ok {
			continue
		}
		seen[profile] = struct{}{}
		result = append(result, profile)
	}
	for _, profile := range additions {
		profile = strings.TrimSpace(profile)
		if profile == "" {
			continue
		}
		if _, ok := seen[profile]; ok {
			continue
		}
		seen[profile] = struct{}{}
		result = append(result, profile)
	}
	return result
}

func manualProfileClientForAllowedClients(allowedClients []string) string {
	if len(allowedClients) == 1 && normalizeBuildClient(allowedClients[0]) == buildClientNexGenReact {
		return buildClientNexGenReact
	}
	return buildClientLegacy
}

func (h *Handlers) resolveAllowedProfiles(allowedProfiles, manualProfiles, allowedClients []string) ([]string, []StoredProfile, error) {
	resolved := mergeProfileKeys(allowedProfiles)
	created := []StoredProfile{}
	seenManual := map[string]struct{}{}
	manualClient := manualProfileClientForAllowedClients(allowedClients)
	for _, displayName := range manualProfiles {
		displayName = strings.TrimSpace(displayName)
		if displayName == "" {
			continue
		}
		if _, ok := seenManual[displayName]; ok {
			continue
		}
		seenManual[displayName] = struct{}{}
		profile, err := h.createManualProfileForClient(manualClient, displayName)
		if err != nil {
			return nil, nil, err
		}
		resolved = mergeProfileKeys(resolved, profile.Key)
		created = append(created, profile)
	}
	return resolved, created, nil
}

func validateYamlContent(yamlContent string) error {
	if strings.TrimSpace(yamlContent) == "" {
		return fmt.Errorf("配置内容为空")
	}
	var doc interface{}
	decoder := yaml.NewDecoder(strings.NewReader(yamlContent))
	decoder.KnownFields(false)
	if err := decoder.Decode(&doc); err != nil {
		return err
	}
	return nil
}

// ==================== 认证 ====================

func (h *Handlers) Login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "请求格式错误", 400)
		return
	}

	ac, err := validateCode(req.Code)
	if err != nil {
		jsonError(w, err.Error(), 401)
		return
	}

	token, err := generateJWT(ac.ID, ac.Name, "user", ac.AllowedProfiles, ac.AllowedClients)
	if err != nil {
		jsonError(w, "生成 Token 失败", 500)
		return
	}

	logAudit(ac.ID, ac.Name, "login", "", r.RemoteAddr)

	jsonResponse(w, map[string]interface{}{
		"token":           token,
		"name":            ac.Name,
		"permissions":     "user",
		"allowed_clients": ac.AllowedClients,
	})
}

func (h *Handlers) GetCurrentUserInfo(w http.ResponseWriter, r *http.Request) {
	claims := getClaims(r)
	if claims.Permissions == "admin" {
		jsonResponse(w, map[string]interface{}{
			"name":                        claims.CodeName,
			"permissions":                 claims.Permissions,
			"max_uses":                    -1,
			"used_count":                  0,
			"remaining_uses":              -1,
			"can_build":                   true,
			"build_status_text":           "管理员不限",
			"expires_at":                  nil,
			"is_active":                   true,
			"allowed_platforms":           []string{},
			"allowed_clients":             []string{},
			"integration_code_configured": hasAnyCustomFeatureBinding(uiColorFeatureKeys...),
		})
		return
	}

	ac, err := getActivationCodeByID(claims.CodeID)
	if err != nil {
		jsonResponse(w, map[string]interface{}{
			"name":                        claims.CodeName,
			"permissions":                 "user",
			"max_uses":                    0,
			"used_count":                  0,
			"remaining_uses":              0,
			"can_build":                   false,
			"build_status_text":           err.Error(),
			"expires_at":                  nil,
			"is_active":                   false,
			"allowed_platforms":           []string{},
			"allowed_clients":             claims.AllowedClients,
			"integration_code_configured": hasAnyCustomFeatureBinding(uiColorFeatureKeys...),
		})
		return
	}

	canBuild, statusText := getBuildSubmissionAvailability(ac)
	jsonResponse(w, map[string]interface{}{
		"name":                        ac.Name,
		"permissions":                 "user",
		"max_uses":                    ac.MaxUses,
		"used_count":                  ac.UsedCount,
		"remaining_uses":              getRemainingBuildSubmissions(ac),
		"can_build":                   canBuild,
		"build_status_text":           statusText,
		"expires_at":                  ac.ExpiresAt,
		"is_active":                   ac.IsActive,
		"allowed_platforms":           ac.AllowedPlatforms,
		"allowed_clients":             ac.AllowedClients,
		"integration_code_configured": hasAnyCustomFeatureBinding(uiColorFeatureKeys...),
	})
}

func (h *Handlers) AdminLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "请求格式错误", 400)
		return
	}

	if req.Password != cfg.AdminPassword {
		jsonError(w, "密码错误", 401)
		return
	}

	token, err := generateJWT(0, "管理员", "admin", nil, nil)
	if err != nil {
		jsonError(w, "生成 Token 失败", 500)
		return
	}

	logAudit(0, "管理员", "admin_login", "", r.RemoteAddr)

	jsonResponse(w, map[string]interface{}{
		"token":       token,
		"name":        "管理员",
		"permissions": "admin",
	})
}

// ==================== 配置档案 ====================

func (h *Handlers) ListProfiles(w http.ResponseWriter, r *http.Request) {
	claims := getClaims(r)
	client := normalizeBuildClient(r.URL.Query().Get("client"))
	if ok, message := canAccessClientForClaims(claims, client); !ok {
		jsonError(w, message, 403)
		return
	}

	loadKeysOnly := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("light")), "1") ||
		strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("summary")), "keys")
	loadFullProfiles := claims.Permissions == "admin" && !loadKeysOnly
	var profiles map[string]StoredProfile
	var err error
	if loadFullProfiles {
		profiles, err = h.listStoredProfilesForClient(client)
	} else if len(claims.AllowedProfiles) > 0 {
		profiles = map[string]StoredProfile{}
	} else {
		profiles, err = h.listStoredProfileKeysForClient(client)
	}
	if err != nil {
		jsonError(w, "加载档案列表失败: "+err.Error(), 500)
		return
	}

	list := []StoredProfile{}

	if loadFullProfiles || len(claims.AllowedProfiles) == 0 {
		names := make([]string, 0, len(profiles))
		for name := range profiles {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			list = append(list, profiles[name])
		}
	} else {
		names := append([]string(nil), claims.AllowedProfiles...)
		sort.Strings(names)

		for _, name := range names {
			if profile, exists := profiles[name]; exists {
				list = append(list, profile)
				continue
			}
			list = append(list, StoredProfile{
				Name:        name,
				Key:         name,
				DisplayName: name,
				Exists:      true,
			})
		}
	}

	jsonResponse(w, map[string]interface{}{"profiles": list})
}

func (h *Handlers) GetProfile(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(chi.URLParam(r, "name"))
	claims := getClaims(r)
	client := normalizeBuildClient(r.URL.Query().Get("client"))

	if ok, message := canAccessClientForClaims(claims, client); !ok {
		jsonError(w, message, 403)
		return
	}
	if !claims.canAccessProfile(name) {
		jsonError(w, "无权访问该档案", 403)
		return
	}

	content, _, lastUpdated, exists, err := h.getStoredProfileForClient(client, name)
	if err != nil {
		jsonError(w, "加载档案失败: "+err.Error(), 500)
		return
	}
	if !exists {
		logAudit(claims.CodeID, claims.CodeName, "load_profile", name+" (新建)", r.RemoteAddr)
		jsonResponse(w, map[string]interface{}{"yaml_content": "", "is_new": true})
		return
	}

	payload := struct {
		YamlContent string `json:"yaml_content"`
		LastUpdated string `json:"last_updated,omitempty"`
		DisplayName string `json:"display_name,omitempty"`
	}{YamlContent: content, LastUpdated: lastUpdated, DisplayName: profileDisplayNameFromYaml(content, name)}

	if payload.YamlContent != "" {
		if client == buildClientNexGenReact {
			cleaned := normalizeNexGenProfileConfig(payload.YamlContent)
			if cleaned != payload.YamlContent {
				payload.YamlContent = cleaned
				filePath, err := profileFilePath(name)
				if err == nil {
					_ = h.profileGitHubClient(client).SaveFileWithRetry(filePath, func(_ string) string {
						return cleaned
					}, "修复配置档案: "+name, 3)
					invalidateProfileCacheForClient(client)
				}
			}
		} else {
			cleaned := normalizeSubscriptionConfig(payload.YamlContent)
			if cleaned != payload.YamlContent {
				payload.YamlContent = cleaned
				filePath, err := profileFilePath(name)
				if err == nil {
					_ = h.profileGitHubClient(client).SaveFileWithRetry(filePath, func(_ string) string {
						return cleaned
					}, "修复配置档案: "+name, 3)
					invalidateProfileCacheForClient(client)
				}
			}
		}
	}
	if err := validateYamlContent(payload.YamlContent); err != nil {
		jsonError(w, "配置格式错误: "+err.Error(), 500)
		return
	}

	logAudit(claims.CodeID, claims.CodeName, "load_profile", name, r.RemoteAddr)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(payload)
}

func (h *Handlers) SaveProfile(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(chi.URLParam(r, "name"))
	claims := getClaims(r)
	client := normalizeBuildClient(r.URL.Query().Get("client"))

	var req struct {
		YamlContent     string            `json:"yaml_content"`
		BaseYamlContent string            `json:"base_yaml_content"`
		FormState       *ProfileFormState `json:"form_state"`
		DisplayName     string            `json:"display_name"`
		CreateNew       bool              `json:"create_new"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "请求格式错误", 400)
		return
	}
	if ok, message := canAccessClientForClaims(claims, client); !ok {
		jsonError(w, message, 403)
		return
	}
	if !claims.canAccessProfile(name) {
		jsonError(w, "无权操作该档案", 403)
		return
	}
	if req.FormState != nil {
		baseYamlContent := req.BaseYamlContent
		if strings.TrimSpace(baseYamlContent) == "" {
			baseYamlContent = req.YamlContent
		}
		var mergedYamlContent string
		var err error
		if client == buildClientNexGenReact {
			mergedYamlContent, err = mergeNexGenProfileYamlWithForm(normalizeNexGenProfileConfig(baseYamlContent), *req.FormState)
		} else {
			mergedYamlContent, err = mergeProfileYamlWithForm(baseYamlContent, *req.FormState)
		}
		if err != nil {
			jsonError(w, "合并配置失败: "+err.Error(), 400)
			return
		}
		req.YamlContent = mergedYamlContent
	}
	if client == buildClientNexGenReact {
		req.YamlContent = normalizeNexGenProfileConfig(req.YamlContent)
	} else {
		req.YamlContent = normalizeSubscriptionConfig(req.YamlContent)
	}
	if err := validateYamlContent(req.YamlContent); err != nil {
		jsonError(w, "配置格式错误: "+err.Error(), 400)
		return
	}

	filePath, err := profileFilePath(name)
	if err != nil {
		jsonError(w, err.Error(), 400)
		return
	}

	if err := h.ensureProfileBranchForClient(client); err != nil {
		jsonError(w, "准备配置分支失败: "+err.Error(), 500)
		return
	}
	profileGH := h.profileGitHubClient(client)
	_, sha, err := profileGH.GetFile(filePath)
	isNewProfile := false
	pathMissing := false
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			isNewProfile = true
			pathMissing = true
			sha = ""
		} else {
			jsonError(w, "加载档案失败: "+err.Error(), 500)
			return
		}
	}

	displayName := strings.TrimSpace(req.DisplayName)
	if displayName == "" {
		displayName = profileDisplayNameFromYaml(req.YamlContent, name)
	}
	if displayName == "" {
		displayName = name
	}

	if req.CreateNew {
		isNewProfile = true
		sha = ""
		uniqueName, err := h.createUniqueProfileKeyForClient(client, displayName)
		if err != nil {
			jsonError(w, err.Error(), 400)
			return
		}
		name = uniqueName
		filePath, err = profileFilePath(name)
		if err != nil {
			jsonError(w, err.Error(), 400)
			return
		}
	} else if pathMissing {
		sha = ""
	}

	if claims.Permissions != "admin" || isNewProfile {
		if client == buildClientNexGenReact {
			req.YamlContent = bindNexGenProfileTitle(req.YamlContent, displayName)
		} else {
			req.YamlContent = bindProfileTitle(req.YamlContent, displayName)
		}
	}

	_, err = profileGH.SaveFile(filePath, req.YamlContent, sha, "保存配置档案: "+name)
	if err != nil {
		jsonError(w, "保存失败: "+err.Error(), 500)
		return
	}
	if isNewProfile && claims.Permissions != "admin" {
		if err := appendAllowedProfileToCode(claims.CodeID, name); err != nil {
			jsonError(w, "保存成功，但同步档案权限失败: "+err.Error(), 500)
			return
		}
	}
	invalidateProfileCacheForClient(client)

	logAudit(claims.CodeID, claims.CodeName, "save_profile", name, r.RemoteAddr)

	jsonResponse(w, map[string]interface{}{
		"message":      "保存成功",
		"yaml_content": req.YamlContent,
		"profile": StoredProfile{
			Name:        name,
			Key:         name,
			DisplayName: displayName,
			Exists:      true,
		},
	})
}

func (h *Handlers) DeleteProfile(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(chi.URLParam(r, "name"))
	claims := getClaims(r)
	client := normalizeBuildClient(r.URL.Query().Get("client"))

	if ok, message := canAccessClientForClaims(claims, client); !ok {
		jsonError(w, message, 403)
		return
	}
	if !claims.canAccessProfile(name) {
		jsonError(w, "无权操作该档案", 403)
		return
	}

	filePath, err := profileFilePath(name)
	if err != nil {
		jsonError(w, err.Error(), 400)
		return
	}

	_, sha, _, exists, err := h.getStoredProfileForClient(client, name)
	if err != nil {
		jsonError(w, "加载档案失败: "+err.Error(), 500)
		return
	}
	if exists {
		if err := h.profileGitHubClient(client).DeleteFile(filePath, sha, "删除配置档案: "+name); err != nil {
			jsonError(w, "删除失败: "+err.Error(), 500)
			return
		}
		invalidateProfileCacheForClient(client)
	}

	logAudit(claims.CodeID, claims.CodeName, "delete_profile", name, r.RemoteAddr)

	jsonResponse(w, map[string]string{"message": "删除成功"})
}

// ==================== 构建 ====================

const maxConcurrentBuildJobs = 20
const maxConcurrentMacOSBuildJobs = 5

const recentWorkflowRunsSearchLimit = 30
const fastWorkflowRunsSearchLimit = 20

const (
	buildClientLegacy      = "xboard_mihomo_sub"
	buildClientNexGenReact = "nexgen_react"
)

type BuildClientConfig struct {
	ID             string   `json:"id"`
	Label          string   `json:"label"`
	SourceOwner    string   `json:"source_owner"`
	SourceRepo     string   `json:"source_repo"`
	WorkflowFile   string   `json:"workflow_file"`
	DefaultBranch  string   `json:"default_branch"`
	DefaultCore    string   `json:"default_core"`
	CoreSelectable bool     `json:"core_selectable"`
	PlatformValues []string `json:"platform_values"`
}

var buildPlatformCatalog = []string{
	"windows-amd64",
	"windows-arm64",
	"android",
	"android-tv",
	"linux-amd64",
	"linux-arm64",
	"macos-amd64",
	"macos-arm64",
}

var buildCorePlatformCatalog = map[string][]string{
	"mihomo": buildPlatformCatalog,
	"xray": {
		"windows-amd64",
		"windows-arm64",
		"android",
		"macos-amd64",
		"macos-arm64",
	},
}

var nexGenReactPlatformCatalog = []string{
	"windows-amd64",
	"windows-arm64",
	"android",
	"macos-amd64",
	"macos-arm64",
}

func normalizeBuildClient(client string) string {
	client = strings.TrimSpace(strings.ToLower(client))
	switch client {
	case "", "legacy", "xboard", "xboard_mihomo_sub":
		return buildClientLegacy
	case "nexgen", "nexgen-react", "nexgen_react":
		return buildClientNexGenReact
	default:
		return client
	}
}

func buildClientLabel(client string) string {
	switch normalizeBuildClient(client) {
	case buildClientNexGenReact:
		return "NexGen Client React"
	default:
		return "Xboard-Mihomo_sub"
	}
}

func buildClientConfig(client string) (BuildClientConfig, error) {
	client = normalizeBuildClient(client)
	switch client {
	case buildClientLegacy:
		return BuildClientConfig{
			ID:             buildClientLegacy,
			Label:          buildClientLabel(buildClientLegacy),
			SourceOwner:    cfg.GithubOwner,
			SourceRepo:     cfg.GithubRepo,
			WorkflowFile:   "build.yaml",
			DefaultBranch:  "main",
			DefaultCore:    "mihomo",
			CoreSelectable: true,
			PlatformValues: buildPlatformCatalog,
		}, nil
	case buildClientNexGenReact:
		return BuildClientConfig{
			ID:             buildClientNexGenReact,
			Label:          buildClientLabel(buildClientNexGenReact),
			SourceOwner:    cfg.NexGenBuildOwner,
			SourceRepo:     cfg.NexGenBuildRepo,
			WorkflowFile:   "build-nexgen-react.yaml",
			DefaultBranch:  "main",
			DefaultCore:    "nexgen",
			CoreSelectable: false,
			PlatformValues: nexGenReactPlatformCatalog,
		}, nil
	default:
		return BuildClientConfig{}, fmt.Errorf("无效的客户端：%s", client)
	}
}

func normalizeBuildCore(core string) string {
	core = strings.TrimSpace(strings.ToLower(core))
	if core == "" {
		return "mihomo"
	}
	return core
}

func validateBuildCore(core string) (string, error) {
	core = normalizeBuildCore(core)
	if _, ok := buildCorePlatformCatalog[core]; !ok {
		return "", fmt.Errorf("无效的打包内核：%s", core)
	}
	return core, nil
}

func validateBuildCoreForClient(client, core string) (string, error) {
	clientConfig, err := buildClientConfig(client)
	if err != nil {
		return "", err
	}
	if !clientConfig.CoreSelectable {
		return clientConfig.DefaultCore, nil
	}
	return validateBuildCore(core)
}

func buildPlatformCatalogForClientCore(client, core string) []string {
	client = normalizeBuildClient(client)
	if client == buildClientNexGenReact {
		return nexGenReactPlatformCatalog
	}
	core = normalizeBuildCore(core)
	if platforms, ok := buildCorePlatformCatalog[core]; ok {
		return platforms
	}
	return buildPlatformCatalog
}

func buildPlatformCatalogForCore(core string) []string {
	return buildPlatformCatalogForClientCore(buildClientLegacy, core)
}

func buildCoreLabel(core string) string {
	switch normalizeBuildCore(core) {
	case "xray":
		return "Xray"
	case "nexgen":
		return "NexGen"
	default:
		return "Mihomo"
	}
}

func isPlatformInCatalog(platform string, catalog []string) bool {
	for _, item := range catalog {
		if platform == item {
			return true
		}
	}
	return false
}

type BuildJobDemand struct {
	Total int
	MacOS int
}

type BuildQueueSnapshot struct {
	ActiveRuns         int    `json:"active_runs"`
	ActiveJobs         int    `json:"active_jobs"`
	ActiveMacOSJobs    int    `json:"active_macos_jobs"`
	MaxJobs            int    `json:"max_jobs"`
	MaxMacOSJobs       int    `json:"max_macos_jobs"`
	RequestedPlatforms string `json:"requested_platforms,omitempty"`
	RequestedJobs      int    `json:"requested_jobs,omitempty"`
	RequestedMacOSJobs int    `json:"requested_macos_jobs,omitempty"`
	RemainingJobs      int    `json:"remaining_jobs"`
	RemainingMacOSJobs int    `json:"remaining_macos_jobs"`
	Available          bool   `json:"available"`
	Message            string `json:"message,omitempty"`
}

type PendingBuild struct {
	CodeID      int
	Client      string
	Profile     string
	Tag         string
	Branch      string
	Core        string
	Platforms   string
	TriggeredAt time.Time
}

// 缓存构建请求对应的最近 run_id，避免每次轮询都搜索所有 runs
var profileRunCache = make(map[string]int64)
var pendingBuildCache = make(map[string]PendingBuild)
var buildStateCacheMu sync.RWMutex

func estimateBuildJobDemand(core, platforms string) BuildJobDemand {
	return estimateBuildJobDemandForClient(buildClientLegacy, core, platforms)
}

func estimateBuildJobDemandForClient(client, core, platforms string) BuildJobDemand {
	selectedJobs := expandRequestedBuildPlatformSetForClient(client, core, platforms)

	demand := BuildJobDemand{Total: len(selectedJobs)}
	for job := range selectedJobs {
		if strings.HasPrefix(job, "macos-") {
			demand.MacOS++
		}
	}
	return demand
}

func expandRequestedBuildPlatformSet(core, platforms string) map[string]struct{} {
	return expandRequestedBuildPlatformSetForClient(buildClientLegacy, core, platforms)
}

func expandRequestedBuildPlatformSetForClient(client, core, platforms string) map[string]struct{} {
	selectedJobs := make(map[string]struct{})
	catalog := buildPlatformCatalogForClientCore(client, core)
	normalized := strings.TrimSpace(strings.ToLower(platforms))
	if normalized == "" {
		normalized = "all"
	}

	addJob := func(job string) {
		if job != "" {
			selectedJobs[job] = struct{}{}
		}
	}

	addJobsByPlatform := func(token string) {
		switch token {
		case "all":
			for _, job := range catalog {
				addJob(job)
			}
		case "windows":
			if isPlatformInCatalog("windows", catalog) {
				addJob("windows")
				return
			}
			for _, job := range []string{"windows-amd64", "windows-arm64"} {
				if isPlatformInCatalog(job, catalog) {
					addJob(job)
				}
			}
		case "linux":
			for _, job := range []string{"linux-amd64", "linux-arm64"} {
				if isPlatformInCatalog(job, catalog) {
					addJob(job)
				}
			}
		case "macos":
			for _, job := range []string{"macos-amd64", "macos-arm64"} {
				if isPlatformInCatalog(job, catalog) {
					addJob(job)
				}
			}
		case "windows-amd64", "windows-arm64",
			"android", "android-tv",
			"linux-amd64", "linux-arm64",
			"macos-amd64", "macos-arm64":
			if isPlatformInCatalog(token, catalog) {
				addJob(token)
			}
		}
	}

	for _, token := range strings.Split(normalized, ",") {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if token == "all" {
			addJobsByPlatform(token)
			break
		}
		addJobsByPlatform(token)
	}

	return selectedJobs
}

func normalizeBuildPlatformList(platforms []string) ([]string, error) {
	return normalizeBuildPlatformListForCore("mihomo", platforms)
}

func normalizeBuildPlatformListForCore(core string, platforms []string) ([]string, error) {
	return normalizeBuildPlatformListForClientCore(buildClientLegacy, core, platforms)
}

func normalizeBuildPlatformListForClientCore(client, core string, platforms []string) ([]string, error) {
	catalog := buildPlatformCatalogForClientCore(client, core)
	clientLabel := buildClientLabel(client)
	result := []string{}
	seen := map[string]struct{}{}
	addExpandedGroup := func(token string, items []string) error {
		added := false
		for _, item := range items {
			if !isPlatformInCatalog(item, catalog) {
				continue
			}
			added = true
			if _, ok := seen[item]; !ok {
				seen[item] = struct{}{}
				result = append(result, item)
			}
		}
		if !added {
			return fmt.Errorf("%s 不支持打包平台：%s", clientLabel, token)
		}
		return nil
	}
	for _, platform := range platforms {
		for _, token := range strings.Split(strings.ToLower(strings.TrimSpace(platform)), ",") {
			token = strings.TrimSpace(token)
			if token == "" {
				continue
			}
			if token == "all" {
				for _, item := range catalog {
					if _, ok := seen[item]; !ok {
						seen[item] = struct{}{}
						result = append(result, item)
					}
				}
				continue
			}
			switch token {
			case "windows":
				if isPlatformInCatalog("windows", catalog) {
					if _, ok := seen["windows"]; !ok {
						seen["windows"] = struct{}{}
						result = append(result, "windows")
					}
				} else if err := addExpandedGroup(token, []string{"windows-amd64", "windows-arm64"}); err != nil {
					return nil, err
				}
			case "linux":
				if err := addExpandedGroup(token, []string{"linux-amd64", "linux-arm64"}); err != nil {
					return nil, err
				}
			case "macos":
				if err := addExpandedGroup(token, []string{"macos-amd64", "macos-arm64"}); err != nil {
					return nil, err
				}
			default:
				matched := false
				for _, item := range catalog {
					if token == item {
						matched = true
						if _, ok := seen[item]; !ok {
							seen[item] = struct{}{}
							result = append(result, item)
						}
						break
					}
				}
				if !matched {
					return nil, fmt.Errorf("%s 不支持打包平台：%s", clientLabel, token)
				}
			}
		}
	}
	return result, nil
}

func expandRequestedBuildPlatforms(core, platforms string) ([]string, error) {
	return normalizeBuildPlatformListForCore(core, []string{platforms})
}

func expandRequestedBuildPlatformsForClient(client, core, platforms string) ([]string, error) {
	return normalizeBuildPlatformListForClientCore(client, core, []string{platforms})
}

func validateAllowedBuildPlatforms(allowedPlatforms []string) ([]string, error) {
	return validateAllowedBuildPlatformsForClient(buildClientLegacy, allowedPlatforms)
}

func validateAllowedBuildClients(allowedClients []string) ([]string, error) {
	if len(allowedClients) == 0 {
		return []string{}, nil
	}
	result := []string{}
	seen := map[string]struct{}{}
	for _, client := range allowedClients {
		client = normalizeBuildClient(client)
		switch client {
		case "":
			continue
		case buildClientLegacy, buildClientNexGenReact:
			if _, ok := seen[client]; ok {
				continue
			}
			seen[client] = struct{}{}
			result = append(result, client)
		default:
			return nil, fmt.Errorf("无效的客户端权限：%s", client)
		}
	}
	return result, nil
}

func canAccessClientForCodeID(codeID int, client string) (bool, string) {
	ac, err := getActivationCodeByID(codeID)
	if err != nil {
		return false, err.Error()
	}
	claims := &Claims{
		CodeID:         ac.ID,
		Permissions:    ac.Permissions,
		AllowedClients: ac.AllowedClients,
	}
	if claims.canAccessClient(client) {
		return true, ""
	}
	return false, "当前激活码不允许访问该客户端"
}

func canAccessClientForClaims(claims *Claims, client string) (bool, string) {
	if claims == nil {
		return false, "未授权"
	}
	if claims.Permissions == "admin" {
		return true, ""
	}
	return canAccessClientForCodeID(claims.CodeID, client)
}

func validateAllowedBuildPlatformsForClient(client string, allowedPlatforms []string) ([]string, error) {
	if len(allowedPlatforms) == 0 {
		return []string{}, nil
	}
	if normalizeBuildClient(client) == buildClientNexGenReact {
		return normalizeAllowedNexGenBuildPlatforms(allowedPlatforms)
	}
	normalized, err := normalizeBuildPlatformList(allowedPlatforms)
	if err != nil {
		return nil, err
	}
	if len(normalized) == 0 {
		return nil, fmt.Errorf("允许打包平台配置无效")
	}
	return normalized, nil
}

func normalizeAllowedNexGenBuildPlatforms(allowedPlatforms []string) ([]string, error) {
	result := []string{}
	seen := map[string]struct{}{}
	add := func(platform string) {
		if platform == "" {
			return
		}
		if _, ok := seen[platform]; ok {
			return
		}
		seen[platform] = struct{}{}
		result = append(result, platform)
	}
	addMacOS := func() {
		add("macos-amd64")
		add("macos-arm64")
	}
	for _, platform := range allowedPlatforms {
		for _, token := range strings.Split(strings.ToLower(strings.TrimSpace(platform)), ",") {
			switch token {
			case "":
				continue
			case "all":
				for _, item := range nexGenReactPlatformCatalog {
					add(item)
				}
			case "windows":
				add("windows-amd64")
				add("windows-arm64")
			case "windows-amd64", "windows-arm64":
				add(token)
			case "android":
				add("android")
			case "macos":
				addMacOS()
			case "macos-amd64", "macos-arm64":
				add(token)
			}
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("允许打包平台配置无效")
	}
	return result, nil
}

func canUseBuildPlatforms(allowedPlatforms []string, core, requestedPlatforms string) (bool, string) {
	return canUseBuildPlatformsForClient(buildClientLegacy, allowedPlatforms, core, requestedPlatforms)
}

func canUseBuildPlatformsForClient(client string, allowedPlatforms []string, core, requestedPlatforms string) (bool, string) {
	allowed, err := validateAllowedBuildPlatformsForClient(client, allowedPlatforms)
	if err != nil {
		return false, err.Error()
	}
	if len(allowed) == 0 {
		return true, ""
	}

	requested, err := expandRequestedBuildPlatformsForClient(client, core, requestedPlatforms)
	if err != nil {
		return false, err.Error()
	}
	if len(requested) == 0 {
		return false, "无效的打包平台选择"
	}

	allowedSet := map[string]struct{}{}
	for _, platform := range allowed {
		allowedSet[platform] = struct{}{}
	}
	for _, platform := range requested {
		if _, ok := allowedSet[platform]; !ok {
			return false, "当前激活码不允许打包平台：" + platform
		}
	}
	return true, ""
}

func isMacOSWorkflowJob(job WorkflowJob) bool {
	return strings.Contains(strings.ToLower(job.Name), "macos")
}

func formatBuildQueueExceededMessage(activeJobs, activeMacOSJobs int, demand BuildJobDemand) string {
	base := fmt.Sprintf(
		"当前活跃 %d/%d 个构建 job（macOS %d/%d），本次请求需要 %d 个 job",
		activeJobs, maxConcurrentBuildJobs, activeMacOSJobs, maxConcurrentMacOSBuildJobs, demand.Total,
	)
	if demand.MacOS > 0 {
		base += fmt.Sprintf("（其中 macOS %d 个）", demand.MacOS)
	}
	return base + "，超过并发限制，请稍后再试"
}

func (h *Handlers) getActiveBuildJobUsage() (BuildQueueSnapshot, error) {
	return h.getActiveBuildJobUsageForClient(buildClientLegacy)
}

func (h *Handlers) getActiveBuildJobUsageForClient(client string) (BuildQueueSnapshot, error) {
	snapshot := BuildQueueSnapshot{
		MaxJobs:      maxConcurrentBuildJobs,
		MaxMacOSJobs: maxConcurrentMacOSBuildJobs,
	}

	clientConfig, err := buildClientConfig(client)
	if err != nil {
		return snapshot, err
	}
	runs, err := h.gh.GetActiveWorkflowRuns(cfg.BuildOwner, cfg.BuildRepo, clientConfig.WorkflowFile)
	if err != nil {
		return snapshot, err
	}

	snapshot.ActiveRuns = len(runs)
	for _, run := range runs {
		jobs, jobsErr := h.gh.GetWorkflowRunJobs(cfg.BuildOwner, cfg.BuildRepo, run.ID)
		if jobsErr == nil && len(jobs) > 0 {
			for _, job := range jobs {
				if !isActiveWorkflowStatus(job.Status) {
					continue
				}
				snapshot.ActiveJobs++
				if isMacOSWorkflowJob(job) {
					snapshot.ActiveMacOSJobs++
				}
			}
			continue
		}

		inputs, inputsErr := h.gh.GetWorkflowRunInputs(cfg.BuildOwner, cfg.BuildRepo, run.ID)
		demand := BuildJobDemand{Total: 1}
		if inputsErr == nil {
			inputClient := normalizeBuildClient(inputs["client"])
			if inputClient == buildClientLegacy && client != buildClientLegacy && strings.TrimSpace(inputs["client"]) == "" {
				inputClient = normalizeBuildClient(client)
			}
			if estimated := estimateBuildJobDemandForClient(inputClient, inputs["core"], inputs["platforms"]); estimated.Total > 0 {
				demand = estimated
			}
		}
		snapshot.ActiveJobs += demand.Total
		snapshot.ActiveMacOSJobs += demand.MacOS
	}

	snapshot.RemainingJobs = maxConcurrentBuildJobs - snapshot.ActiveJobs
	if snapshot.RemainingJobs < 0 {
		snapshot.RemainingJobs = 0
	}
	snapshot.RemainingMacOSJobs = maxConcurrentMacOSBuildJobs - snapshot.ActiveMacOSJobs
	if snapshot.RemainingMacOSJobs < 0 {
		snapshot.RemainingMacOSJobs = 0
	}
	snapshot.Available = snapshot.ActiveJobs < maxConcurrentBuildJobs &&
		snapshot.ActiveMacOSJobs < maxConcurrentMacOSBuildJobs

	return snapshot, nil
}

func (h *Handlers) getBuildQueueSnapshot(core, platforms string) (BuildQueueSnapshot, error) {
	return h.getBuildQueueSnapshotForClient(buildClientLegacy, core, platforms)
}

func (h *Handlers) getBuildQueueSnapshotForClient(client, core, platforms string) (BuildQueueSnapshot, error) {
	client = normalizeBuildClient(client)
	core = normalizeBuildCore(core)
	cacheKey := client + "|" + core + "|" + strings.TrimSpace(platforms)
	if cacheKey == "" {
		cacheKey = client + "|" + core + "|all"
	}
	if cached, ok := buildQueueSnapshotCache.get(cacheKey); ok {
		return cached, nil
	}

	buildCacheMu.Lock()
	defer buildCacheMu.Unlock()
	if cached, ok := buildQueueSnapshotCache.get(cacheKey); ok {
		return cached, nil
	}

	snapshot, err := h.getActiveBuildJobUsageForClient(client)
	if err != nil {
		return snapshot, err
	}

	demand := estimateBuildJobDemandForClient(client, core, platforms)
	if demand.Total > 0 {
		snapshot.RequestedPlatforms = platforms
		snapshot.RequestedJobs = demand.Total
		snapshot.RequestedMacOSJobs = demand.MacOS
		snapshot.Available = snapshot.ActiveJobs+demand.Total <= maxConcurrentBuildJobs &&
			snapshot.ActiveMacOSJobs+demand.MacOS <= maxConcurrentMacOSBuildJobs
		if !snapshot.Available {
			snapshot.Message = formatBuildQueueExceededMessage(snapshot.ActiveJobs, snapshot.ActiveMacOSJobs, demand)
		}
	}

	buildQueueSnapshotCache.set(cacheKey, snapshot, buildQueueSnapshotTTL)
	return snapshot, nil
}

func buildPendingCacheKey(codeID int, client, profile, tag, branch, core, platforms string) string {
	return fmt.Sprintf("code:%d|client:%s|profile:%s|tag:%s|branch:%s|core:%s|platforms:%s", codeID, normalizeBuildClient(client), profile, tag, branch, normalizeBuildCore(core), platforms)
}

func rememberPendingBuild(codeID int, profile, tag, branch, core, platforms string) PendingBuild {
	return rememberPendingBuildForClient(codeID, buildClientLegacy, profile, tag, branch, core, platforms)
}

func rememberPendingBuildForClient(codeID int, client, profile, tag, branch, core, platforms string) PendingBuild {
	pending := PendingBuild{
		CodeID:      codeID,
		Client:      normalizeBuildClient(client),
		Profile:     profile,
		Tag:         tag,
		Branch:      branch,
		Core:        normalizeBuildCore(core),
		Platforms:   platforms,
		TriggeredAt: time.Now().UTC(),
	}
	buildStateCacheMu.Lock()
	pendingBuildCache[buildPendingCacheKey(codeID, client, profile, tag, branch, core, platforms)] = pending
	buildStateCacheMu.Unlock()
	return pending
}

func getPendingBuild(codeID int, profile, tag, branch, core, platforms string) (PendingBuild, bool) {
	return getPendingBuildForClient(codeID, buildClientLegacy, profile, tag, branch, core, platforms)
}

func getPendingBuildForClient(codeID int, client, profile, tag, branch, core, platforms string) (PendingBuild, bool) {
	buildStateCacheMu.RLock()
	pending, ok := pendingBuildCache[buildPendingCacheKey(codeID, client, profile, tag, branch, core, platforms)]
	buildStateCacheMu.RUnlock()
	return pending, ok
}

func getLatestPendingBuildByProfile(codeID int, profile string) (PendingBuild, bool) {
	return getLatestPendingBuildByClientProfile(codeID, buildClientLegacy, profile)
}

func getLatestPendingBuildByClientProfile(codeID int, client, profile string) (PendingBuild, bool) {
	client = normalizeBuildClient(client)
	var latest PendingBuild
	found := false
	buildStateCacheMu.RLock()
	defer buildStateCacheMu.RUnlock()
	for _, pending := range pendingBuildCache {
		if codeID > 0 && pending.CodeID != codeID {
			continue
		}
		if pending.Profile != profile {
			continue
		}
		if normalizeBuildClient(pending.Client) != client {
			continue
		}
		if !found || pending.TriggeredAt.After(latest.TriggeredAt) {
			latest = pending
			found = true
		}
	}
	return latest, found
}

func clearPendingBuild(codeID int, profile, tag, branch, core, platforms string) {
	clearPendingBuildForClient(codeID, buildClientLegacy, profile, tag, branch, core, platforms)
}

func clearPendingBuildForClient(codeID int, client, profile, tag, branch, core, platforms string) {
	buildStateCacheMu.Lock()
	delete(pendingBuildCache, buildPendingCacheKey(codeID, client, profile, tag, branch, core, platforms))
	buildStateCacheMu.Unlock()
}

func getCachedProfileRunID(cacheKey string) (int64, bool) {
	buildStateCacheMu.RLock()
	runID, ok := profileRunCache[cacheKey]
	buildStateCacheMu.RUnlock()
	return runID, ok
}

func setCachedProfileRunID(cacheKey string, runID int64) {
	if cacheKey == "" || runID <= 0 {
		return
	}
	buildStateCacheMu.Lock()
	profileRunCache[cacheKey] = runID
	buildStateCacheMu.Unlock()
}

func deleteCachedProfileRunID(cacheKey string) {
	if cacheKey == "" {
		return
	}
	buildStateCacheMu.Lock()
	delete(profileRunCache, cacheKey)
	buildStateCacheMu.Unlock()
}

func parseGitHubTimestamp(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

func parseDBTimestamp(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, false
	}
	layouts := []string{
		"2006-01-02 15:04:05",
		time.RFC3339,
		"2006-01-02T15:04:05Z07:00",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
}

func matchRunByPendingBuild(run *WorkflowRun, pending PendingBuild) bool {
	runCreatedAt, ok := parseGitHubTimestamp(run.CreatedAt)
	if !ok {
		return false
	}
	earliest := pending.TriggeredAt.Add(-30 * time.Second)
	latest := pending.TriggeredAt.Add(15 * time.Minute)
	if runCreatedAt.Before(earliest) || runCreatedAt.After(latest) {
		return false
	}
	return true
}

func buildRunCacheKey(codeID int, profile, requestID, tag, branch, core, platforms string) string {
	return buildRunCacheKeyForClient(codeID, buildClientLegacy, profile, requestID, tag, branch, core, platforms)
}

func buildRunCacheKeyForClient(codeID int, client, profile, requestID, tag, branch, core, platforms string) string {
	if requestID != "" {
		return "request:" + requestID
	}
	return fmt.Sprintf("code:%d|client:%s|profile:%s|tag:%s|branch:%s|core:%s|platforms:%s", codeID, normalizeBuildClient(client), profile, tag, branch, normalizeBuildCore(core), platforms)
}

func buildRequestMatches(inputs map[string]string, profile, requestID, tag, branch, core, platforms string) bool {
	return buildRequestMatchesForClient(inputs, buildClientLegacy, profile, requestID, tag, branch, core, platforms)
}

func buildRequestMatchesForClient(inputs map[string]string, client, profile, requestID, tag, branch, core, platforms string) bool {
	if len(inputs) == 0 {
		return false
	}
	if requestID != "" {
		return strings.TrimSpace(inputs["request_id"]) == strings.TrimSpace(requestID)
	}
	if inputs["profile"] != profile {
		return false
	}
	inputClient := normalizeBuildClient(inputs["client"])
	if strings.TrimSpace(inputs["client"]) == "" {
		inputClient = buildClientLegacy
	}
	if normalizeBuildClient(client) != inputClient {
		return false
	}
	if tag != "" && inputs["tag"] != tag {
		return false
	}
	if branch != "" && inputs["branch"] != branch {
		return false
	}
	inputCore := normalizeBuildCore(inputs["core"])
	if inputs["core"] == "" {
		inputCore = "mihomo"
	}
	if normalizeBuildCore(core) != inputCore {
		return false
	}
	if platforms != "" && inputs["platforms"] != platforms {
		return false
	}
	return true
}

func isActiveWorkflowStatus(status string) bool {
	return status != "" && status != "completed"
}

func buildRecordRequestID(record *BuildRecord) string {
	if record == nil {
		return ""
	}
	if strings.TrimSpace(record.RequestID) != "" {
		return strings.TrimSpace(record.RequestID)
	}
	if record.ID > 0 {
		return strconv.FormatInt(record.ID, 10)
	}
	return ""
}

func buildWorkflowRunURL(runID int64) string {
	if runID <= 0 {
		return ""
	}
	return fmt.Sprintf("https://github.com/%s/%s/actions/runs/%d", cfg.BuildOwner, cfg.BuildRepo, runID)
}

func buildWorkflowFileForClient(client string) string {
	clientConfig, err := buildClientConfig(client)
	if err != nil {
		return "build.yaml"
	}
	return clientConfig.WorkflowFile
}

func resolveBuildSourceRepo(core string) (string, string) {
	return resolveBuildSourceRepoForClient(buildClientLegacy, core)
}

func resolveBuildSourceRepoForClient(client, core string) (string, string) {
	clientConfig, err := buildClientConfig(client)
	if err == nil && normalizeBuildClient(client) == buildClientNexGenReact {
		return clientConfig.SourceOwner, clientConfig.SourceRepo
	}
	if normalizeBuildCore(core) == "xray" {
		return cfg.XrayBuildOwner, cfg.XrayBuildRepo
	}
	return cfg.GithubOwner, cfg.GithubRepo
}

func extractBuildRequestIDFromText(value string) string {
	match := buildRequestIDPattern.FindString(strings.TrimSpace(value))
	return strings.TrimSpace(match)
}

func buildReleaseTag(record *BuildRecord) string {
	if record == nil {
		return ""
	}
	if strings.TrimSpace(record.ReleaseTag) != "" {
		return strings.TrimSpace(record.ReleaseTag)
	}
	if record.RunID <= 0 {
		return ""
	}
	return fmt.Sprintf("%s-%s-%d", record.Profile, record.Tag, record.RunID)
}

func canAccessBuildRecord(claims *Claims, record *BuildRecord) bool {
	if claims == nil || record == nil {
		return false
	}
	if claims.Permissions == "admin" {
		return true
	}
	if claims.CodeID != record.CodeID {
		return false
	}
	ok, _ := canAccessClientForCodeID(claims.CodeID, record.Client)
	return ok
}

func buildRecordResponse(record BuildRecord) map[string]interface{} {
	releaseTag := buildReleaseTag(&record)
	return map[string]interface{}{
		"id":               record.ID,
		"code_id":          record.CodeID,
		"code_name":        record.CodeName,
		"request_id":       buildRecordRequestID(&record),
		"client":           normalizeBuildClient(record.Client),
		"client_label":     buildClientLabel(record.Client),
		"profile":          record.Profile,
		"tag":              record.Tag,
		"branch":           record.Branch,
		"core":             normalizeBuildCore(record.Core),
		"platforms":        record.Platforms,
		"run_id":           record.RunID,
		"run_url":          record.RunURL,
		"status":           record.Status,
		"conclusion":       record.Conclusion,
		"status_source":    record.StatusSource,
		"progress":         normalizeBuildProgress(&record),
		"progress_percent": normalizeBuildProgress(&record),
		"progress_text":    record.ProgressText,
		"progress_stage":   record.ProgressStage,
		"bound_at":         record.BoundAt,
		"finished_at":      record.FinishedAt,
		"last_sync_at":     record.LastSyncAt,
		"created_at":       record.CreatedAt,
		"updated_at":       record.UpdatedAt,
		"release_tag":      releaseTag,
		"download_ready":   releaseTag != "" && record.Status == "completed" && record.Conclusion == "success",
	}
}

func buildRecordInputs(record *BuildRecord) map[string]string {
	if record == nil {
		return nil
	}
	return map[string]string{
		"client":     normalizeBuildClient(record.Client),
		"profile":    record.Profile,
		"tag":        record.Tag,
		"branch":     record.Branch,
		"core":       normalizeBuildCore(record.Core),
		"platforms":  record.Platforms,
		"request_id": buildRecordRequestID(record),
	}
}

func buildStatusResponseFromRecord(record *BuildRecord) map[string]interface{} {
	if record == nil {
		return map[string]interface{}{"found": false}
	}
	return map[string]interface{}{
		"found":            true,
		"record_id":        record.ID,
		"run_id":           record.RunID,
		"run_url":          record.RunURL,
		"status":           record.Status,
		"conclusion":       record.Conclusion,
		"status_source":    record.StatusSource,
		"bound_at":         record.BoundAt,
		"finished_at":      record.FinishedAt,
		"last_sync_at":     record.LastSyncAt,
		"progress":         normalizeBuildProgress(record),
		"progress_percent": normalizeBuildProgress(record),
		"progress_text":    record.ProgressText,
		"progress_stage":   record.ProgressStage,
		"created_at":       record.CreatedAt,
		"updated_at":       record.UpdatedAt,
		"release_tag":      buildReleaseTag(record),
		"request_id":       buildRecordRequestID(record),
		"client":           normalizeBuildClient(record.Client),
		"inputs":           buildRecordInputs(record),
	}
}

func shouldUseLocalBuildStatus(record *BuildRecord) bool {
	if record == nil {
		return false
	}
	if record.Status == "cancel_requested" {
		return true
	}
	return record.Status == "completed" && record.Conclusion != ""
}

func shouldSyncBuildStatusWithGitHub(r *http.Request, record *BuildRecord) bool {
	if record == nil {
		return true
	}
	if shouldUseLocalBuildStatus(record) {
		return false
	}
	query := r.URL.Query()
	forceSync := strings.EqualFold(strings.TrimSpace(query.Get("sync")), "1") ||
		strings.EqualFold(strings.TrimSpace(query.Get("force_sync")), "1")
	if !forceSync && record.RunID > 0 {
		return false
	}
	if record.LastSyncAt == "" {
		return forceSync
	}
	lastSyncAt, ok := parseDBTimestamp(record.LastSyncAt)
	if !ok {
		return forceSync
	}
	return forceSync && time.Since(lastSyncAt) >= buildStatusGitHubSyncMinInterval
}

func isBuildStatusSyncRequested(r *http.Request) bool {
	query := r.URL.Query()
	return strings.EqualFold(strings.TrimSpace(query.Get("sync")), "1") ||
		strings.EqualFold(strings.TrimSpace(query.Get("force_sync")), "1")
}

func buildStatusNextSyncAt(record *BuildRecord) string {
	if record == nil || record.LastSyncAt == "" {
		return ""
	}
	lastSyncAt, ok := parseDBTimestamp(record.LastSyncAt)
	if !ok {
		return ""
	}
	return lastSyncAt.Add(buildStatusGitHubSyncMinInterval).Local().Format("2006-01-02 15:04:05 MST")
}

func applyBuildStatusSyncHints(result map[string]interface{}, record *BuildRecord, syncRequested, syncWithGitHub bool) {
	if result == nil || record == nil {
		return
	}
	result["github_sync_interval_seconds"] = int(buildStatusGitHubSyncMinInterval.Seconds())
	if !syncRequested {
		result["local_status"] = true
		return
	}
	result["sync_requested"] = true
	if syncWithGitHub {
		result["sync_skipped"] = false
		return
	}
	result["sync_skipped"] = true
	if nextSyncAt := buildStatusNextSyncAt(record); nextSyncAt != "" {
		result["sync_available_at"] = nextSyncAt
	}
}

func shouldIncludeBuildJobs(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("include_jobs")), "1")
}

func normalizeBuildProgress(record *BuildRecord) int {
	if record == nil {
		return 0
	}
	progress := record.Progress
	if progress < 0 {
		progress = 0
	}
	if record.Status == "completed" {
		progress = 100
	}
	if record.Status == "cancel_requested" && progress < 90 {
		progress = 90
	}
	if progress > 100 {
		progress = 100
	}
	return progress
}

func buildProgressForEvent(status, conclusion string, progress int, progressText, progressStage string) (int, string, string) {
	status = strings.TrimSpace(status)
	conclusion = strings.TrimSpace(conclusion)
	progressText = strings.TrimSpace(progressText)
	progressStage = strings.TrimSpace(progressStage)
	if progress < 0 {
		progress = -1
	}
	if status == "completed" {
		progress = 100
		if progressStage == "" {
			progressStage = "completed"
		}
		if progressText == "" {
			if conclusion == "success" {
				progressText = "打包完成，产物已上传"
			} else if conclusion != "" {
				progressText = "打包已结束：" + conclusion
			} else {
				progressText = "打包已结束"
			}
		}
		return progress, progressText, progressStage
	}
	if progress < 0 {
		switch status {
		case "dispatching":
			progress = 5
		case "queued", "waiting", "pending", "requested":
			progress = 12
		case "in_progress":
			progress = 30
		case "cancel_requested":
			progress = 90
		default:
			progress = 10
		}
	}
	if progressText == "" {
		switch status {
		case "dispatching":
			progressText = "已提交打包请求，等待 GitHub Actions 接收"
		case "queued", "waiting", "pending", "requested":
			progressText = "已进入 GitHub Actions 队列"
		case "in_progress":
			progressText = "正在打包，请稍候"
		case "cancel_requested":
			progressText = "正在停止打包"
		default:
			progressText = "正在处理打包任务"
		}
	}
	if progressStage == "" {
		progressStage = status
	}
	return progress, progressText, progressStage
}

func buildPendingMatchesRecordTime(record *BuildRecord, pending PendingBuild) bool {
	if record == nil {
		return false
	}
	createdAt, ok := parseDBTimestamp(record.CreatedAt)
	if !ok {
		return false
	}
	earliest := pending.TriggeredAt.Add(-20 * time.Minute)
	latest := pending.TriggeredAt.Add(20 * time.Minute)
	return !createdAt.Before(earliest) && !createdAt.After(latest)
}

func derivePendingBuildForRecord(record *BuildRecord) (PendingBuild, bool) {
	if record == nil {
		return PendingBuild{}, false
	}
	if pendingBuild, ok := getPendingBuildForClient(record.CodeID, record.Client, record.Profile, record.Tag, record.Branch, record.Core, record.Platforms); ok {
		if buildPendingMatchesRecordTime(record, pendingBuild) {
			return pendingBuild, true
		}
	}
	if createdAt, ok := parseDBTimestamp(record.CreatedAt); ok {
		return PendingBuild{
			CodeID:      record.CodeID,
			Client:      normalizeBuildClient(record.Client),
			Profile:     record.Profile,
			Tag:         record.Tag,
			Branch:      record.Branch,
			Core:        normalizeBuildCore(record.Core),
			Platforms:   record.Platforms,
			TriggeredAt: createdAt,
		}, true
	}
	return PendingBuild{}, false
}

func workflowRunMatchesRecord(record *BuildRecord, run *WorkflowRun, inputs map[string]string) bool {
	if record == nil {
		return false
	}
	if len(inputs) > 0 && buildRequestMatchesForClient(inputs, record.Client, record.Profile, buildRecordRequestID(record), record.Tag, record.Branch, record.Core, record.Platforms) {
		return true
	}
	// 绑定回调已经把 run_id 明确写进数据库后，后续查询不再强依赖 GitHub run 详情里必须带 inputs。
	if run != nil && record.RunID > 0 && run.ID == record.RunID {
		switch strings.TrimSpace(record.StatusSource) {
		case "callback", "webhook", "github":
			return true
		}
	}
	return false
}

func (h *Handlers) findWorkflowRunByRequestID(requestID string, activeOnly bool) (*WorkflowRun, map[string]string, error) {
	return h.findWorkflowRunByRequestIDForClient(buildClientLegacy, requestID, activeOnly)
}

func (h *Handlers) findWorkflowRunByRequestIDForClient(client, requestID string, activeOnly bool) (*WorkflowRun, map[string]string, error) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return nil, nil, nil
	}
	workflowFile := buildWorkflowFileForClient(client)

	searchCounts := []int{fastWorkflowRunsSearchLimit, recentWorkflowRunsSearchLimit}
	for index, count := range searchCounts {
		if index > 0 && searchCounts[index-1] >= count {
			continue
		}
		runs, err := h.gh.GetRecentWorkflowRuns(cfg.BuildOwner, cfg.BuildRepo, workflowFile, count)
		if err != nil {
			return nil, nil, err
		}

		for i := range runs {
			run := &runs[i]
			if activeOnly && !isActiveWorkflowStatus(run.Status) {
				continue
			}
			if extracted := extractBuildRequestIDFromText(run.Name); extracted == requestID {
				inputs := map[string]string{"request_id": requestID}
				return run, inputs, nil
			}
			if extractBuildRequestIDFromText(run.Name) != "" {
				continue
			}
			// 新工作流 run-name 已包含 request_id；没有标记的旧完成记录不再逐个翻详情，避免快速耗尽 GitHub API 额度。
			if index == 0 && isActiveWorkflowStatus(run.Status) {
				inputs, err := h.gh.GetWorkflowRunInputs(cfg.BuildOwner, cfg.BuildRepo, run.ID)
				if err != nil {
					continue
				}
				if strings.TrimSpace(inputs["request_id"]) == requestID {
					return run, inputs, nil
				}
			}
		}
	}

	return nil, nil, nil
}

func (h *Handlers) findActiveWorkflowRunForRecord(record *BuildRecord) (*WorkflowRun, error) {
	if record == nil {
		return nil, nil
	}

	if record.RunID > 0 {
		run, inputs, err := h.gh.GetWorkflowRun(cfg.BuildOwner, cfg.BuildRepo, record.RunID)
		if err == nil && run != nil && workflowRunMatchesRecord(record, run, inputs) && isActiveWorkflowStatus(run.Status) {
			return run, nil
		}
	}

	run, _, err := h.findWorkflowRunByRequestIDForClient(record.Client, buildRecordRequestID(record), true)
	if err != nil {
		return nil, err
	}
	return run, nil
}

func (h *Handlers) findStrictActiveWorkflowRunForRecord(record *BuildRecord) (*WorkflowRun, error) {
	return h.findActiveWorkflowRunForRecord(record)
}

func resolveBuildRecordWorkflowStatus(record *BuildRecord, run *WorkflowRun) (string, string) {
	if record == nil || run == nil {
		return "", ""
	}
	conclusion := ""
	if run.Conclusion != nil {
		conclusion = *run.Conclusion
	}
	status := run.Status
	if record.Status == "cancel_requested" && run.Status != "completed" {
		status = "cancel_requested"
		if conclusion == "" {
			conclusion = record.Conclusion
		}
	}
	return status, conclusion
}

func workflowRunConclusion(run *WorkflowRun) string {
	if run == nil || run.Conclusion == nil {
		return ""
	}
	return *run.Conclusion
}

func buildWorkflowJobStatusList(jobs []WorkflowJob) []map[string]interface{} {
	jobList := []map[string]interface{}{}
	for _, job := range jobs {
		jobConclusion := ""
		if job.Conclusion != nil {
			jobConclusion = *job.Conclusion
		}
		stepList := []map[string]interface{}{}
		for _, step := range job.Steps {
			stepConclusion := ""
			if step.Conclusion != nil {
				stepConclusion = *step.Conclusion
			}
			stepList = append(stepList, map[string]interface{}{
				"name":       step.Name,
				"status":     step.Status,
				"conclusion": stepConclusion,
				"number":     step.Number,
			})
		}
		jobList = append(jobList, map[string]interface{}{
			"name":       job.Name,
			"status":     job.Status,
			"conclusion": jobConclusion,
			"steps":      stepList,
		})
	}
	return jobList
}

func isWorkflowRunCompleted(run *WorkflowRun) bool {
	return run != nil && run.Status == "completed"
}

func isWorkflowRunCancelled(run *WorkflowRun) bool {
	return isWorkflowRunCompleted(run) && workflowRunConclusion(run) == "cancelled"
}

func (h *Handlers) waitForWorkflowRunTerminalState(record *BuildRecord, runID int64, timeout time.Duration) (*WorkflowRun, error) {
	if record == nil || runID <= 0 {
		return nil, nil
	}

	deadline := time.Now().Add(timeout)
	var lastRun *WorkflowRun
	for {
		run, inputs, err := h.gh.GetWorkflowRun(cfg.BuildOwner, cfg.BuildRepo, runID)
		if err == nil && run != nil && workflowRunMatchesRecord(record, run, inputs) {
			lastRun = run
			if isWorkflowRunCompleted(run) {
				return run, nil
			}
		}

		if time.Now().After(deadline) {
			return lastRun, nil
		}
		time.Sleep(800 * time.Millisecond)
	}
}

func (h *Handlers) applyWorkflowRunToBuildRecord(record *BuildRecord, run *WorkflowRun) {
	if record == nil || run == nil {
		return
	}
	status, conclusion := resolveBuildRecordWorkflowStatus(record, run)
	if status == "" {
		return
	}
	runURL := run.HTMLURL
	if runURL == "" {
		runURL = buildWorkflowRunURL(run.ID)
	}
	releaseTag := buildReleaseTag(record)
	progress, progressText, progressStage := buildProgressForEvent(status, conclusion, -1, "", "")
	if err := updateBuildRecordStatusProgressExt(record.ID, run.ID, status, conclusion, "github", runURL, releaseTag, progress, progressText, progressStage); err != nil {
		return
	}
	if err := consumeBuildUsageForCompletedRecord(record.ID); err != nil {
		log.Printf("记录打包成功次数失败: record_id=%d code_id=%d err=%v", record.ID, record.CodeID, err)
	}
	if updatedRecord, err := getBuildRecord(record.ID); err == nil && updatedRecord != nil {
		*record = *updatedRecord
		return
	}
	record.RunID = run.ID
	if runURL != "" {
		record.RunURL = runURL
	}
	if releaseTag != "" {
		record.ReleaseTag = releaseTag
	}
	record.Status = status
	record.Conclusion = conclusion
	record.StatusSource = "github"
	record.Progress = progress
	record.ProgressText = progressText
	record.ProgressStage = progressStage
	if run.CreatedAt != "" {
		record.CreatedAt = run.CreatedAt
	}
	if run.UpdatedAt != "" {
		record.UpdatedAt = run.UpdatedAt
	}
	if run.Status == "completed" {
		clearPendingBuildForClient(record.CodeID, record.Client, record.Profile, record.Tag, record.Branch, record.Core, record.Platforms)
		deleteCachedProfileRunID(buildRunCacheKeyForClient(record.CodeID, record.Client, record.Profile, buildRecordRequestID(record), record.Tag, record.Branch, record.Core, record.Platforms))
	} else {
		setCachedProfileRunID(buildRunCacheKeyForClient(record.CodeID, record.Client, record.Profile, buildRecordRequestID(record), record.Tag, record.Branch, record.Core, record.Platforms), run.ID)
	}
	invalidateBuildCachesForRecord(record)
}

func (h *Handlers) reconcileBuildRecords(records []BuildRecord, deep bool) []BuildRecord {
	if len(records) == 0 {
		return records
	}

	type unresolvedRecord struct {
		index      int
		pending    PendingBuild
		hasPending bool
		requestID  string
	}

	unresolved := make([]unresolvedRecord, 0)
	for i := range records {
		record := &records[i]
		if record.Status == "completed" {
			continue
		}
		if record.RunID > 0 {
			run, inputs, err := h.gh.GetWorkflowRun(cfg.BuildOwner, cfg.BuildRepo, record.RunID)
			if err == nil && run != nil && workflowRunMatchesRecord(record, run, inputs) {
				h.applyWorkflowRunToBuildRecord(record, run)
				if record.Status == "completed" || record.RunID > 0 {
					continue
				}
			}
		}

		requestID := buildRecordRequestID(record)
		pendingBuild, hasPendingBuild := derivePendingBuildForRecord(record)

		unresolved = append(unresolved, unresolvedRecord{
			index:      i,
			pending:    pendingBuild,
			hasPending: hasPendingBuild,
			requestID:  requestID,
		})
	}

	if len(unresolved) == 0 {
		return records
	}
	if !deep {
		return records
	}

	groupedUnresolved := map[string][]unresolvedRecord{}
	for _, item := range unresolved {
		record := records[item.index]
		client := normalizeBuildClient(record.Client)
		groupedUnresolved[client] = append(groupedUnresolved[client], item)
	}
	resolved := make(map[int]struct{})
	for client, items := range groupedUnresolved {
		runs, err := h.gh.GetRecentWorkflowRuns(cfg.BuildOwner, cfg.BuildRepo, buildWorkflowFileForClient(client), recentWorkflowRunsSearchLimit)
		if err != nil {
			continue
		}

		for i := range runs {
			run := &runs[i]
			extractedRequestID := extractBuildRequestIDFromText(run.Name)
			var inputs map[string]string
			var inputsErr error
			if extractedRequestID == "" {
				shouldReadInputs := false
				for _, item := range items {
					if _, ok := resolved[item.index]; ok {
						continue
					}
					if run.Status != "completed" && item.hasPending && matchRunByPendingBuild(run, item.pending) {
						shouldReadInputs = true
						break
					}
				}
				if shouldReadInputs {
					inputs, inputsErr = h.gh.GetWorkflowRunInputs(cfg.BuildOwner, cfg.BuildRepo, run.ID)
				} else {
					inputsErr = fmt.Errorf("跳过无 request_id 的非候选运行")
				}
			} else {
				inputs = map[string]string{"request_id": extractedRequestID}
			}
			for _, item := range items {
				if _, ok := resolved[item.index]; ok {
					continue
				}
				record := &records[item.index]
				matchesByInputs := false
				if item.requestID != "" && extractedRequestID == item.requestID {
					matchesByInputs = true
				} else {
					matchesByInputs = inputsErr == nil && buildRequestMatchesForClient(inputs, record.Client, record.Profile, item.requestID, record.Tag, record.Branch, record.Core, record.Platforms)
				}
				matchesByPending := item.requestID == "" && run.Status != "completed" && item.hasPending && matchRunByPendingBuild(run, item.pending)
				if !matchesByInputs && !matchesByPending {
					continue
				}
				h.applyWorkflowRunToBuildRecord(record, run)
				resolved[item.index] = struct{}{}
				break
			}
			if len(resolved) == len(unresolved) {
				break
			}
		}
	}

	return records
}

func findReleaseAssetByID(release *Release, assetID int64) *ReleaseAsset {
	if release == nil {
		return nil
	}
	for i := range release.Assets {
		if release.Assets[i].ID == assetID {
			return &release.Assets[i]
		}
	}
	return nil
}

func (h *Handlers) resolveBuildRecordAsset(record *BuildRecord, assetID int64) (*ReleaseAsset, error) {
	if record == nil {
		return nil, fmt.Errorf("打包记录不存在")
	}

	releaseTag := buildReleaseTag(record)
	if releaseTag == "" {
		return nil, fmt.Errorf("当前打包尚未生成可下载产物")
	}

	release, err := h.gh.GetReleaseByTag(cfg.BuildOwner, cfg.BuildRepo, releaseTag)
	if err != nil {
		return nil, fmt.Errorf("获取打包产物失败")
	}
	if release == nil {
		return nil, fmt.Errorf("当前打包尚未生成可下载产物")
	}

	matchedAsset := findReleaseAssetByID(release, assetID)
	if matchedAsset == nil {
		return nil, fmt.Errorf("打包产物不存在")
	}

	return matchedAsset, nil
}

func writeReleaseAssetToResponse(w http.ResponseWriter, asset *ReleaseAsset, resp *http.Response) {
	contentType := asset.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", asset.Name))
	w.Header().Set("Cache-Control", "private, no-store, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if asset.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(asset.Size, 10))
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, resp.Body)
}

func (h *Handlers) deleteBuildRecordRelease(record *BuildRecord) error {
	releaseTag := buildReleaseTag(record)
	if releaseTag == "" {
		return nil
	}
	return h.gh.DeleteReleaseByTag(cfg.BuildOwner, cfg.BuildRepo, releaseTag)
}

func (h *Handlers) cleanupOverflowBuildRecords(codeID int, client string) {
	records, err := listOverflowBuildRecordsByClient(codeID, normalizeBuildClient(client), maxBuildRecordHistory)
	if err != nil {
		log.Printf("清理旧打包记录失败: code_id=%d client=%s err=%v", codeID, client, err)
		return
	}

	for _, record := range records {
		if isActiveWorkflowStatus(record.Status) {
			continue
		}
		if err := h.deleteBuildRecordRelease(&record); err != nil {
			log.Printf("删除旧打包记录对应 Release 失败: record_id=%d err=%v", record.ID, err)
			continue
		}
		if err := deleteBuildRecord(record.ID); err != nil {
			log.Printf("删除旧打包记录失败: record_id=%d err=%v", record.ID, err)
		}
	}
}

func (h *Handlers) TriggerBuild(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Client    string `json:"client"`
		Profile   string `json:"profile"`
		Tag       string `json:"tag"`
		Core      string `json:"core"`
		Platforms string `json:"platforms"`
		Branch    string `json:"branch"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "请求格式错误", 400)
		return
	}

	if req.Profile == "" || req.Tag == "" {
		jsonError(w, "请填写配置档案和版本标签", 400)
		return
	}
	clientConfig, err := buildClientConfig(req.Client)
	if err != nil {
		jsonError(w, err.Error(), 400)
		return
	}
	req.Client = clientConfig.ID

	claims := getClaims(r)
	if !claims.canAccessProfile(req.Profile) {
		jsonError(w, "无权操作该档案", 403)
		return
	}
	if claims.Permissions != "admin" {
		if ok, message := canAccessClientForCodeID(claims.CodeID, req.Client); !ok {
			jsonError(w, message, 403)
			return
		}
	}

	if req.Platforms == "" {
		req.Platforms = "all"
	}
	core, err := validateBuildCoreForClient(req.Client, req.Core)
	if err != nil {
		jsonError(w, err.Error(), 400)
		return
	}
	req.Core = core
	if req.Branch == "" {
		req.Branch = "main"
	}
	profileBranch := h.profileBranchForClient(req.Client)
	if strings.EqualFold(strings.TrimSpace(req.Branch), strings.TrimSpace(profileBranch)) {
		jsonError(w, "配置档案分支不可作为打包源码分支", 400)
		return
	}

	if _, err := expandRequestedBuildPlatformsForClient(req.Client, req.Core, req.Platforms); err != nil {
		jsonError(w, err.Error(), 400)
		return
	}
	demand := estimateBuildJobDemandForClient(req.Client, req.Core, req.Platforms)
	if demand.Total <= 0 {
		jsonError(w, "无效的打包平台选择", 400)
		return
	}

	if claims.Permissions != "admin" {
		ac, err := getActivationCodeByID(claims.CodeID)
		if err != nil {
			jsonError(w, err.Error(), 403)
			return
		}
		if ok, message := getBuildSubmissionAvailability(ac); !ok {
			jsonError(w, message, 400)
			return
		}
		if ok, message := canUseBuildPlatformsForClient(req.Client, ac.AllowedPlatforms, req.Core, req.Platforms); !ok {
			jsonError(w, message, 403)
			return
		}
	}

	queueSnapshot, err := h.getBuildQueueSnapshotForClient(req.Client, req.Core, req.Platforms)
	if err == nil && !queueSnapshot.Available {
		jsonError(w, queueSnapshot.Message, 429)
		return
	}

	if err := h.validateProfileYamlForClient(req.Client, req.Profile); err != nil {
		jsonError(w, "配置档案无效: "+err.Error(), 400)
		return
	}

	record, err := createBuildRecord(claims.CodeID, claims.CodeName, req.Client, req.Profile, req.Tag, req.Branch, req.Core, req.Platforms)
	if err != nil {
		jsonError(w, "创建打包记录失败", 500)
		return
	}
	if claims.Permissions != "admin" {
		if err := ensureBuildRecordSubmissionSlot(record); err != nil {
			_ = deleteBuildRecord(record.ID)
			jsonError(w, err.Error(), 400)
			return
		}
	}

	requestID := buildRecordRequestID(record)
	inputs := map[string]string{
		"client":         req.Client,
		"profile":        req.Profile,
		"profile_branch": profileBranch,
		"tag":            req.Tag,
		"platforms":      req.Platforms,
		"branch":         req.Branch,
		"core":           req.Core,
		"request_id":     requestID,
	}

	err = h.gh.TriggerWorkflow(cfg.BuildOwner, cfg.BuildRepo, clientConfig.WorkflowFile, inputs)
	if err != nil {
		progressText := "触发打包失败：" + err.Error()
		_ = updateBuildRecordStatusProgressExt(record.ID, 0, "completed", "trigger_failed", "server", "", "", 100, progressText, "trigger_failed")
		jsonError(w, err.Error(), 500)
		return
	}

	rememberPendingBuildForClient(claims.CodeID, req.Client, req.Profile, req.Tag, req.Branch, req.Core, req.Platforms)

	deleteCachedProfileRunID(buildRunCacheKeyForClient(claims.CodeID, req.Client, req.Profile, requestID, req.Tag, req.Branch, req.Core, req.Platforms))
	deleteCachedProfileRunID(buildRunCacheKeyForClient(claims.CodeID, req.Client, req.Profile, "", "", "", req.Core, ""))
	invalidateBuildCachesForRecord(record)

	logAudit(claims.CodeID, claims.CodeName, "trigger_build",
		fmt.Sprintf("record_id=%d client=%s profile=%s tag=%s core=%s platforms=%s branch=%s", record.ID, req.Client, req.Profile, req.Tag, req.Core, req.Platforms, req.Branch),
		r.RemoteAddr)

	go h.cleanupOverflowBuildRecords(record.CodeID, record.Client)

	jsonResponse(w, map[string]interface{}{
		"message":    "打包已提交",
		"record_id":  record.ID,
		"request_id": requestID,
	})
}

func secureCompareString(expected, actual string) bool {
	expected = strings.TrimSpace(expected)
	actual = strings.TrimSpace(actual)
	if expected == "" || actual == "" {
		return false
	}
	return hmac.Equal([]byte(expected), []byte(actual))
}

func authorizeBuildEventRequest(r *http.Request) bool {
	return secureCompareString(cfg.BuildEventToken, r.Header.Get("X-Build-Event-Token"))
}

func verifyGitHubWebhookSignature(secret, signature string, body []byte) bool {
	secret = strings.TrimSpace(secret)
	signature = strings.TrimSpace(signature)
	if secret == "" || signature == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	expected := fmt.Sprintf("sha256=%x", mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}

func buildReleaseTagForRun(record *BuildRecord, runID int64, releaseTag string) string {
	releaseTag = strings.TrimSpace(releaseTag)
	if releaseTag != "" {
		return releaseTag
	}
	if record == nil {
		return ""
	}
	if strings.TrimSpace(record.ReleaseTag) != "" {
		return strings.TrimSpace(record.ReleaseTag)
	}
	if runID > 0 {
		return fmt.Sprintf("%s-%s-%d", record.Profile, record.Tag, runID)
	}
	return ""
}

func (h *Handlers) persistBuildRecordEvent(record *BuildRecord, runID int64, status, conclusion, statusSource, runURL, releaseTag string) (*BuildRecord, error) {
	return h.persistBuildRecordProgressEvent(record, runID, status, conclusion, statusSource, runURL, releaseTag, -1, "", "")
}

func (h *Handlers) persistBuildRecordProgressEvent(record *BuildRecord, runID int64, status, conclusion, statusSource, runURL, releaseTag string, progress int, progressText, progressStage string) (*BuildRecord, error) {
	if record == nil {
		return nil, fmt.Errorf("打包记录不存在")
	}

	status = strings.TrimSpace(status)
	if status == "" {
		status = record.Status
	}
	if record.Status == "completed" && status != "completed" {
		status = record.Status
	}
	conclusion = strings.TrimSpace(conclusion)
	if conclusion == "" {
		conclusion = record.Conclusion
	}
	runURL = strings.TrimSpace(runURL)
	if runURL == "" && runID > 0 {
		runURL = buildWorkflowRunURL(runID)
	}
	releaseTag = buildReleaseTagForRun(record, runID, releaseTag)
	progress, progressText, progressStage = buildProgressForEvent(status, conclusion, progress, progressText, progressStage)

	if err := updateBuildRecordStatusProgressExt(record.ID, runID, status, conclusion, statusSource, runURL, releaseTag, progress, progressText, progressStage); err != nil {
		return nil, err
	}
	if err := consumeBuildUsageForCompletedRecord(record.ID); err != nil {
		return nil, err
	}

	cacheKey := buildRunCacheKeyForClient(record.CodeID, record.Client, record.Profile, buildRecordRequestID(record), record.Tag, record.Branch, record.Core, record.Platforms)
	if status == "completed" {
		clearPendingBuildForClient(record.CodeID, record.Client, record.Profile, record.Tag, record.Branch, record.Core, record.Platforms)
		deleteCachedProfileRunID(cacheKey)
	} else if runID > 0 {
		setCachedProfileRunID(cacheKey, runID)
	}
	invalidateBuildCachesForRecord(record)

	updatedRecord, err := getBuildRecord(record.ID)
	if err != nil {
		return record, nil
	}
	return updatedRecord, nil
}

func (h *Handlers) InternalBindBuildRun(w http.ResponseWriter, r *http.Request) {
	if !authorizeBuildEventRequest(r) {
		jsonError(w, "未授权的内部回调请求", 401)
		return
	}

	var req struct {
		RequestID     string `json:"request_id"`
		RunID         int64  `json:"run_id"`
		RunURL        string `json:"run_url"`
		Status        string `json:"status"`
		Conclusion    string `json:"conclusion"`
		ReleaseTag    string `json:"release_tag"`
		Progress      int    `json:"progress_percent"`
		ProgressText  string `json:"progress_text"`
		ProgressStage string `json:"progress_stage"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "请求格式错误", 400)
		return
	}

	req.RequestID = strings.TrimSpace(req.RequestID)
	if req.RequestID == "" || req.RunID <= 0 {
		jsonError(w, "缺少 request_id 或 run_id", 400)
		return
	}

	record, err := getBuildRecordByRequestID(req.RequestID)
	if err != nil {
		jsonError(w, "打包记录不存在", 404)
		return
	}

	status := strings.TrimSpace(req.Status)
	if status == "" || status == "dispatching" {
		status = "in_progress"
	}
	record, err = h.persistBuildRecordProgressEvent(record, req.RunID, status, req.Conclusion, "callback", req.RunURL, req.ReleaseTag, req.Progress, req.ProgressText, req.ProgressStage)
	if err != nil {
		jsonError(w, "绑定打包运行失败", 500)
		return
	}

	jsonResponse(w, map[string]interface{}{
		"message": "绑定成功",
		"record":  buildRecordResponse(*record),
	})
}

func (h *Handlers) InternalCompleteBuildRun(w http.ResponseWriter, r *http.Request) {
	if !authorizeBuildEventRequest(r) {
		jsonError(w, "未授权的内部回调请求", 401)
		return
	}

	var req struct {
		RequestID     string `json:"request_id"`
		RunID         int64  `json:"run_id"`
		RunURL        string `json:"run_url"`
		Status        string `json:"status"`
		Conclusion    string `json:"conclusion"`
		ReleaseTag    string `json:"release_tag"`
		Progress      int    `json:"progress_percent"`
		ProgressText  string `json:"progress_text"`
		ProgressStage string `json:"progress_stage"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "请求格式错误", 400)
		return
	}

	req.RequestID = strings.TrimSpace(req.RequestID)
	if req.RequestID == "" {
		jsonError(w, "缺少 request_id", 400)
		return
	}

	record, err := getBuildRecordByRequestID(req.RequestID)
	if err != nil {
		jsonError(w, "打包记录不存在", 404)
		return
	}

	if req.RunID <= 0 {
		req.RunID = record.RunID
	}
	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = "completed"
	}
	record, err = h.persistBuildRecordProgressEvent(record, req.RunID, status, req.Conclusion, "callback", req.RunURL, req.ReleaseTag, req.Progress, req.ProgressText, req.ProgressStage)
	if err != nil {
		jsonError(w, "更新打包完成状态失败", 500)
		return
	}

	jsonResponse(w, map[string]interface{}{
		"message": "回写成功",
		"record":  buildRecordResponse(*record),
	})
}

func (h *Handlers) InternalUpdateBuildProgress(w http.ResponseWriter, r *http.Request) {
	if !authorizeBuildEventRequest(r) {
		jsonError(w, "未授权的内部回调请求", 401)
		return
	}

	var req struct {
		RequestID     string `json:"request_id"`
		RunID         int64  `json:"run_id"`
		RunURL        string `json:"run_url"`
		Status        string `json:"status"`
		Conclusion    string `json:"conclusion"`
		ReleaseTag    string `json:"release_tag"`
		Progress      int    `json:"progress_percent"`
		ProgressText  string `json:"progress_text"`
		ProgressStage string `json:"progress_stage"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "请求格式错误", 400)
		return
	}

	req.RequestID = strings.TrimSpace(req.RequestID)
	if req.RequestID == "" {
		jsonError(w, "缺少 request_id", 400)
		return
	}

	record, err := getBuildRecordByRequestID(req.RequestID)
	if err != nil {
		jsonError(w, "打包记录不存在", 404)
		return
	}
	if req.RunID <= 0 {
		req.RunID = record.RunID
	}
	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = "in_progress"
	}
	record, err = h.persistBuildRecordProgressEvent(record, req.RunID, status, req.Conclusion, "callback", req.RunURL, req.ReleaseTag, req.Progress, req.ProgressText, req.ProgressStage)
	if err != nil {
		jsonError(w, "更新打包进度失败", 500)
		return
	}

	jsonResponse(w, map[string]interface{}{
		"message": "进度已更新",
		"record":  buildRecordResponse(*record),
	})
}

func (h *Handlers) HandleGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(cfg.GitHubWebhookSecret) == "" {
		jsonError(w, "未启用 GitHub webhook", 503)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		jsonError(w, "读取 webhook 请求失败", 400)
		return
	}
	if !verifyGitHubWebhookSignature(cfg.GitHubWebhookSecret, r.Header.Get("X-Hub-Signature-256"), body) {
		jsonError(w, "webhook 签名校验失败", 401)
		return
	}
	if strings.TrimSpace(r.Header.Get("X-GitHub-Event")) != "workflow_run" {
		jsonResponse(w, map[string]interface{}{"message": "已忽略非 workflow_run 事件"})
		return
	}

	var payload struct {
		Action     string `json:"action"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
		Workflow struct {
			Path string `json:"path"`
		} `json:"workflow"`
		WorkflowRun struct {
			ID         int64   `json:"id"`
			Event      string  `json:"event"`
			Status     string  `json:"status"`
			Conclusion *string `json:"conclusion"`
			HTMLURL    string  `json:"html_url"`
			Path       string  `json:"path"`
		} `json:"workflow_run"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		jsonError(w, "webhook 请求体解析失败", 400)
		return
	}

	if payload.Repository.FullName != "" && payload.Repository.FullName != fmt.Sprintf("%s/%s", cfg.BuildOwner, cfg.BuildRepo) {
		jsonResponse(w, map[string]interface{}{"message": "已忽略其他仓库事件"})
		return
	}
	if payload.WorkflowRun.Event != "" && payload.WorkflowRun.Event != "workflow_dispatch" {
		jsonResponse(w, map[string]interface{}{"message": "已忽略非手动触发构建事件"})
		return
	}
	workflowPath := strings.TrimSpace(payload.Workflow.Path)
	if workflowPath == "" {
		workflowPath = strings.TrimSpace(payload.WorkflowRun.Path)
	}
	if workflowPath != "" &&
		!strings.HasSuffix(workflowPath, buildWorkflowFileForClient(buildClientLegacy)) &&
		!strings.HasSuffix(workflowPath, buildWorkflowFileForClient(buildClientNexGenReact)) {
		jsonResponse(w, map[string]interface{}{"message": "已忽略非客户端打包工作流"})
		return
	}
	if payload.WorkflowRun.ID <= 0 {
		jsonResponse(w, map[string]interface{}{"message": "已忽略缺少 run_id 的事件"})
		return
	}

	run, inputs, err := h.gh.GetWorkflowRun(cfg.BuildOwner, cfg.BuildRepo, payload.WorkflowRun.ID)
	if err != nil || run == nil {
		jsonResponse(w, map[string]interface{}{"message": "获取 workflow run 详情失败，已忽略"})
		return
	}
	requestID := strings.TrimSpace(inputs["request_id"])
	if requestID == "" {
		if fallbackInputs, err := h.gh.GetWorkflowRunInputs(cfg.BuildOwner, cfg.BuildRepo, payload.WorkflowRun.ID); err == nil {
			inputs = fallbackInputs
			requestID = strings.TrimSpace(fallbackInputs["request_id"])
		}
	}
	if requestID == "" {
		requestID = extractBuildRequestIDFromText(run.Name)
	}
	if requestID == "" {
		requestID = extractBuildRequestIDFromText(payload.WorkflowRun.HTMLURL)
	}
	if requestID == "" {
		jsonResponse(w, map[string]interface{}{"message": "未找到 request_id，已忽略"})
		return
	}

	record, err := getBuildRecordByRequestID(requestID)
	if err != nil {
		jsonResponse(w, map[string]interface{}{"message": "未匹配到本地打包记录，已忽略", "request_id": requestID})
		return
	}

	status := strings.TrimSpace(run.Status)
	if status == "" {
		status = strings.TrimSpace(payload.WorkflowRun.Status)
	}
	conclusion := ""
	if run.Conclusion != nil {
		conclusion = *run.Conclusion
	} else if payload.WorkflowRun.Conclusion != nil {
		conclusion = *payload.WorkflowRun.Conclusion
	}
	runURL := strings.TrimSpace(run.HTMLURL)
	if runURL == "" {
		runURL = strings.TrimSpace(payload.WorkflowRun.HTMLURL)
	}

	record, err = h.persistBuildRecordEvent(record, run.ID, status, conclusion, "webhook", runURL, "")
	if err != nil {
		jsonError(w, "同步 webhook 状态失败", 500)
		return
	}

	jsonResponse(w, map[string]interface{}{
		"message":    "同步成功",
		"request_id": requestID,
		"record":     buildRecordResponse(*record),
	})
}

func (h *Handlers) validateProfileYaml(profileName string) error {
	return h.validateProfileYamlForClient(buildClientLegacy, profileName)
}

func (h *Handlers) validateProfileYamlForClient(client, profileName string) error {
	client = normalizeBuildClient(client)
	content, _, _, exists, err := h.getStoredProfileForClient(client, profileName)
	if err != nil {
		return fmt.Errorf("读取档案失败")
	}
	if !exists {
		return fmt.Errorf("未找到档案")
	}
	if client == buildClientNexGenReact {
		cleaned := normalizeNexGenProfileConfig(content)
		if cleaned != content {
			filePath, err := profileFilePath(profileName)
			if err != nil {
				return fmt.Errorf("修复档案失败")
			}
			_ = h.profileGitHubClient(client).SaveFileWithRetry(filePath, func(_ string) string {
				return cleaned
			}, "修复配置档案: "+profileName, 3)
			invalidateProfileCacheForClient(client)
		}
		return validateYamlContent(cleaned)
	}
	cleaned := normalizeSubscriptionConfig(content)
	if cleaned != content {
		filePath, err := profileFilePath(profileName)
		if err != nil {
			return fmt.Errorf("修复档案失败")
		}
		_ = h.profileGitHubClient(client).SaveFileWithRetry(filePath, func(_ string) string {
			return cleaned
		}, "修复配置档案: "+profileName, 3)
		invalidateProfileCacheForClient(client)
	}
	return validateYamlContent(cleaned)
}

func (h *Handlers) GetBuildHistory(w http.ResponseWriter, r *http.Request) {
	claims := getClaims(r)
	client := normalizeBuildClient(r.URL.Query().Get("client"))
	if ok, message := canAccessClientForClaims(claims, client); !ok {
		jsonError(w, message, 403)
		return
	}
	records, err := listBuildRecordsByClient(claims.CodeID, claims.Permissions == "admin", client, 100)
	if err != nil {
		jsonResponse(w, map[string]interface{}{})
		return
	}

	history := make(map[string]interface{})
	for _, record := range records {
		if normalizeBuildClient(record.Client) != client {
			continue
		}
		if _, exists := history[record.Profile]; exists {
			continue
		}
		history[record.Profile] = map[string]interface{}{
			"version":   record.Tag,
			"core":      normalizeBuildCore(record.Core),
			"platforms": record.Platforms,
			"time":      record.CreatedAt,
		}
	}
	jsonResponse(w, history)
}

func (h *Handlers) GetClientUpdates(w http.ResponseWriter, r *http.Request) {
	limit := getClientUpdatesLimit()
	claims := getClaims(r)
	clientConfig, err := buildClientConfig(r.URL.Query().Get("client"))
	if err != nil {
		jsonError(w, err.Error(), 400)
		return
	}
	if ok, message := canAccessClientForClaims(claims, clientConfig.ID); !ok {
		jsonError(w, message, 403)
		return
	}
	core, err := validateBuildCoreForClient(clientConfig.ID, r.URL.Query().Get("core"))
	if err != nil {
		jsonError(w, err.Error(), 400)
		return
	}
	branch := strings.TrimSpace(r.URL.Query().Get("branch"))
	if branch == "" && core == "mihomo" {
		branch = cfg.GithubBranch
	}

	owner, repo := resolveBuildSourceRepoForClient(clientConfig.ID, core)
	commits, err := h.gh.ListRecentCommitsForRepo(owner, repo, branch, limit)
	if err != nil {
		jsonError(w, "获取更新记录失败: "+err.Error(), 500)
		return
	}

	jsonResponse(w, map[string]interface{}{
		"client":  clientConfig.ID,
		"core":    core,
		"repo":    repo,
		"branch":  branch,
		"limit":   limit,
		"commits": commits,
	})
}

func (h *Handlers) ListBranches(w http.ResponseWriter, r *http.Request) {
	claims := getClaims(r)
	clientConfig, err := buildClientConfig(r.URL.Query().Get("client"))
	if err != nil {
		jsonError(w, err.Error(), 400)
		return
	}
	if ok, message := canAccessClientForClaims(claims, clientConfig.ID); !ok {
		jsonError(w, message, 403)
		return
	}
	core, err := validateBuildCoreForClient(clientConfig.ID, r.URL.Query().Get("core"))
	if err != nil {
		jsonError(w, err.Error(), 400)
		return
	}
	owner, repo := resolveBuildSourceRepoForClient(clientConfig.ID, core)
	branches, err := h.gh.ListBranchesForRepo(owner, repo)
	if err != nil {
		jsonError(w, "获取分支列表失败", 500)
		return
	}
	jsonResponse(w, branches)
}

func (h *Handlers) GetBuildStatus(w http.ResponseWriter, r *http.Request) {
	claims := getClaims(r)
	client := normalizeBuildClient(r.URL.Query().Get("client"))
	profile := strings.TrimSpace(r.URL.Query().Get("profile"))
	requestID := strings.TrimSpace(r.URL.Query().Get("request_id"))
	tag := strings.TrimSpace(r.URL.Query().Get("tag"))
	branch := strings.TrimSpace(r.URL.Query().Get("branch"))
	core := strings.TrimSpace(r.URL.Query().Get("core"))
	platforms := strings.TrimSpace(r.URL.Query().Get("platforms"))

	recordID, _ := strconv.ParseInt(r.URL.Query().Get("record_id"), 10, 64)

	var record *BuildRecord
	if requestID != "" {
		if loadedRecord, err := getBuildRecordByRequestID(requestID); err == nil {
			record = loadedRecord
		}
	}
	if recordID > 0 {
		if loadedRecord, err := getBuildRecord(recordID); err == nil {
			record = loadedRecord
		}
	}

	statusCacheKey := ""
	if record != nil {
		if !canAccessBuildRecord(claims, record) {
			jsonError(w, "无权访问该打包记录", 403)
			return
		}
		recordID = record.ID
		requestID = buildRecordRequestID(record)
		client = normalizeBuildClient(record.Client)
		if profile == "" {
			profile = record.Profile
		}
		if tag == "" {
			tag = record.Tag
		}
		if branch == "" {
			branch = record.Branch
		}
		if core == "" {
			core = record.Core
		}
		if platforms == "" {
			platforms = record.Platforms
		}
	}
	if record == nil {
		if ok, message := canAccessClientForClaims(claims, client); !ok {
			jsonError(w, message, 403)
			return
		}
	}
	if core == "" {
		core = "mihomo"
	}
	core, err := validateBuildCoreForClient(client, core)
	if err != nil {
		jsonError(w, err.Error(), 400)
		return
	}

	if record == nil {
		if recordID > 0 || requestID != "" {
			result := map[string]interface{}{
				"found": false,
			}
			if recordID > 0 {
				result["record_id"] = recordID
			}
			if requestID != "" {
				result["request_id"] = requestID
			}
			writeBuildStatusResponse(w, buildStatusCacheKey(record, 0, client, profile, requestID, tag, branch, core, platforms), result)
			return
		}
		if claims.Permissions != "admin" {
			jsonError(w, "普通用户查询打包状态必须提供自己的 record_id 或 request_id", 400)
			return
		}
	}
	if record == nil && profile != "" && !claims.canAccessProfile(profile) {
		jsonError(w, "无权查看该档案的打包状态", 403)
		return
	}

	if profile == "" && requestID == "" && recordID == 0 {
		jsonError(w, "缺少 profile、request_id 或 record_id 参数", 400)
		return
	}

	pendingCodeID := 0
	if record != nil {
		pendingCodeID = record.CodeID
	} else if claims.Permissions != "admin" {
		pendingCodeID = claims.CodeID
	}
	pendingBuild := PendingBuild{}
	hasPendingBuild := false
	if record != nil {
		pendingBuild, hasPendingBuild = derivePendingBuildForRecord(record)
	} else {
		pendingBuild, hasPendingBuild = getPendingBuildForClient(pendingCodeID, client, profile, tag, branch, core, platforms)
	}
	if !hasPendingBuild && profile != "" && claims.Permissions == "admin" {
		if inferredPending, ok := getLatestPendingBuildByClientProfile(pendingCodeID, client, profile); ok {
			pendingBuild = inferredPending
			hasPendingBuild = true
			if tag == "" {
				tag = inferredPending.Tag
			}
			if branch == "" {
				branch = inferredPending.Branch
			}
			if core == "" {
				core = inferredPending.Core
			}
			if client == "" {
				client = inferredPending.Client
			}
			if platforms == "" {
				platforms = inferredPending.Platforms
			}
		}
	}
	cacheKey := buildRunCacheKeyForClient(pendingCodeID, client, profile, requestID, tag, branch, core, platforms)
	statusCacheKey = buildStatusCacheKey(record, pendingCodeID, client, profile, requestID, tag, branch, core, platforms)
	syncRequested := isBuildStatusSyncRequested(r)
	includeJobs := shouldIncludeBuildJobs(r)
	responseCacheKey := statusCacheKey
	if syncRequested || includeJobs {
		responseCacheKey = ""
	}
	if !syncRequested && !includeJobs {
		if cached, ok := buildStatusCache.get(statusCacheKey); ok {
			jsonResponse(w, cloneInterfaceMap(cached))
			return
		}
	}

	syncWithGitHub := shouldSyncBuildStatusWithGitHub(r, record)
	if record != nil && !syncWithGitHub {
		result := buildStatusResponseFromRecord(record)
		if hasPendingBuild {
			result["pending_detected"] = true
		}
		applyBuildStatusSyncHints(result, record, syncRequested, syncWithGitHub)
		writeBuildStatusResponse(w, responseCacheKey, result)
		return
	}

	var matchedRun *WorkflowRun
	var matchedInputs map[string]string

	if record == nil && !syncRequested {
		result := map[string]interface{}{
			"found": false,
		}
		if recordID > 0 {
			result["record_id"] = recordID
		}
		if requestID != "" {
			result["request_id"] = requestID
		}
		if hasPendingBuild {
			result["pending_detected"] = true
		}
		result["sync_required"] = true
		result["progress"] = 5
		result["progress_percent"] = 5
		result["progress_text"] = "已提交打包请求，等待 GitHub Actions 回传运行信息"
		result["progress_stage"] = "dispatching"
		writeBuildStatusResponse(w, responseCacheKey, result)
		return
	}

	if record != nil {
		if record.RunID > 0 {
			run, inputs, err := h.gh.GetWorkflowRun(cfg.BuildOwner, cfg.BuildRepo, record.RunID)
			if err == nil && run != nil && workflowRunMatchesRecord(record, run, inputs) {
				matchedRun = run
				matchedInputs = inputs
			}
		}
		if matchedRun == nil {
			run, inputs, err := h.findWorkflowRunByRequestIDForClient(record.Client, requestID, false)
			if err != nil {
				result := buildStatusResponseFromRecord(record)
				result["sync_error"] = true
				applyBuildStatusSyncHints(result, record, syncRequested, syncWithGitHub)
				writeBuildStatusResponse(w, responseCacheKey, result)
				return
			}
			if run != nil {
				matchedRun = run
				matchedInputs = inputs
			}
		}
		if matchedRun == nil {
			result := buildStatusResponseFromRecord(record)
			if hasPendingBuild {
				result["pending_detected"] = true
			}
			applyBuildStatusSyncHints(result, record, syncRequested, syncWithGitHub)
			writeBuildStatusResponse(w, responseCacheKey, result)
			return
		}

		h.applyWorkflowRunToBuildRecord(record, matchedRun)
		result := buildStatusResponseFromRecord(record)
		applyBuildStatusSyncHints(result, record, syncRequested, syncWithGitHub)
		if matchedInputs != nil && len(matchedInputs) > 0 {
			result["inputs"] = matchedInputs
			if strings.TrimSpace(matchedInputs["request_id"]) != "" {
				result["request_id"] = strings.TrimSpace(matchedInputs["request_id"])
			}
			if strings.TrimSpace(matchedInputs["core"]) == "" {
				matchedInputs["core"] = core
			}
		}
		if hasPendingBuild {
			result["pending_detected"] = true
		}

		if includeJobs {
			jobs, err := h.gh.GetWorkflowRunJobs(cfg.BuildOwner, cfg.BuildRepo, matchedRun.ID)
			if err == nil {
				result["jobs"] = buildWorkflowJobStatusList(jobs)
			}
		}

		writeBuildStatusResponse(w, responseCacheKey, result)
		return
	}

	if cachedRunID, ok := getCachedProfileRunID(cacheKey); ok {
		run, inputs, err := h.gh.GetWorkflowRun(cfg.BuildOwner, cfg.BuildRepo, cachedRunID)
		if err == nil {
			if buildRequestMatchesForClient(inputs, client, profile, requestID, tag, branch, core, platforms) || (requestID == "" && hasPendingBuild && matchRunByPendingBuild(run, pendingBuild)) {
				matchedRun = run
				matchedInputs = inputs
				if matchedRun.Status == "completed" {
					deleteCachedProfileRunID(cacheKey)
					clearPendingBuildForClient(pendingCodeID, client, profile, tag, branch, core, platforms)
				}
			} else {
				deleteCachedProfileRunID(cacheKey)
			}
		} else {
			deleteCachedProfileRunID(cacheKey)
		}
	}

	if matchedRun == nil {
		if requestID != "" {
			run, inputs, err := h.findWorkflowRunByRequestIDForClient(client, requestID, false)
			if err != nil {
				jsonError(w, "查询构建状态失败", 500)
				return
			}
			if run != nil {
				matchedRun = run
				matchedInputs = inputs
				if matchedRun.Status == "completed" {
					clearPendingBuildForClient(pendingCodeID, client, profile, tag, branch, core, platforms)
				} else {
					setCachedProfileRunID(cacheKey, run.ID)
				}
			}
		} else {
			runs, err := h.gh.GetRecentWorkflowRuns(cfg.BuildOwner, cfg.BuildRepo, buildWorkflowFileForClient(client), recentWorkflowRunsSearchLimit)
			if err != nil {
				jsonError(w, "查询构建状态失败", 500)
				return
			}

			for i := range runs {
				run := &runs[i]
				if !isActiveWorkflowStatus(run.Status) {
					continue
				}
				matchesByPending := hasPendingBuild && matchRunByPendingBuild(run, pendingBuild)
				if !matchesByPending && requestID == "" {
					continue
				}
				if matchesByPending && requestID == "" {
					matchedRun = run
					setCachedProfileRunID(cacheKey, run.ID)
					break
				}
				inputs, err := h.gh.GetWorkflowRunInputs(cfg.BuildOwner, cfg.BuildRepo, run.ID)
				matchesByInputs := err == nil && buildRequestMatchesForClient(inputs, client, profile, requestID, tag, branch, core, platforms)
				if !matchesByInputs {
					continue
				}
				matchedRun = run
				matchedInputs = inputs
				setCachedProfileRunID(cacheKey, run.ID)
				break
			}
		}
	}

	if matchedRun == nil {
		result := map[string]interface{}{
			"found": false,
		}
		if recordID > 0 {
			result["record_id"] = recordID
		}
		if requestID != "" {
			result["request_id"] = requestID
		}
		if hasPendingBuild {
			result["pending_detected"] = true
		}
		writeBuildStatusResponse(w, responseCacheKey, result)
		return
	}

	conclusion := ""
	if matchedRun.Conclusion != nil {
		conclusion = *matchedRun.Conclusion
	}
	progress, progressText, progressStage := buildProgressForEvent(matchedRun.Status, conclusion, -1, "", "")
	result := map[string]interface{}{
		"found":            true,
		"record_id":        recordID,
		"run_id":           matchedRun.ID,
		"run_url":          matchedRun.HTMLURL,
		"status":           matchedRun.Status,
		"conclusion":       conclusion,
		"progress":         progress,
		"progress_percent": progress,
		"progress_text":    progressText,
		"progress_stage":   progressStage,
		"created_at":       matchedRun.CreatedAt,
		"updated_at":       matchedRun.UpdatedAt,
		"inputs": map[string]string{
			"client":     client,
			"profile":    profile,
			"tag":        tag,
			"branch":     branch,
			"core":       core,
			"platforms":  platforms,
			"request_id": requestID,
		},
	}

	if matchedInputs != nil && len(matchedInputs) > 0 {
		result["inputs"] = matchedInputs
		if strings.TrimSpace(matchedInputs["request_id"]) != "" {
			result["request_id"] = strings.TrimSpace(matchedInputs["request_id"])
		}
		if strings.TrimSpace(matchedInputs["core"]) == "" {
			matchedInputs["core"] = core
		}
	}
	if requestID != "" {
		if _, ok := result["request_id"]; !ok {
			result["request_id"] = requestID
		}
	}

	if includeJobs {
		jobs, err := h.gh.GetWorkflowRunJobs(cfg.BuildOwner, cfg.BuildRepo, matchedRun.ID)
		if err == nil {
			result["jobs"] = buildWorkflowJobStatusList(jobs)
		}
	}

	writeBuildStatusResponse(w, responseCacheKey, result)
}

func (h *Handlers) CancelBuildRecord(w http.ResponseWriter, r *http.Request) {
	claims := getClaims(r)
	recordID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || recordID <= 0 {
		jsonError(w, "无效的打包记录 ID", 400)
		return
	}

	record, err := getBuildRecord(recordID)
	if err != nil {
		jsonError(w, "打包记录不存在", 404)
		return
	}
	if !canAccessBuildRecord(claims, record) {
		jsonError(w, "无权停止该打包记录", 403)
		return
	}
	reconciled := h.reconcileBuildRecords([]BuildRecord{*record}, true)
	if len(reconciled) > 0 {
		record = &reconciled[0]
	}

	if record.Status == "completed" {
		jsonResponse(w, map[string]interface{}{
			"message":    "打包已结束，无需停止",
			"record_id":  record.ID,
			"run_id":     record.RunID,
			"status":     record.Status,
			"conclusion": record.Conclusion,
		})
		return
	}
	if record.Status == "cancel_requested" {
		if record.RunID > 0 {
			run, inputs, err := h.gh.GetWorkflowRun(cfg.BuildOwner, cfg.BuildRepo, record.RunID)
			if err == nil && run != nil && workflowRunMatchesRecord(record, run, inputs) && isWorkflowRunCompleted(run) {
				conclusion := workflowRunConclusion(run)
				record, _ = h.persistBuildRecordEvent(record, run.ID, run.Status, conclusion, "github", run.HTMLURL, "")
				message := "打包已结束，无需停止"
				confirmed := false
				if conclusion == "cancelled" {
					message = "GitHub 已确认取消本次打包"
					confirmed = true
				}
				jsonResponse(w, map[string]interface{}{
					"message":    message,
					"record_id":  record.ID,
					"run_id":     record.RunID,
					"status":     record.Status,
					"conclusion": record.Conclusion,
					"confirmed":  confirmed,
				})
				return
			}
		}
		jsonResponse(w, map[string]interface{}{
			"message":    "已提交停止请求，等待 GitHub 确认取消",
			"record_id":  record.ID,
			"run_id":     record.RunID,
			"status":     record.Status,
			"conclusion": record.Conclusion,
			"confirmed":  false,
		})
		return
	}
	if record.RunID > 0 {
		run, inputs, err := h.gh.GetWorkflowRun(cfg.BuildOwner, cfg.BuildRepo, record.RunID)
		if err == nil && run != nil && workflowRunMatchesRecord(record, run, inputs) && run.Status == "completed" {
			conclusion := ""
			if run.Conclusion != nil {
				conclusion = *run.Conclusion
			}
			record, _ = h.persistBuildRecordEvent(record, run.ID, run.Status, conclusion, "github", run.HTMLURL, "")
			confirmed := conclusion == "cancelled"
			message := "打包已结束，无需停止"
			if confirmed {
				message = "GitHub 已确认取消本次打包"
			}
			jsonResponse(w, map[string]interface{}{
				"message":    message,
				"record_id":  record.ID,
				"run_id":     run.ID,
				"status":     run.Status,
				"conclusion": conclusion,
				"confirmed":  confirmed,
			})
			return
		}
	}

	run, err := h.findActiveWorkflowRunForRecord(record)
	if err != nil {
		jsonError(w, "查询可停止的 GitHub 运行失败", 500)
		return
	}
	if run == nil {
		conclusion := record.Conclusion
		if conclusion == "" {
			conclusion = "cancelled"
		}
		record, _ = h.persistBuildRecordEvent(record, record.RunID, "completed", conclusion, "server", record.RunURL, record.ReleaseTag)
		jsonResponse(w, map[string]interface{}{
			"message":    "未发现仍在运行的 GitHub 打包，已将本地记录标记为已取消",
			"record_id":  record.ID,
			"run_id":     record.RunID,
			"status":     "completed",
			"conclusion": conclusion,
			"confirmed":  true,
		})
		return
	}

	if err := h.gh.CancelWorkflowRun(cfg.BuildOwner, cfg.BuildRepo, run.ID); err != nil {
		jsonError(w, "停止 GitHub 打包失败: "+err.Error(), 500)
		return
	}

	confirmedRun, waitErr := h.waitForWorkflowRunTerminalState(record, run.ID, 8*time.Second)
	if waitErr != nil {
		log.Printf("等待 GitHub 取消状态确认失败: record_id=%d run_id=%d err=%v", record.ID, run.ID, waitErr)
	}

	confirmed := confirmedRun != nil && isWorkflowRunCancelled(confirmedRun)
	if confirmedRun != nil && isWorkflowRunCompleted(confirmedRun) {
		record, err = h.persistBuildRecordEvent(record, confirmedRun.ID, confirmedRun.Status, workflowRunConclusion(confirmedRun), "github", confirmedRun.HTMLURL, "")
		if err != nil {
			jsonError(w, "更新打包记录状态失败", 500)
			return
		}
	} else {
		record, err = h.persistBuildRecordEvent(record, run.ID, "cancel_requested", "cancelled", "server", run.HTMLURL, "")
		if err != nil {
			jsonError(w, "更新打包记录状态失败", 500)
			return
		}
	}

	logAudit(claims.CodeID, claims.CodeName, "cancel_build",
		fmt.Sprintf("record_id=%d profile=%s tag=%s platforms=%s branch=%s run_id=%d", record.ID, record.Profile, record.Tag, record.Platforms, record.Branch, run.ID),
		r.RemoteAddr)

	message := "已提交停止请求，等待 GitHub 确认取消"
	if confirmed {
		message = "GitHub 已确认取消本次打包"
	}
	jsonResponse(w, map[string]interface{}{
		"message":    message,
		"record_id":  record.ID,
		"run_id":     record.RunID,
		"status":     record.Status,
		"conclusion": record.Conclusion,
		"confirmed":  confirmed,
	})
}

func (h *Handlers) GetBuildQueue(w http.ResponseWriter, r *http.Request) {
	claims := getClaims(r)
	clientConfig, err := buildClientConfig(r.URL.Query().Get("client"))
	if err != nil {
		jsonError(w, err.Error(), 400)
		return
	}
	if ok, message := canAccessClientForClaims(claims, clientConfig.ID); !ok {
		jsonError(w, message, 403)
		return
	}
	core, err := validateBuildCoreForClient(clientConfig.ID, r.URL.Query().Get("core"))
	if err != nil {
		jsonError(w, err.Error(), 400)
		return
	}
	platforms := strings.TrimSpace(r.URL.Query().Get("platforms"))
	if _, err := expandRequestedBuildPlatformsForClient(clientConfig.ID, core, platforms); err != nil {
		jsonError(w, err.Error(), 400)
		return
	}
	queueSnapshot, err := h.getBuildQueueSnapshotForClient(clientConfig.ID, core, platforms)
	if err != nil {
		jsonResponse(w, BuildQueueSnapshot{
			MaxJobs:      maxConcurrentBuildJobs,
			MaxMacOSJobs: maxConcurrentMacOSBuildJobs,
			Available:    true,
		})
		return
	}
	jsonResponse(w, map[string]interface{}{
		"running":              queueSnapshot.ActiveJobs,
		"max":                  queueSnapshot.MaxJobs,
		"active_runs":          queueSnapshot.ActiveRuns,
		"active_jobs":          queueSnapshot.ActiveJobs,
		"active_macos_jobs":    queueSnapshot.ActiveMacOSJobs,
		"max_jobs":             queueSnapshot.MaxJobs,
		"max_macos_jobs":       queueSnapshot.MaxMacOSJobs,
		"requested_platforms":  queueSnapshot.RequestedPlatforms,
		"requested_jobs":       queueSnapshot.RequestedJobs,
		"requested_macos_jobs": queueSnapshot.RequestedMacOSJobs,
		"remaining_jobs":       queueSnapshot.RemainingJobs,
		"remaining_macos_jobs": queueSnapshot.RemainingMacOSJobs,
		"available":            queueSnapshot.Available,
		"message":              queueSnapshot.Message,
	})
}

func (h *Handlers) ListBuildRecords(w http.ResponseWriter, r *http.Request) {
	claims := getClaims(r)
	client := normalizeBuildClient(r.URL.Query().Get("client"))
	if ok, message := canAccessClientForClaims(claims, client); !ok {
		jsonError(w, message, 403)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	records, err := listBuildRecordsByClient(claims.CodeID, claims.Permissions == "admin", client, limit)
	if err != nil {
		jsonError(w, "获取打包记录失败", 500)
		return
	}
	records = h.reconcileBuildRecords(records, false)

	items := make([]map[string]interface{}, 0, len(records))
	for _, record := range records {
		if normalizeBuildClient(record.Client) != client {
			continue
		}
		if claims.Permissions != "admin" && record.CodeID != claims.CodeID {
			continue
		}
		items = append(items, buildRecordResponse(record))
	}

	jsonResponse(w, map[string]interface{}{
		"records": items,
	})
}

func (h *Handlers) GetBuildRecordAssets(w http.ResponseWriter, r *http.Request) {
	claims := getClaims(r)
	recordID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || recordID <= 0 {
		jsonError(w, "无效的打包记录 ID", 400)
		return
	}

	record, err := getBuildRecord(recordID)
	if err != nil {
		jsonError(w, "打包记录不存在", 404)
		return
	}
	if !canAccessBuildRecord(claims, record) {
		jsonError(w, "无权访问该打包记录", 403)
		return
	}
	reconciled := h.reconcileBuildRecords([]BuildRecord{*record}, true)
	if len(reconciled) > 0 {
		record = &reconciled[0]
	}

	releaseTag := buildReleaseTag(record)
	if releaseTag == "" {
		jsonResponse(w, map[string]interface{}{
			"record":      buildRecordResponse(*record),
			"available":   false,
			"release_tag": "",
			"assets":      []map[string]interface{}{},
		})
		return
	}

	release, err := h.gh.GetReleaseByTag(cfg.BuildOwner, cfg.BuildRepo, releaseTag)
	if err != nil {
		jsonError(w, "获取打包产物失败", 500)
		return
	}
	if release == nil {
		jsonResponse(w, map[string]interface{}{
			"record":      buildRecordResponse(*record),
			"available":   false,
			"release_tag": releaseTag,
			"assets":      []map[string]interface{}{},
		})
		return
	}

	assets := []map[string]interface{}{}
	for _, asset := range release.Assets {
		assets = append(assets, map[string]interface{}{
			"id":             asset.ID,
			"name":           asset.Name,
			"size":           asset.Size,
			"content_type":   asset.ContentType,
			"download_count": asset.DownloadCount,
			"updated_at":     asset.UpdatedAt,
		})
	}

	jsonResponse(w, map[string]interface{}{
		"record":      buildRecordResponse(*record),
		"available":   true,
		"release_tag": release.TagName,
		"assets":      assets,
	})
}

func (h *Handlers) CreateBuildRecordAssetDownloadLink(w http.ResponseWriter, r *http.Request) {
	claims := getClaims(r)
	recordID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || recordID <= 0 {
		jsonError(w, "无效的打包记录 ID", 400)
		return
	}
	assetID, err := strconv.ParseInt(chi.URLParam(r, "assetID"), 10, 64)
	if err != nil || assetID <= 0 {
		jsonError(w, "无效的资源 ID", 400)
		return
	}

	record, err := getBuildRecord(recordID)
	if err != nil {
		jsonError(w, "打包记录不存在", 404)
		return
	}
	if !canAccessBuildRecord(claims, record) {
		jsonError(w, "无权访问该打包产物", 403)
		return
	}

	asset, err := h.resolveBuildRecordAsset(record, assetID)
	if err != nil {
		switch err.Error() {
		case "当前打包尚未生成可下载产物", "打包产物不存在":
			jsonError(w, err.Error(), 404)
		default:
			jsonError(w, err.Error(), 500)
		}
		return
	}

	token, expiresAt, err := generateBuildAssetDownloadToken(record.ID, asset.ID, buildAssetDownloadLinkTTL)
	if err != nil {
		jsonError(w, "生成下载链接失败", 500)
		return
	}

	logAudit(claims.CodeID, claims.CodeName, "create_build_asset_download_link",
		fmt.Sprintf("record_id=%d asset_id=%d asset_name=%s expires_at=%s", record.ID, asset.ID, asset.Name, expiresAt.UTC().Format(time.RFC3339)),
		r.RemoteAddr)

	jsonResponse(w, map[string]interface{}{
		"record_id":          record.ID,
		"asset_id":           asset.ID,
		"asset_name":         asset.Name,
		"download_url":       buildAssetSignedDownloadURL(r, record.ID, asset.ID, token),
		"expires_at":         expiresAt.UTC().Format(time.RFC3339),
		"expires_in_seconds": int(buildAssetDownloadLinkTTL.Seconds()),
	})
}

func (h *Handlers) DeleteBuildRecord(w http.ResponseWriter, r *http.Request) {
	claims := getClaims(r)
	recordID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || recordID <= 0 {
		jsonError(w, "无效的打包记录 ID", 400)
		return
	}

	record, err := getBuildRecord(recordID)
	if err != nil {
		jsonError(w, "打包记录不存在", 404)
		return
	}
	if !canAccessBuildRecord(claims, record) {
		jsonError(w, "无权删除该打包记录", 403)
		return
	}
	reconciled := h.reconcileBuildRecords([]BuildRecord{*record}, true)
	if len(reconciled) > 0 {
		record = &reconciled[0]
	}
	if isActiveWorkflowStatus(record.Status) {
		activeRun, activeErr := h.findStrictActiveWorkflowRunForRecord(record)
		if activeErr == nil && activeRun == nil {
			conclusion := record.Conclusion
			if conclusion == "" {
				conclusion = "cancelled"
			}
			if strings.TrimSpace(record.ReleaseTag) == "" {
				record.ReleaseTag = buildReleaseTag(record)
			}
			record, _ = h.persistBuildRecordEvent(record, record.RunID, "completed", conclusion, "server", record.RunURL, record.ReleaseTag)
			record.RunID = 0
		} else {
			jsonError(w, "正在打包的记录暂不支持删除", 409)
			return
		}
	}

	releaseTag := buildReleaseTag(record)
	if err := h.deleteBuildRecordRelease(record); err != nil {
		jsonError(w, "删除 GitHub 打包产物失败", 500)
		return
	}
	if releaseTag != "" {
		githubReleaseCache.delete(githubReleaseCacheKey(cfg.BuildOwner, cfg.BuildRepo, releaseTag))
	}
	if record.RunID > 0 {
		run, inputs, err := h.gh.GetWorkflowRun(cfg.BuildOwner, cfg.BuildRepo, record.RunID)
		if err == nil && run != nil && workflowRunMatchesRecord(record, run, inputs) {
			if err := h.gh.DeleteWorkflowRun(cfg.BuildOwner, cfg.BuildRepo, record.RunID); err != nil {
				jsonError(w, "删除 GitHub Actions 运行失败", 500)
				return
			}
		}
	}
	if err := deleteBuildRecord(record.ID); err != nil {
		jsonError(w, "删除打包记录失败", 500)
		return
	}

	deleteCachedProfileRunID(buildRunCacheKeyForClient(record.CodeID, record.Client, record.Profile, buildRecordRequestID(record), record.Tag, record.Branch, record.Core, record.Platforms))
	clearPendingBuildForClient(record.CodeID, record.Client, record.Profile, record.Tag, record.Branch, record.Core, record.Platforms)
	invalidateBuildCachesForRecord(record)

	logAudit(claims.CodeID, claims.CodeName, "delete_build_record",
		fmt.Sprintf("record_id=%d release_tag=%s", record.ID, releaseTag),
		r.RemoteAddr)

	jsonResponse(w, map[string]interface{}{
		"message":     "删除成功",
		"record_id":   record.ID,
		"release_tag": releaseTag,
	})
}

func (h *Handlers) DownloadBuildRecordAsset(w http.ResponseWriter, r *http.Request) {
	claims := getClaims(r)
	recordID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || recordID <= 0 {
		jsonError(w, "无效的打包记录 ID", 400)
		return
	}
	assetID, err := strconv.ParseInt(chi.URLParam(r, "assetID"), 10, 64)
	if err != nil || assetID <= 0 {
		jsonError(w, "无效的资源 ID", 400)
		return
	}

	record, err := getBuildRecord(recordID)
	if err != nil {
		jsonError(w, "打包记录不存在", 404)
		return
	}
	if !canAccessBuildRecord(claims, record) {
		jsonError(w, "无权下载该打包产物", 403)
		return
	}

	matchedAsset, err := h.resolveBuildRecordAsset(record, assetID)
	if err != nil {
		switch err.Error() {
		case "当前打包尚未生成可下载产物", "打包产物不存在":
			jsonError(w, err.Error(), 404)
		default:
			jsonError(w, err.Error(), 500)
		}
		return
	}

	resp, err := h.gh.DownloadReleaseAsset(cfg.BuildOwner, cfg.BuildRepo, matchedAsset.ID)
	if err != nil {
		jsonError(w, "下载打包产物失败", 500)
		return
	}
	defer resp.Body.Close()

	writeReleaseAssetToResponse(w, matchedAsset, resp)

	logAudit(claims.CodeID, claims.CodeName, "download_build_asset",
		fmt.Sprintf("record_id=%d asset_id=%d asset_name=%s", record.ID, matchedAsset.ID, matchedAsset.Name),
		r.RemoteAddr)
}

func (h *Handlers) DownloadBuildRecordAssetByToken(w http.ResponseWriter, r *http.Request) {
	tokenString := strings.TrimSpace(r.URL.Query().Get("token"))
	if tokenString == "" {
		writeDownloadLinkStatusPage(w, http.StatusBadRequest, "下载链接无效", "缺少下载令牌，请重新获取下载链接后再试。")
		return
	}

	downloadClaims, err := parseBuildAssetDownloadToken(tokenString)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			writeDownloadLinkStatusPage(w, http.StatusGone, "下载链接已过期", "该下载链接仅在生成后的 10 分钟内有效，当前链接已经失效。")
			return
		}
		writeDownloadLinkStatusPage(w, http.StatusForbidden, "下载链接无效", "当前下载链接无法通过校验，可能已经损坏、被修改或不再可用。")
		return
	}

	recordID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || recordID <= 0 || recordID != downloadClaims.RecordID {
		writeDownloadLinkStatusPage(w, http.StatusForbidden, "下载链接无效", "下载链接中的记录信息不匹配，请重新获取新的下载链接。")
		return
	}
	assetID, err := strconv.ParseInt(chi.URLParam(r, "assetID"), 10, 64)
	if err != nil || assetID <= 0 || assetID != downloadClaims.AssetID {
		writeDownloadLinkStatusPage(w, http.StatusForbidden, "下载链接无效", "下载链接中的产物信息不匹配，请重新获取新的下载链接。")
		return
	}

	record, err := getBuildRecord(recordID)
	if err != nil {
		writeDownloadLinkStatusPage(w, http.StatusNotFound, "打包记录不存在", "对应的打包记录已不存在，可能已被删除。")
		return
	}

	matchedAsset, err := h.resolveBuildRecordAsset(record, assetID)
	if err != nil {
		switch err.Error() {
		case "当前打包尚未生成可下载产物", "打包产物不存在":
			writeDownloadLinkStatusPage(w, http.StatusNotFound, "打包产物不存在", "当前打包产物已不存在、尚未生成，或已经被删除。")
		default:
			writeDownloadLinkStatusPage(w, http.StatusInternalServerError, "下载暂时不可用", "服务器暂时无法读取该打包产物，请稍后重新获取下载链接再试。")
		}
		return
	}

	resp, err := h.gh.DownloadReleaseAsset(cfg.BuildOwner, cfg.BuildRepo, matchedAsset.ID)
	if err != nil {
		writeDownloadLinkStatusPage(w, http.StatusBadGateway, "下载暂时不可用", "服务器暂时无法连接到产物源，请稍后重新获取下载链接再试。")
		return
	}
	defer resp.Body.Close()

	writeReleaseAssetToResponse(w, matchedAsset, resp)

	logAudit(record.CodeID, record.CodeName, "download_build_asset_signed",
		fmt.Sprintf("record_id=%d asset_id=%d asset_name=%s", record.ID, matchedAsset.ID, matchedAsset.Name),
		r.RemoteAddr)
}

// ==================== 管理后台 ====================

func (h *Handlers) ListCodes(w http.ResponseWriter, r *http.Request) {
	codes, err := listCodes()
	if err != nil {
		jsonError(w, "获取激活码列表失败", 500)
		return
	}
	if codes == nil {
		codes = []ActivationCode{}
	}
	jsonResponse(w, codes)
}

func (h *Handlers) GetGitHubAPIStats(w http.ResponseWriter, r *http.Request) {
	items := snapshotGitHubAPICallStats()
	latest := map[string]string{}
	var latestItem *GitHubAPICallStat
	for _, item := range items {
		if item.RateRemaining == "" && item.RateReset == "" && item.RateUsed == "" {
			continue
		}
		if latestItem == nil || item.LastAtUnixNano > latestItem.LastAtUnixNano {
			itemCopy := item
			latestItem = &itemCopy
		}
	}
	if latestItem != nil {
		latest = map[string]string{
			"method":           latestItem.Method,
			"path":             latestItem.Path,
			"status":           strconv.Itoa(latestItem.Status),
			"rate_limit":       latestItem.RateLimit,
			"rate_remaining":   latestItem.RateRemaining,
			"rate_used":        latestItem.RateUsed,
			"rate_reset":       latestItem.RateReset,
			"rate_reset_local": latestItem.RateResetLocal,
			"rate_resource":    latestItem.RateResource,
			"last_at":          latestItem.LastAt,
			"last_request_id":  latestItem.LastRequestID,
		}
	}
	jsonResponse(w, map[string]interface{}{
		"latest": latest,
		"items":  items,
	})
}

func (h *Handlers) CreateCode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name             string   `json:"name"`
		MaxUses          int      `json:"max_uses"`
		AllowedProfiles  []string `json:"allowed_profiles"`
		ManualProfiles   []string `json:"manual_profiles"`
		AllowedPlatforms []string `json:"allowed_platforms"`
		AllowedClients   []string `json:"allowed_clients"`
		ExpiresAt        string   `json:"expires_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "请求格式错误", 400)
		return
	}
	if req.MaxUses < -1 {
		jsonError(w, "可打包次数不能小于 -1", 400)
		return
	}
	allowedPlatforms, err := validateAllowedBuildPlatforms(req.AllowedPlatforms)
	if err != nil {
		jsonError(w, err.Error(), 400)
		return
	}
	allowedClients, err := validateAllowedBuildClients(req.AllowedClients)
	if err != nil {
		jsonError(w, err.Error(), 400)
		return
	}
	if _, err := normalizeExpiresAt(req.ExpiresAt); err != nil {
		jsonError(w, "创建激活码失败: "+err.Error(), 400)
		return
	}
	allowedProfiles, createdProfiles, err := h.resolveAllowedProfiles(req.AllowedProfiles, req.ManualProfiles, allowedClients)
	if err != nil {
		jsonError(w, "创建手动档案失败: "+err.Error(), 400)
		return
	}

	ac, err := createCode(req.Name, req.MaxUses, allowedProfiles, allowedPlatforms, allowedClients, req.ExpiresAt)
	if err != nil {
		statusCode := 500
		if strings.Contains(err.Error(), "到期时间格式错误") {
			statusCode = 400
		}
		jsonError(w, "创建激活码失败: "+err.Error(), statusCode)
		return
	}

	claims := getClaims(r)
	logAudit(claims.CodeID, claims.CodeName, "create_code", ac.Name, r.RemoteAddr)

	jsonResponse(w, map[string]interface{}{
		"id":                ac.ID,
		"code":              ac.Code,
		"name":              ac.Name,
		"permissions":       ac.Permissions,
		"max_uses":          ac.MaxUses,
		"used_count":        ac.UsedCount,
		"allowed_profiles":  ac.AllowedProfiles,
		"allowed_platforms": ac.AllowedPlatforms,
		"allowed_clients":   ac.AllowedClients,
		"expires_at":        ac.ExpiresAt,
		"created_at":        ac.CreatedAt,
		"is_active":         ac.IsActive,
		"created_profiles":  createdProfiles,
	})
}

func (h *Handlers) UpdateCode(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		jsonError(w, "无效的 ID", 400)
		return
	}

	var req struct {
		Name             string   `json:"name"`
		MaxUses          int      `json:"max_uses"`
		UsedCount        int      `json:"used_count"`
		AllowedProfiles  []string `json:"allowed_profiles"`
		ManualProfiles   []string `json:"manual_profiles"`
		AllowedPlatforms []string `json:"allowed_platforms"`
		AllowedClients   []string `json:"allowed_clients"`
		ExpiresAt        string   `json:"expires_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "请求格式错误", 400)
		return
	}
	if req.MaxUses < -1 {
		jsonError(w, "可打包次数不能小于 -1", 400)
		return
	}
	if req.UsedCount < 0 {
		jsonError(w, "已打包次数不能小于 0", 400)
		return
	}
	allowedPlatforms, err := validateAllowedBuildPlatforms(req.AllowedPlatforms)
	if err != nil {
		jsonError(w, err.Error(), 400)
		return
	}
	allowedClients, err := validateAllowedBuildClients(req.AllowedClients)
	if err != nil {
		jsonError(w, err.Error(), 400)
		return
	}
	if _, err := normalizeExpiresAt(req.ExpiresAt); err != nil {
		jsonError(w, "更新失败: "+err.Error(), 400)
		return
	}
	if _, err := getActivationCodeByID(id); err != nil {
		jsonError(w, "更新失败: 激活码不存在", 404)
		return
	}
	allowedProfiles, createdProfiles, err := h.resolveAllowedProfiles(req.AllowedProfiles, req.ManualProfiles, allowedClients)
	if err != nil {
		jsonError(w, "创建手动档案失败: "+err.Error(), 400)
		return
	}

	if err := updateCode(id, req.Name, req.MaxUses, req.UsedCount, allowedProfiles, allowedPlatforms, allowedClients, req.ExpiresAt); err != nil {
		statusCode := 500
		if strings.Contains(err.Error(), "到期时间格式错误") {
			statusCode = 400
		} else if strings.Contains(err.Error(), "激活码不存在") {
			statusCode = 404
		}
		jsonError(w, "更新失败: "+err.Error(), statusCode)
		return
	}

	claims := getClaims(r)
	logAudit(claims.CodeID, claims.CodeName, "update_code", fmt.Sprintf("id=%d name=%s used_count=%d", id, req.Name, req.UsedCount), r.RemoteAddr)

	jsonResponse(w, map[string]interface{}{
		"message":          "更新成功",
		"allowed_profiles": allowedProfiles,
		"allowed_clients":  allowedClients,
		"created_profiles": createdProfiles,
	})
}

func (h *Handlers) BatchUpdateCodeClients(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs            []int    `json:"ids"`
		AllowedClients []string `json:"allowed_clients"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "请求格式错误", 400)
		return
	}
	if len(req.IDs) == 0 {
		jsonError(w, "请选择要批量编辑的激活码", 400)
		return
	}
	allowedClients, err := validateAllowedBuildClients(req.AllowedClients)
	if err != nil {
		jsonError(w, err.Error(), 400)
		return
	}
	updatedCount, err := updateCodesAllowedClients(req.IDs, allowedClients)
	if err != nil {
		statusCode := 500
		if strings.Contains(err.Error(), "请选择") || strings.Contains(err.Error(), "未找到") {
			statusCode = 400
		}
		jsonError(w, "批量更新失败: "+err.Error(), statusCode)
		return
	}

	claims := getClaims(r)
	logAudit(claims.CodeID, claims.CodeName, "batch_update_code_clients", fmt.Sprintf("ids=%v allowed_clients=%v updated=%d", req.IDs, allowedClients, updatedCount), r.RemoteAddr)

	jsonResponse(w, map[string]interface{}{
		"message":         "批量更新成功",
		"updated_count":   updatedCount,
		"allowed_clients": allowedClients,
	})
}

func (h *Handlers) DeleteCode(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		jsonError(w, "无效的 ID", 400)
		return
	}

	if err := deleteCode(id); err != nil {
		jsonError(w, "删除失败", 500)
		return
	}

	claims := getClaims(r)
	logAudit(claims.CodeID, claims.CodeName, "delete_code", idStr, r.RemoteAddr)

	jsonResponse(w, map[string]string{"message": "删除成功"})
}

type profileRenameTarget struct {
	Client string
	GH     *GitHubClient
}

type profileRenamePlan struct {
	Target  profileRenameTarget
	Content string
	SHA     string
}

func isRenameAllClients(clientParam string) bool {
	clientParam = strings.TrimSpace(clientParam)
	return clientParam == "" || strings.EqualFold(clientParam, "all")
}

func profileGitHubTargetKey(gh *GitHubClient) string {
	if gh == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(gh.Owner)) + "/" + strings.ToLower(strings.TrimSpace(gh.Repo)) + "#" + strings.TrimSpace(gh.Branch)
}

func (h *Handlers) profileRenameTargets(clientParam string) ([]profileRenameTarget, error) {
	clientParam = strings.TrimSpace(clientParam)
	clients := []string{}
	if clientParam == "" || strings.EqualFold(clientParam, "all") {
		clients = []string{buildClientLegacy, buildClientNexGenReact}
	} else {
		client := normalizeBuildClient(clientParam)
		switch client {
		case buildClientLegacy, buildClientNexGenReact:
			clients = []string{client}
		default:
			return nil, fmt.Errorf("无效的客户端：%s", clientParam)
		}
	}

	targets := []profileRenameTarget{}
	seenTargets := map[string]struct{}{}
	for _, client := range clients {
		gh := h.profileGitHubClient(client)
		key := profileGitHubTargetKey(gh)
		if key == "" {
			return nil, fmt.Errorf("配置档案仓库未初始化")
		}
		if _, ok := seenTargets[key]; ok {
			continue
		}
		seenTargets[key] = struct{}{}
		targets = append(targets, profileRenameTarget{
			Client: client,
			GH:     gh,
		})
	}
	return targets, nil
}

func profileContentUsesNexGenRoot(content string) bool {
	doc, err := parseProfileYamlDocument(content)
	if err != nil {
		return false
	}
	root := ensureDocumentMappingNode(doc)
	return getMapValueNode(root, "nexgen") != nil
}

func renamedProfileContentForClient(client, content, newName string) string {
	if normalizeBuildClient(client) == buildClientNexGenReact || profileContentUsesNexGenRoot(content) {
		return bindNexGenProfileTitle(normalizeNexGenProfileConfig(content), newName)
	}
	return bindProfileTitle(content, newName)
}

func otherBuildClient(client string) string {
	if normalizeBuildClient(client) == buildClientNexGenReact {
		return buildClientLegacy
	}
	return buildClientNexGenReact
}

func (h *Handlers) guardSingleClientProfileRename(client, oldName string) error {
	otherClient := otherBuildClient(client)
	if profileGitHubTargetKey(h.profileGitHubClient(client)) == profileGitHubTargetKey(h.profileGitHubClient(otherClient)) {
		return nil
	}
	_, _, _, exists, err := h.getStoredProfileForClient(otherClient, oldName)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("另一个项目也存在同名档案，请选择“全部项目”同步重命名，避免激活码绑定断开")
	}
	return nil
}

func (h *Handlers) RenameProfile(w http.ResponseWriter, r *http.Request) {
	oldName := strings.TrimSpace(chi.URLParam(r, "name"))
	clientParam := r.URL.Query().Get("client")
	targets, err := h.profileRenameTargets(clientParam)
	if err != nil {
		jsonError(w, err.Error(), 400)
		return
	}
	var req struct {
		NewName string `json:"new_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "请求格式错误", 400)
		return
	}
	newName := strings.TrimSpace(req.NewName)
	if oldName == "" || newName == "" {
		jsonError(w, "档案名称不能为空", 400)
		return
	}
	if oldName == newName {
		jsonError(w, "新档案名称不能与原名称相同", 400)
		return
	}

	oldPath, err := profileFilePath(oldName)
	if err != nil {
		jsonError(w, err.Error(), 400)
		return
	}
	newPath, err := profileFilePath(newName)
	if err != nil {
		jsonError(w, err.Error(), 400)
		return
	}

	if !isRenameAllClients(clientParam) && len(targets) == 1 {
		if err := h.guardSingleClientProfileRename(targets[0].Client, oldName); err != nil {
			jsonError(w, err.Error(), 409)
			return
		}
	}

	plans := []profileRenamePlan{}
	for _, target := range targets {
		_, _, _, newExists, err := h.getStoredProfileForClient(target.Client, newName)
		if err != nil {
			jsonError(w, "检查新档案名称失败: "+err.Error(), 500)
			return
		}
		if newExists {
			jsonError(w, "新档案名称已存在", 409)
			return
		}

		content, sha, _, exists, err := h.getStoredProfileForClient(target.Client, oldName)
		if err != nil {
			jsonError(w, "读取原档案失败: "+err.Error(), 500)
			return
		}
		if !exists {
			continue
		}
		plans = append(plans, profileRenamePlan{
			Target:  target,
			Content: content,
			SHA:     sha,
		})
	}
	if len(plans) == 0 {
		jsonError(w, "原档案不存在", 404)
		return
	}

	renamedClients := []string{}
	createdFiles := []profileRenamePlan{}
	for _, plan := range plans {
		renamedContent := renamedProfileContentForClient(plan.Target.Client, plan.Content, newName)
		if _, err := plan.Target.GH.SaveFile(newPath, renamedContent, "", "重命名配置档案: "+oldName+" -> "+newName); err != nil {
			jsonError(w, "创建新档案失败: "+err.Error(), 500)
			return
		}
		createdFiles = append(createdFiles, plan)
	}
	for _, plan := range createdFiles {
		if err := plan.Target.GH.DeleteFile(oldPath, plan.SHA, "删除重命名前旧档案: "+oldName); err != nil {
			jsonError(w, "删除旧档案失败，新档案已创建，请稍后手动清理旧档案: "+err.Error(), 500)
			return
		}
		renamedClients = append(renamedClients, plan.Target.Client)
	}

	if err := renameProfileReferences(oldName, newName); err != nil {
		jsonError(w, "同步档案引用失败: "+err.Error(), 500)
		return
	}

	invalidateProfileCache()
	buildStatusCache.clear()
	buildQueueSnapshotCache.clear()

	claims := getClaims(r)
	logAudit(claims.CodeID, claims.CodeName, "rename_profile", fmt.Sprintf("%s -> %s clients=%v", oldName, newName, renamedClients), r.RemoteAddr)

	jsonResponse(w, map[string]interface{}{
		"message":         "重命名成功",
		"old_name":        oldName,
		"new_name":        newName,
		"renamed_clients": renamedClients,
	})
}

func (h *Handlers) GetAuditLogs(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil {
			limit = n
		}
	}

	logs, err := getAuditLogs(limit)
	if err != nil {
		jsonError(w, "获取日志失败", 500)
		return
	}
	if logs == nil {
		logs = []map[string]interface{}{}
	}
	jsonResponse(w, logs)
}

func (h *Handlers) GetSystemSettings(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, map[string]interface{}{
		"client_updates_limit":  getClientUpdatesLimit(),
		"custom_feature_groups": getCustomFeatureGroups(),
	})
}

func (h *Handlers) SaveSystemSettings(w http.ResponseWriter, r *http.Request) {
	claims := getClaims(r)
	var req struct {
		ClientUpdatesLimit  int                  `json:"client_updates_limit"`
		CustomFeatureGroups []CustomFeatureGroup `json:"custom_feature_groups"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "请求格式错误", 400)
		return
	}

	limit := normalizeClientUpdatesLimit(req.ClientUpdatesLimit)
	if err := setClientUpdatesLimit(limit); err != nil {
		jsonError(w, "保存系统设置失败", 500)
		return
	}
	if err := setCustomFeatureGroups(req.CustomFeatureGroups); err != nil {
		jsonError(w, "保存系统设置失败", 500)
		return
	}

	logAudit(claims.CodeID, claims.CodeName, "update_settings", fmt.Sprintf("client_updates_limit=%d custom_feature_groups=%d", limit, len(normalizeCustomFeatureGroups(req.CustomFeatureGroups))), r.RemoteAddr)
	jsonResponse(w, map[string]interface{}{
		"message":               "设置已保存",
		"client_updates_limit":  limit,
		"custom_feature_groups": normalizeCustomFeatureGroups(req.CustomFeatureGroups),
	})
}
