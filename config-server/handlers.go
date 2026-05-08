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
	profileListCacheTTL       = 90 * time.Second
	buildQueueSnapshotTTL     = 15 * time.Second
	buildStatusActiveCacheTTL = 8 * time.Second
	buildStatusDoneCacheTTL   = 2 * time.Minute
)

var (
	storedProfilesCache     singleTTLCache[map[string]string]
	buildQueueSnapshotCache keyedTTLCache[BuildQueueSnapshot]
	buildStatusCache        keyedTTLCache[map[string]interface{}]
	buildCacheMu            sync.Mutex
)

func cloneStringMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

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
	buildStatusCache.delete(buildStatusFallbackCacheKey(record.CodeID, record.Profile, buildRecordRequestID(record), record.Tag, record.Branch, record.Core, record.Platforms))
}

func buildStatusFallbackCacheKey(codeID int, profile, requestID, tag, branch, core, platforms string) string {
	if strings.TrimSpace(requestID) != "" {
		return "request:" + strings.TrimSpace(requestID)
	}
	return fmt.Sprintf("fallback:%d|%s|%s|%s|%s|%s", codeID, profile, tag, branch, normalizeBuildCore(core), platforms)
}

func buildStatusCacheKey(record *BuildRecord, codeID int, profile, requestID, tag, branch, core, platforms string) string {
	if record != nil && record.ID > 0 {
		return fmt.Sprintf("record:%d", record.ID)
	}
	if strings.TrimSpace(requestID) != "" {
		return "request:" + strings.TrimSpace(requestID)
	}
	return buildStatusFallbackCacheKey(codeID, profile, requestID, tag, branch, core, platforms)
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
	gh        *GitHubClient
	profileGH *GitHubClient
}

func NewHandlers(gh *GitHubClient, profileGH *GitHubClient) *Handlers {
	return &Handlers{
		gh:        gh,
		profileGH: profileGH,
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
	if yamlContent == "" || profileName == "" {
		return yamlContent
	}

	titleValue, err := json.Marshal(profileName)
	if err != nil {
		return yamlContent
	}
	titleLine := "  title: " + string(titleValue)
	lines := strings.Split(yamlContent, "\n")

	xboardIndex := -1
	providerIndex := -1
	titleIndex := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "xboard:" {
			xboardIndex = i
			continue
		}
		if xboardIndex == -1 {
			continue
		}

		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !strings.HasPrefix(line, "  ") {
			break
		}
		if strings.HasPrefix(line, "    ") {
			continue
		}
		if strings.HasPrefix(trimmed, "provider:") {
			providerIndex = i
			continue
		}
		if strings.HasPrefix(trimmed, "title:") {
			titleIndex = i
			break
		}
	}

	if titleIndex >= 0 {
		lines[titleIndex] = titleLine
		return strings.Join(lines, "\n")
	}
	if xboardIndex == -1 {
		return yamlContent
	}

	insertIndex := xboardIndex + 1
	if providerIndex >= 0 {
		insertIndex = providerIndex + 1
	}
	lines = append(lines[:insertIndex], append([]string{titleLine}, lines[insertIndex:]...)...)
	return strings.Join(lines, "\n")
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

	token, err := generateJWT(ac.ID, ac.Name, "user", ac.AllowedProfiles)
	if err != nil {
		jsonError(w, "生成 Token 失败", 500)
		return
	}

	logAudit(ac.ID, ac.Name, "login", "", r.RemoteAddr)

	jsonResponse(w, map[string]interface{}{
		"token":       token,
		"name":        ac.Name,
		"permissions": "user",
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
			"integration_code_configured": hasAnyCustomFeatureBinding(uiColorFeatureKeys...),
		})
		return
	}

	canBuild, statusText := getBuildAvailability(ac)
	jsonResponse(w, map[string]interface{}{
		"name":                        ac.Name,
		"permissions":                 "user",
		"max_uses":                    ac.MaxUses,
		"used_count":                  ac.UsedCount,
		"remaining_uses":              getRemainingBuildUses(ac),
		"can_build":                   canBuild,
		"build_status_text":           statusText,
		"expires_at":                  ac.ExpiresAt,
		"is_active":                   ac.IsActive,
		"allowed_platforms":           ac.AllowedPlatforms,
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

	token, err := generateJWT(0, "管理员", "admin", nil)
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
	profiles, err := h.listStoredProfiles()
	if err != nil {
		jsonError(w, "加载档案列表失败: "+err.Error(), 500)
		return
	}

	claims := getClaims(r)
	list := []map[string]string{}

	if claims.Permissions == "admin" || len(claims.AllowedProfiles) == 0 {
		names := make([]string, 0, len(profiles))
		for name := range profiles {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			item := map[string]string{"name": name}
			if lu := profiles[name]; lu != "" {
				item["last_updated"] = lu
			}
			list = append(list, item)
		}
	} else {
		names := append([]string(nil), claims.AllowedProfiles...)
		sort.Strings(names)

		for _, name := range names {
			item := map[string]string{"name": name}
			if lu, exists := profiles[name]; exists && lu != "" {
				item["last_updated"] = lu
			}
			list = append(list, item)
		}
	}

	jsonResponse(w, map[string]interface{}{"profiles": list})
}

func (h *Handlers) GetProfile(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(chi.URLParam(r, "name"))
	claims := getClaims(r)

	if !claims.canAccessProfile(name) {
		jsonError(w, "无权访问该档案", 403)
		return
	}

	content, _, lastUpdated, exists, err := h.getStoredProfile(name)
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
	}{YamlContent: content, LastUpdated: lastUpdated}

	if payload.YamlContent != "" {
		cleaned := normalizeSubscriptionConfig(payload.YamlContent)
		if cleaned != payload.YamlContent {
			payload.YamlContent = cleaned
			filePath, err := profileFilePath(name)
			if err == nil {
				_ = h.profileGH.SaveFileWithRetry(filePath, func(_ string) string {
					return cleaned
				}, "修复配置档案: "+name, 3)
				invalidateProfileCache()
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

	if !claims.canAccessProfile(name) {
		jsonError(w, "无权操作该档案", 403)
		return
	}

	var req struct {
		YamlContent     string            `json:"yaml_content"`
		BaseYamlContent string            `json:"base_yaml_content"`
		FormState       *ProfileFormState `json:"form_state"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "请求格式错误", 400)
		return
	}
	if req.FormState != nil {
		baseYamlContent := req.BaseYamlContent
		if strings.TrimSpace(baseYamlContent) == "" {
			baseYamlContent = req.YamlContent
		}
		mergedYamlContent, err := mergeProfileYamlWithForm(baseYamlContent, *req.FormState)
		if err != nil {
			jsonError(w, "合并配置失败: "+err.Error(), 400)
			return
		}
		req.YamlContent = mergedYamlContent
	}
	if claims.Permissions != "admin" {
		req.YamlContent = bindProfileTitle(req.YamlContent, name)
	}
	req.YamlContent = normalizeSubscriptionConfig(req.YamlContent)
	if err := validateYamlContent(req.YamlContent); err != nil {
		jsonError(w, "配置格式错误: "+err.Error(), 400)
		return
	}

	filePath, err := profileFilePath(name)
	if err != nil {
		jsonError(w, err.Error(), 400)
		return
	}

	_, sha, err := h.profileGH.GetFile(filePath)
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			sha = ""
		} else {
			jsonError(w, "加载档案失败: "+err.Error(), 500)
			return
		}
	}

	_, err = h.profileGH.SaveFile(filePath, req.YamlContent, sha, "保存配置档案: "+name)
	if err != nil {
		jsonError(w, "保存失败: "+err.Error(), 500)
		return
	}
	invalidateProfileCache()

	logAudit(claims.CodeID, claims.CodeName, "save_profile", name, r.RemoteAddr)

	jsonResponse(w, map[string]string{"message": "保存成功", "yaml_content": req.YamlContent})
}

func (h *Handlers) DeleteProfile(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(chi.URLParam(r, "name"))
	claims := getClaims(r)

	if !claims.canAccessProfile(name) {
		jsonError(w, "无权操作该档案", 403)
		return
	}

	filePath, err := profileFilePath(name)
	if err != nil {
		jsonError(w, err.Error(), 400)
		return
	}

	_, sha, _, exists, err := h.getStoredProfile(name)
	if err != nil {
		jsonError(w, "加载档案失败: "+err.Error(), 500)
		return
	}
	if exists {
		if err := h.profileGH.DeleteFile(filePath, sha, "删除配置档案: "+name); err != nil {
			jsonError(w, "删除失败: "+err.Error(), 500)
			return
		}
		invalidateProfileCache()
	}

	logAudit(claims.CodeID, claims.CodeName, "delete_profile", name, r.RemoteAddr)

	jsonResponse(w, map[string]string{"message": "删除成功"})
}

// ==================== 构建 ====================

const maxConcurrentBuildJobs = 20
const maxConcurrentMacOSBuildJobs = 5

const recentWorkflowRunsSearchLimit = 100
const fastWorkflowRunsSearchLimit = 20

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

func buildPlatformCatalogForCore(core string) []string {
	core = normalizeBuildCore(core)
	if platforms, ok := buildCorePlatformCatalog[core]; ok {
		return platforms
	}
	return buildPlatformCatalog
}

func buildCoreLabel(core string) string {
	switch normalizeBuildCore(core) {
	case "xray":
		return "Xray"
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
	selectedJobs := expandRequestedBuildPlatformSet(core, platforms)

	demand := BuildJobDemand{Total: len(selectedJobs)}
	for job := range selectedJobs {
		if strings.HasPrefix(job, "macos-") {
			demand.MacOS++
		}
	}
	return demand
}

func expandRequestedBuildPlatformSet(core, platforms string) map[string]struct{} {
	selectedJobs := make(map[string]struct{})
	catalog := buildPlatformCatalogForCore(core)
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
	catalog := buildPlatformCatalogForCore(core)
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
			return fmt.Errorf("%s 内核不支持打包平台：%s", buildCoreLabel(core), token)
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
				if err := addExpandedGroup(token, []string{"windows-amd64", "windows-arm64"}); err != nil {
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
					return nil, fmt.Errorf("%s 内核不支持打包平台：%s", buildCoreLabel(core), token)
				}
			}
		}
	}
	return result, nil
}

func expandRequestedBuildPlatforms(core, platforms string) ([]string, error) {
	return normalizeBuildPlatformListForCore(core, []string{platforms})
}

func validateAllowedBuildPlatforms(allowedPlatforms []string) ([]string, error) {
	if len(allowedPlatforms) == 0 {
		return []string{}, nil
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

func canUseBuildPlatforms(allowedPlatforms []string, core, requestedPlatforms string) (bool, string) {
	allowed, err := validateAllowedBuildPlatforms(allowedPlatforms)
	if err != nil {
		return false, err.Error()
	}
	if len(allowed) == 0 {
		return true, ""
	}

	requested, err := expandRequestedBuildPlatforms(core, requestedPlatforms)
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
	snapshot := BuildQueueSnapshot{
		MaxJobs:      maxConcurrentBuildJobs,
		MaxMacOSJobs: maxConcurrentMacOSBuildJobs,
	}

	runs, err := h.gh.GetActiveWorkflowRuns(cfg.BuildOwner, cfg.BuildRepo, "build.yaml")
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
			if estimated := estimateBuildJobDemand(inputs["core"], inputs["platforms"]); estimated.Total > 0 {
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
	core = normalizeBuildCore(core)
	cacheKey := core + "|" + strings.TrimSpace(platforms)
	if cacheKey == "" {
		cacheKey = core + "|all"
	}
	if cached, ok := buildQueueSnapshotCache.get(cacheKey); ok {
		return cached, nil
	}

	buildCacheMu.Lock()
	defer buildCacheMu.Unlock()
	if cached, ok := buildQueueSnapshotCache.get(cacheKey); ok {
		return cached, nil
	}

	snapshot, err := h.getActiveBuildJobUsage()
	if err != nil {
		return snapshot, err
	}

	demand := estimateBuildJobDemand(core, platforms)
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

func buildPendingCacheKey(codeID int, profile, tag, branch, core, platforms string) string {
	return fmt.Sprintf("code:%d|profile:%s|tag:%s|branch:%s|core:%s|platforms:%s", codeID, profile, tag, branch, normalizeBuildCore(core), platforms)
}

func rememberPendingBuild(codeID int, profile, tag, branch, core, platforms string) PendingBuild {
	pending := PendingBuild{
		CodeID:      codeID,
		Profile:     profile,
		Tag:         tag,
		Branch:      branch,
		Core:        normalizeBuildCore(core),
		Platforms:   platforms,
		TriggeredAt: time.Now().UTC(),
	}
	buildStateCacheMu.Lock()
	pendingBuildCache[buildPendingCacheKey(codeID, profile, tag, branch, core, platforms)] = pending
	buildStateCacheMu.Unlock()
	return pending
}

func getPendingBuild(codeID int, profile, tag, branch, core, platforms string) (PendingBuild, bool) {
	buildStateCacheMu.RLock()
	pending, ok := pendingBuildCache[buildPendingCacheKey(codeID, profile, tag, branch, core, platforms)]
	buildStateCacheMu.RUnlock()
	return pending, ok
}

func getLatestPendingBuildByProfile(codeID int, profile string) (PendingBuild, bool) {
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
		if !found || pending.TriggeredAt.After(latest.TriggeredAt) {
			latest = pending
			found = true
		}
	}
	return latest, found
}

func clearPendingBuild(codeID int, profile, tag, branch, core, platforms string) {
	buildStateCacheMu.Lock()
	delete(pendingBuildCache, buildPendingCacheKey(codeID, profile, tag, branch, core, platforms))
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
	if requestID != "" {
		return "request:" + requestID
	}
	return fmt.Sprintf("code:%d|profile:%s|tag:%s|branch:%s|core:%s|platforms:%s", codeID, profile, tag, branch, normalizeBuildCore(core), platforms)
}

func buildRequestMatches(inputs map[string]string, profile, requestID, tag, branch, core, platforms string) bool {
	if len(inputs) == 0 {
		return false
	}
	if requestID != "" {
		return strings.TrimSpace(inputs["request_id"]) == strings.TrimSpace(requestID)
	}
	if inputs["profile"] != profile {
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

func resolveBuildSourceRepo(core string) (string, string) {
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
	return claims.CodeID == record.CodeID
}

func buildRecordResponse(record BuildRecord) map[string]interface{} {
	releaseTag := buildReleaseTag(&record)
	return map[string]interface{}{
		"id":             record.ID,
		"code_id":        record.CodeID,
		"code_name":      record.CodeName,
		"request_id":     buildRecordRequestID(&record),
		"profile":        record.Profile,
		"tag":            record.Tag,
		"branch":         record.Branch,
		"core":           normalizeBuildCore(record.Core),
		"platforms":      record.Platforms,
		"run_id":         record.RunID,
		"run_url":        record.RunURL,
		"status":         record.Status,
		"conclusion":     record.Conclusion,
		"status_source":  record.StatusSource,
		"bound_at":       record.BoundAt,
		"finished_at":    record.FinishedAt,
		"last_sync_at":   record.LastSyncAt,
		"created_at":     record.CreatedAt,
		"updated_at":     record.UpdatedAt,
		"release_tag":    releaseTag,
		"download_ready": releaseTag != "" && record.Status == "completed" && record.Conclusion == "success",
	}
}

func buildRecordInputs(record *BuildRecord) map[string]string {
	if record == nil {
		return nil
	}
	return map[string]string{
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
		"found":         true,
		"record_id":     record.ID,
		"run_id":        record.RunID,
		"run_url":       record.RunURL,
		"status":        record.Status,
		"conclusion":    record.Conclusion,
		"status_source": record.StatusSource,
		"bound_at":      record.BoundAt,
		"finished_at":   record.FinishedAt,
		"last_sync_at":  record.LastSyncAt,
		"created_at":    record.CreatedAt,
		"updated_at":    record.UpdatedAt,
		"release_tag":   buildReleaseTag(record),
		"request_id":    buildRecordRequestID(record),
		"inputs":        buildRecordInputs(record),
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
	if pendingBuild, ok := getPendingBuild(record.CodeID, record.Profile, record.Tag, record.Branch, record.Core, record.Platforms); ok {
		if buildPendingMatchesRecordTime(record, pendingBuild) {
			return pendingBuild, true
		}
	}
	if createdAt, ok := parseDBTimestamp(record.CreatedAt); ok {
		return PendingBuild{
			CodeID:      record.CodeID,
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
	if len(inputs) > 0 && buildRequestMatches(inputs, record.Profile, buildRecordRequestID(record), record.Tag, record.Branch, record.Core, record.Platforms) {
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
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return nil, nil, nil
	}

	searchCounts := []int{fastWorkflowRunsSearchLimit, recentWorkflowRunsSearchLimit}
	for index, count := range searchCounts {
		if index > 0 && searchCounts[index-1] >= count {
			continue
		}
		runs, err := h.gh.GetRecentWorkflowRuns(cfg.BuildOwner, cfg.BuildRepo, "build.yaml", count)
		if err != nil {
			return nil, nil, err
		}

		for i := range runs {
			run := &runs[i]
			if activeOnly && !isActiveWorkflowStatus(run.Status) {
				continue
			}
			if extracted := extractBuildRequestIDFromText(run.Name); extracted == requestID {
				inputs, _ := h.gh.GetWorkflowRunInputs(cfg.BuildOwner, cfg.BuildRepo, run.ID)
				return run, inputs, nil
			}
			inputs, err := h.gh.GetWorkflowRunInputs(cfg.BuildOwner, cfg.BuildRepo, run.ID)
			if err != nil {
				continue
			}
			if strings.TrimSpace(inputs["request_id"]) == requestID {
				return run, inputs, nil
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

	run, _, err := h.findWorkflowRunByRequestID(buildRecordRequestID(record), true)
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
	if err := updateBuildRecordStatusExt(record.ID, run.ID, status, conclusion, "github", runURL, releaseTag); err != nil {
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
	if run.CreatedAt != "" {
		record.CreatedAt = run.CreatedAt
	}
	if run.UpdatedAt != "" {
		record.UpdatedAt = run.UpdatedAt
	}
	if run.Status == "completed" {
		clearPendingBuild(record.CodeID, record.Profile, record.Tag, record.Branch, record.Core, record.Platforms)
		deleteCachedProfileRunID(buildRunCacheKey(record.CodeID, record.Profile, buildRecordRequestID(record), record.Tag, record.Branch, record.Core, record.Platforms))
	} else {
		setCachedProfileRunID(buildRunCacheKey(record.CodeID, record.Profile, buildRecordRequestID(record), record.Tag, record.Branch, record.Core, record.Platforms), run.ID)
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

	runs, err := h.gh.GetRecentWorkflowRuns(cfg.BuildOwner, cfg.BuildRepo, "build.yaml", recentWorkflowRunsSearchLimit)
	if err != nil {
		return records
	}

	resolved := make(map[int]struct{})
	for i := range runs {
		run := &runs[i]
		inputs, err := h.gh.GetWorkflowRunInputs(cfg.BuildOwner, cfg.BuildRepo, run.ID)
		for _, item := range unresolved {
			if _, ok := resolved[item.index]; ok {
				continue
			}
			record := &records[item.index]
			matchesByInputs := err == nil && buildRequestMatches(inputs, record.Profile, item.requestID, record.Tag, record.Branch, record.Core, record.Platforms)
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

func (h *Handlers) cleanupOverflowBuildRecords(codeID int) {
	records, err := listOverflowBuildRecords(codeID, maxBuildRecordHistory)
	if err != nil {
		log.Printf("清理旧打包记录失败: code_id=%d err=%v", codeID, err)
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

	claims := getClaims(r)
	if !claims.canAccessProfile(req.Profile) {
		jsonError(w, "无权操作该档案", 403)
		return
	}

	if req.Platforms == "" {
		req.Platforms = "all"
	}
	core, err := validateBuildCore(req.Core)
	if err != nil {
		jsonError(w, err.Error(), 400)
		return
	}
	req.Core = core
	if req.Branch == "" {
		req.Branch = "main"
	}

	if _, err := expandRequestedBuildPlatforms(req.Core, req.Platforms); err != nil {
		jsonError(w, err.Error(), 400)
		return
	}
	demand := estimateBuildJobDemand(req.Core, req.Platforms)
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
		if ok, message := canUseBuildPlatforms(ac.AllowedPlatforms, req.Core, req.Platforms); !ok {
			jsonError(w, message, 403)
			return
		}
	}

	queueSnapshot, err := h.getBuildQueueSnapshot(req.Core, req.Platforms)
	if err == nil && !queueSnapshot.Available {
		jsonError(w, queueSnapshot.Message, 429)
		return
	}

	usageConsumed := false
	if claims.Permissions != "admin" {
		if err := consumeBuildUsage(claims.CodeID); err != nil {
			jsonError(w, err.Error(), 400)
			return
		}
		usageConsumed = true
	}

	if err := h.validateProfileYaml(req.Profile); err != nil {
		if usageConsumed {
			_ = rollbackBuildUsage(claims.CodeID)
		}
		jsonError(w, "配置档案无效: "+err.Error(), 400)
		return
	}

	record, err := createBuildRecord(claims.CodeID, claims.CodeName, req.Profile, req.Tag, req.Branch, req.Core, req.Platforms)
	if err != nil {
		if usageConsumed {
			_ = rollbackBuildUsage(claims.CodeID)
		}
		jsonError(w, "创建打包记录失败", 500)
		return
	}

	requestID := buildRecordRequestID(record)
	inputs := map[string]string{
		"profile":        req.Profile,
		"profile_branch": cfg.GithubProfileBranch,
		"tag":            req.Tag,
		"platforms":      req.Platforms,
		"branch":         req.Branch,
		"core":           req.Core,
		"request_id":     requestID,
	}

	err = h.gh.TriggerWorkflow(cfg.BuildOwner, cfg.BuildRepo, "build.yaml", inputs)
	if err != nil {
		if usageConsumed {
			_ = rollbackBuildUsage(claims.CodeID)
		}
		_ = updateBuildRecordStatusExt(record.ID, 0, "completed", "trigger_failed", "server", "", "")
		jsonError(w, err.Error(), 500)
		return
	}

	rememberPendingBuild(claims.CodeID, req.Profile, req.Tag, req.Branch, req.Core, req.Platforms)

	deleteCachedProfileRunID(buildRunCacheKey(claims.CodeID, req.Profile, requestID, req.Tag, req.Branch, req.Core, req.Platforms))
	deleteCachedProfileRunID(buildRunCacheKey(claims.CodeID, req.Profile, "", "", "", req.Core, ""))
	invalidateBuildCachesForRecord(record)

	logAudit(claims.CodeID, claims.CodeName, "trigger_build",
		fmt.Sprintf("record_id=%d profile=%s tag=%s core=%s platforms=%s branch=%s", record.ID, req.Profile, req.Tag, req.Core, req.Platforms, req.Branch),
		r.RemoteAddr)

	go h.cleanupOverflowBuildRecords(record.CodeID)

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
	if record == nil {
		return nil, fmt.Errorf("打包记录不存在")
	}

	status = strings.TrimSpace(status)
	if status == "" {
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

	if err := updateBuildRecordStatusExt(record.ID, runID, status, conclusion, statusSource, runURL, releaseTag); err != nil {
		return nil, err
	}

	cacheKey := buildRunCacheKey(record.CodeID, record.Profile, buildRecordRequestID(record), record.Tag, record.Branch, record.Core, record.Platforms)
	if status == "completed" {
		clearPendingBuild(record.CodeID, record.Profile, record.Tag, record.Branch, record.Core, record.Platforms)
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
		RequestID  string `json:"request_id"`
		RunID      int64  `json:"run_id"`
		RunURL     string `json:"run_url"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		ReleaseTag string `json:"release_tag"`
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
	record, err = h.persistBuildRecordEvent(record, req.RunID, status, req.Conclusion, "callback", req.RunURL, req.ReleaseTag)
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
		RequestID  string `json:"request_id"`
		RunID      int64  `json:"run_id"`
		RunURL     string `json:"run_url"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		ReleaseTag string `json:"release_tag"`
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
	record, err = h.persistBuildRecordEvent(record, req.RunID, status, req.Conclusion, "callback", req.RunURL, req.ReleaseTag)
	if err != nil {
		jsonError(w, "更新打包完成状态失败", 500)
		return
	}

	jsonResponse(w, map[string]interface{}{
		"message": "回写成功",
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
	if workflowPath != "" && !strings.HasSuffix(workflowPath, "build.yaml") {
		jsonResponse(w, map[string]interface{}{"message": "已忽略非 build.yaml 工作流"})
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
	content, _, _, exists, err := h.getStoredProfile(profileName)
	if err != nil {
		return fmt.Errorf("读取档案失败")
	}
	if !exists {
		return fmt.Errorf("未找到档案")
	}
	cleaned := normalizeSubscriptionConfig(content)
	if cleaned != content {
		filePath, err := profileFilePath(profileName)
		if err != nil {
			return fmt.Errorf("修复档案失败")
		}
		_ = h.profileGH.SaveFileWithRetry(filePath, func(_ string) string {
			return cleaned
		}, "修复配置档案: "+profileName, 3)
	}
	return validateYamlContent(cleaned)
}

func (h *Handlers) GetBuildHistory(w http.ResponseWriter, r *http.Request) {
	claims := getClaims(r)
	records, err := listBuildRecords(claims.CodeID, claims.Permissions == "admin", 100)
	if err != nil {
		jsonResponse(w, map[string]interface{}{})
		return
	}

	history := make(map[string]interface{})
	for _, record := range records {
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
	branch := strings.TrimSpace(r.URL.Query().Get("branch"))
	if branch == "" {
		branch = cfg.GithubBranch
	}

	commits, err := h.gh.ListRecentCommits(branch, limit)
	if err != nil {
		jsonError(w, "获取更新记录失败: "+err.Error(), 500)
		return
	}

	jsonResponse(w, map[string]interface{}{
		"repo":    cfg.GithubRepo,
		"branch":  branch,
		"limit":   limit,
		"commits": commits,
	})
}

func (h *Handlers) ListBranches(w http.ResponseWriter, r *http.Request) {
	core, err := validateBuildCore(r.URL.Query().Get("core"))
	if err != nil {
		jsonError(w, err.Error(), 400)
		return
	}
	owner, repo := resolveBuildSourceRepo(core)
	branches, err := h.gh.ListBranchesForRepo(owner, repo)
	if err != nil {
		jsonError(w, "获取分支列表失败", 500)
		return
	}
	jsonResponse(w, branches)
}

func (h *Handlers) GetBuildStatus(w http.ResponseWriter, r *http.Request) {
	claims := getClaims(r)
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
	if core == "" {
		core = "mihomo"
	}
	core, err := validateBuildCore(core)
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
			writeBuildStatusResponse(w, buildStatusCacheKey(record, 0, profile, requestID, tag, branch, core, platforms), result)
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
		pendingBuild, hasPendingBuild = getPendingBuild(pendingCodeID, profile, tag, branch, core, platforms)
	}
	if !hasPendingBuild && profile != "" && claims.Permissions == "admin" {
		if inferredPending, ok := getLatestPendingBuildByProfile(pendingCodeID, profile); ok {
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
			if platforms == "" {
				platforms = inferredPending.Platforms
			}
		}
	}
	cacheKey := buildRunCacheKey(pendingCodeID, profile, requestID, tag, branch, core, platforms)
	statusCacheKey = buildStatusCacheKey(record, pendingCodeID, profile, requestID, tag, branch, core, platforms)
	if cached, ok := buildStatusCache.get(statusCacheKey); ok {
		jsonResponse(w, cloneInterfaceMap(cached))
		return
	}

	var matchedRun *WorkflowRun
	var matchedInputs map[string]string

	if record != nil {
		if record.RunID > 0 {
			run, inputs, err := h.gh.GetWorkflowRun(cfg.BuildOwner, cfg.BuildRepo, record.RunID)
			if err == nil && run != nil && workflowRunMatchesRecord(record, run, inputs) {
				matchedRun = run
				matchedInputs = inputs
			}
		}
		if matchedRun == nil {
			run, inputs, err := h.findWorkflowRunByRequestID(requestID, false)
			if err != nil {
				result := buildStatusResponseFromRecord(record)
				result["sync_error"] = true
				writeBuildStatusResponse(w, statusCacheKey, result)
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
			writeBuildStatusResponse(w, statusCacheKey, result)
			return
		}

		h.applyWorkflowRunToBuildRecord(record, matchedRun)
		result := buildStatusResponseFromRecord(record)
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

		jobs, err := h.gh.GetWorkflowRunJobs(cfg.BuildOwner, cfg.BuildRepo, matchedRun.ID)
		if err == nil {
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
			result["jobs"] = jobList
		}

		writeBuildStatusResponse(w, statusCacheKey, result)
		return
	}

	if cachedRunID, ok := getCachedProfileRunID(cacheKey); ok {
		run, inputs, err := h.gh.GetWorkflowRun(cfg.BuildOwner, cfg.BuildRepo, cachedRunID)
		if err == nil {
			if buildRequestMatches(inputs, profile, requestID, tag, branch, core, platforms) || (requestID == "" && hasPendingBuild && matchRunByPendingBuild(run, pendingBuild)) {
				matchedRun = run
				matchedInputs = inputs
				if matchedRun.Status == "completed" {
					deleteCachedProfileRunID(cacheKey)
					clearPendingBuild(pendingCodeID, profile, tag, branch, core, platforms)
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
			run, inputs, err := h.findWorkflowRunByRequestID(requestID, false)
			if err != nil {
				jsonError(w, "查询构建状态失败", 500)
				return
			}
			if run != nil {
				matchedRun = run
				matchedInputs = inputs
				if matchedRun.Status == "completed" {
					clearPendingBuild(pendingCodeID, profile, tag, branch, core, platforms)
				} else {
					setCachedProfileRunID(cacheKey, run.ID)
				}
			}
		} else {
			runs, err := h.gh.GetRecentWorkflowRuns(cfg.BuildOwner, cfg.BuildRepo, "build.yaml", recentWorkflowRunsSearchLimit)
			if err != nil {
				jsonError(w, "查询构建状态失败", 500)
				return
			}

			for i := range runs {
				run := &runs[i]
				if !isActiveWorkflowStatus(run.Status) {
					continue
				}
				inputs, err := h.gh.GetWorkflowRunInputs(cfg.BuildOwner, cfg.BuildRepo, run.ID)
				matchesByInputs := err == nil && buildRequestMatches(inputs, profile, requestID, tag, branch, core, platforms)
				matchesByPending := hasPendingBuild && matchRunByPendingBuild(run, pendingBuild)
				if matchesByInputs || matchesByPending {
					matchedRun = run
					if err == nil {
						matchedInputs = inputs
					}
					setCachedProfileRunID(cacheKey, run.ID)
					break
				}
			}

			if matchedRun == nil {
				for i := range runs {
					run := &runs[i]
					if run.Status != "completed" {
						continue
					}
					inputs, err := h.gh.GetWorkflowRunInputs(cfg.BuildOwner, cfg.BuildRepo, run.ID)
					matchesByInputs := err == nil && buildRequestMatches(inputs, profile, requestID, tag, branch, core, platforms)
					if matchesByInputs {
						matchedRun = run
						if err == nil {
							matchedInputs = inputs
						}
						clearPendingBuild(pendingCodeID, profile, tag, branch, core, platforms)
						break
					}
				}
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
		writeBuildStatusResponse(w, statusCacheKey, result)
		return
	}

	conclusion := ""
	if matchedRun.Conclusion != nil {
		conclusion = *matchedRun.Conclusion
	}
	result := map[string]interface{}{
		"found":      true,
		"record_id":  recordID,
		"run_id":     matchedRun.ID,
		"run_url":    matchedRun.HTMLURL,
		"status":     matchedRun.Status,
		"conclusion": conclusion,
		"created_at": matchedRun.CreatedAt,
		"updated_at": matchedRun.UpdatedAt,
		"inputs": map[string]string{
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

	jobs, err := h.gh.GetWorkflowRunJobs(cfg.BuildOwner, cfg.BuildRepo, matchedRun.ID)
	if err == nil {
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
		result["jobs"] = jobList
	}

	writeBuildStatusResponse(w, statusCacheKey, result)
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
	core, err := validateBuildCore(r.URL.Query().Get("core"))
	if err != nil {
		jsonError(w, err.Error(), 400)
		return
	}
	platforms := strings.TrimSpace(r.URL.Query().Get("platforms"))
	if _, err := expandRequestedBuildPlatforms(core, platforms); err != nil {
		jsonError(w, err.Error(), 400)
		return
	}
	queueSnapshot, err := h.getBuildQueueSnapshot(core, platforms)
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
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	records, err := listBuildRecords(claims.CodeID, claims.Permissions == "admin", limit)
	if err != nil {
		jsonError(w, "获取打包记录失败", 500)
		return
	}
	records = h.reconcileBuildRecords(records, false)

	items := make([]map[string]interface{}, 0, len(records))
	for _, record := range records {
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

	deleteCachedProfileRunID(buildRunCacheKey(record.CodeID, record.Profile, buildRecordRequestID(record), record.Tag, record.Branch, record.Core, record.Platforms))
	clearPendingBuild(record.CodeID, record.Profile, record.Tag, record.Branch, record.Core, record.Platforms)
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

func (h *Handlers) CreateCode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name             string   `json:"name"`
		MaxUses          int      `json:"max_uses"`
		AllowedProfiles  []string `json:"allowed_profiles"`
		AllowedPlatforms []string `json:"allowed_platforms"`
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

	ac, err := createCode(req.Name, req.MaxUses, req.AllowedProfiles, allowedPlatforms, req.ExpiresAt)
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

	jsonResponse(w, ac)
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
		AllowedPlatforms []string `json:"allowed_platforms"`
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

	if err := updateCode(id, req.Name, req.MaxUses, req.UsedCount, req.AllowedProfiles, allowedPlatforms, req.ExpiresAt); err != nil {
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

	jsonResponse(w, map[string]string{"message": "更新成功"})
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

func (h *Handlers) RenameProfile(w http.ResponseWriter, r *http.Request) {
	oldName := strings.TrimSpace(chi.URLParam(r, "name"))
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

	content, sha, _, exists, err := h.getStoredProfile(oldName)
	if err != nil {
		jsonError(w, "读取原档案失败: "+err.Error(), 500)
		return
	}
	if !exists {
		jsonError(w, "原档案不存在", 404)
		return
	}
	_, _, _, newExists, err := h.getStoredProfile(newName)
	if err != nil {
		jsonError(w, "检查新档案名称失败: "+err.Error(), 500)
		return
	}
	if newExists {
		jsonError(w, "新档案名称已存在", 409)
		return
	}

	if _, err := h.profileGH.SaveFile(newPath, content, "", "重命名配置档案: "+oldName+" -> "+newName); err != nil {
		jsonError(w, "创建新档案失败: "+err.Error(), 500)
		return
	}
	if err := h.profileGH.DeleteFile(oldPath, sha, "删除重命名前旧档案: "+oldName); err != nil {
		jsonError(w, "删除旧档案失败，新档案已创建，请稍后手动清理旧档案: "+err.Error(), 500)
		return
	}
	if err := renameProfileReferences(oldName, newName); err != nil {
		jsonError(w, "同步档案引用失败: "+err.Error(), 500)
		return
	}

	invalidateProfileCache()
	buildStatusCache.clear()
	buildQueueSnapshotCache.clear()

	claims := getClaims(r)
	logAudit(claims.CodeID, claims.CodeName, "rename_profile", fmt.Sprintf("%s -> %s", oldName, newName), r.RemoteAddr)

	jsonResponse(w, map[string]string{
		"message":  "重命名成功",
		"old_name": oldName,
		"new_name": newName,
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
