package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

const maxBuildRecordHistory = 5

type Handlers struct {
	gh *GitHubClient
}

func NewHandlers(gh *GitHubClient) *Handlers {
	return &Handlers{gh: gh}
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
			"name":              claims.CodeName,
			"permissions":       claims.Permissions,
			"max_uses":          -1,
			"used_count":        0,
			"remaining_uses":    -1,
			"can_build":         true,
			"build_status_text": "管理员不限",
			"expires_at":        nil,
			"is_active":         true,
		})
		return
	}

	ac, err := getActivationCodeByID(claims.CodeID)
	if err != nil {
		jsonResponse(w, map[string]interface{}{
			"name":              claims.CodeName,
			"permissions":       "user",
			"max_uses":          0,
			"used_count":        0,
			"remaining_uses":    0,
			"can_build":         false,
			"build_status_text": err.Error(),
			"expires_at":        nil,
			"is_active":         false,
		})
		return
	}

	canBuild, statusText := getBuildAvailability(ac)
	jsonResponse(w, map[string]interface{}{
		"name":              ac.Name,
		"permissions":       "user",
		"max_uses":          ac.MaxUses,
		"used_count":        ac.UsedCount,
		"remaining_uses":    getRemainingBuildUses(ac),
		"can_build":         canBuild,
		"build_status_text": statusText,
		"expires_at":        ac.ExpiresAt,
		"is_active":         ac.IsActive,
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

const profilesPath = "config-profiles.json"

func (h *Handlers) ListProfiles(w http.ResponseWriter, r *http.Request) {
	content, _, _ := h.gh.GetFile(profilesPath)
	var profiles map[string]interface{}
	if content != "" {
		json.Unmarshal([]byte(content), &profiles)
	}
	if profiles == nil {
		profiles = make(map[string]interface{})
	}

	claims := getClaims(r)
	list := []map[string]string{}

	if claims.Permissions == "admin" || len(claims.AllowedProfiles) == 0 {
		// 管理员：返回所有已有档案
		for name, data := range profiles {
			item := map[string]string{"name": name}
			if m, ok := data.(map[string]interface{}); ok {
				if lu, ok := m["last_updated"].(string); ok {
					item["last_updated"] = lu
				}
			}
			list = append(list, item)
		}
	} else {
		// 普通用户：始终返回 allowed_profiles 中的所有名称，不管是否已创建
		for _, name := range claims.AllowedProfiles {
			item := map[string]string{"name": name}
			if data, exists := profiles[name]; exists {
				if m, ok := data.(map[string]interface{}); ok {
					if lu, ok := m["last_updated"].(string); ok {
						item["last_updated"] = lu
					}
				}
			}
			list = append(list, item)
		}
	}

	jsonResponse(w, map[string]interface{}{"profiles": list})
}

func (h *Handlers) GetProfile(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	claims := getClaims(r)

	if !claims.canAccessProfile(name) {
		jsonError(w, "无权访问该档案", 403)
		return
	}

	content, _, err := h.gh.GetFile(profilesPath)
	if err != nil {
		// 文件不存在，返回空档案让用户从零开始
		logAudit(claims.CodeID, claims.CodeName, "load_profile", name+" (新建)", r.RemoteAddr)
		jsonResponse(w, map[string]interface{}{"yaml_content": "", "is_new": true})
		return
	}

	var profiles map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &profiles); err != nil {
		jsonError(w, "解析档案数据失败", 500)
		return
	}

	profileData, ok := profiles[name]
	if !ok {
		// 档案名称存在于 allowed_profiles 但还没创建过，返回空内容
		logAudit(claims.CodeID, claims.CodeName, "load_profile", name+" (新建)", r.RemoteAddr)
		jsonResponse(w, map[string]interface{}{"yaml_content": "", "is_new": true})
		return
	}

	logAudit(claims.CodeID, claims.CodeName, "load_profile", name, r.RemoteAddr)

	w.Header().Set("Content-Type", "application/json")
	w.Write(profileData)
}

func (h *Handlers) SaveProfile(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	claims := getClaims(r)

	if !claims.canAccessProfile(name) {
		jsonError(w, "无权操作该档案", 403)
		return
	}

	var req struct {
		YamlContent string `json:"yaml_content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "请求格式错误", 400)
		return
	}
	if claims.Permissions != "admin" {
		req.YamlContent = bindProfileTitle(req.YamlContent, name)
	}

	content, sha, err := h.gh.GetFile(profilesPath)
	var profiles map[string]interface{}
	if err != nil {
		profiles = make(map[string]interface{})
		sha = ""
	} else {
		if err := json.Unmarshal([]byte(content), &profiles); err != nil {
			profiles = make(map[string]interface{})
		}
	}

	profiles[name] = map[string]interface{}{
		"yaml_content": req.YamlContent,
		"last_updated": time.Now().Format("2006/1/2 15:04:05"),
	}

	newContent, _ := json.MarshalIndent(profiles, "", "  ")
	_, err = h.gh.SaveFile(profilesPath, string(newContent), sha, "保存配置档案: "+name)
	if err != nil {
		jsonError(w, "保存失败: "+err.Error(), 500)
		return
	}

	logAudit(claims.CodeID, claims.CodeName, "save_profile", name, r.RemoteAddr)

	jsonResponse(w, map[string]string{"message": "保存成功"})
}

func (h *Handlers) DeleteProfile(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	claims := getClaims(r)

	if !claims.canAccessProfile(name) {
		jsonError(w, "无权操作该档案", 403)
		return
	}

	content, sha, err := h.gh.GetFile(profilesPath)
	if err != nil {
		jsonError(w, "加载档案失败", 500)
		return
	}

	var profiles map[string]interface{}
	if err := json.Unmarshal([]byte(content), &profiles); err != nil {
		jsonError(w, "解析档案数据失败", 500)
		return
	}

	delete(profiles, name)

	newContent, _ := json.MarshalIndent(profiles, "", "  ")
	_, err = h.gh.SaveFile(profilesPath, string(newContent), sha, "删除配置档案: "+name)
	if err != nil {
		jsonError(w, "删除失败: "+err.Error(), 500)
		return
	}

	logAudit(claims.CodeID, claims.CodeName, "delete_profile", name, r.RemoteAddr)

	jsonResponse(w, map[string]string{"message": "删除成功"})
}

// ==================== 构建 ====================

const buildHistoryPath = "build-versions.json"

const maxConcurrentBuilds = 3

const recentWorkflowRunsSearchLimit = 30

type PendingBuild struct {
	CodeID      int
	Profile     string
	Tag         string
	Branch      string
	Platforms   string
	TriggeredAt time.Time
}

// 缓存构建请求对应的最近 run_id，避免每次轮询都搜索所有 runs
var profileRunCache = make(map[string]int64)
var pendingBuildCache = make(map[string]PendingBuild)

func buildPendingCacheKey(codeID int, profile, tag, branch, platforms string) string {
	return fmt.Sprintf("code:%d|profile:%s|tag:%s|branch:%s|platforms:%s", codeID, profile, tag, branch, platforms)
}

func rememberPendingBuild(codeID int, profile, tag, branch, platforms string) PendingBuild {
	pending := PendingBuild{
		CodeID:      codeID,
		Profile:     profile,
		Tag:         tag,
		Branch:      branch,
		Platforms:   platforms,
		TriggeredAt: time.Now().UTC(),
	}
	pendingBuildCache[buildPendingCacheKey(codeID, profile, tag, branch, platforms)] = pending
	return pending
}

func getPendingBuild(codeID int, profile, tag, branch, platforms string) (PendingBuild, bool) {
	pending, ok := pendingBuildCache[buildPendingCacheKey(codeID, profile, tag, branch, platforms)]
	return pending, ok
}

func getLatestPendingBuildByProfile(codeID int, profile string) (PendingBuild, bool) {
	var latest PendingBuild
	found := false
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

func clearPendingBuild(codeID int, profile, tag, branch, platforms string) {
	delete(pendingBuildCache, buildPendingCacheKey(codeID, profile, tag, branch, platforms))
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

func buildRunCacheKey(codeID int, profile, requestID, tag, branch, platforms string) string {
	if requestID != "" {
		return "request:" + requestID
	}
	return fmt.Sprintf("code:%d|profile:%s|tag:%s|branch:%s|platforms:%s", codeID, profile, tag, branch, platforms)
}

func buildRequestMatches(inputs map[string]string, profile, requestID, tag, branch, platforms string) bool {
	if len(inputs) == 0 {
		return false
	}
	if requestID != "" && inputs["request_id"] != "" {
		return inputs["request_id"] == requestID
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
	if platforms != "" && inputs["platforms"] != platforms {
		return false
	}
	return true
}

func isActiveWorkflowStatus(status string) bool {
	return status != "" && status != "completed"
}

func buildReleaseTag(record *BuildRecord) string {
	if record == nil || record.RunID <= 0 {
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
		"profile":        record.Profile,
		"tag":            record.Tag,
		"branch":         record.Branch,
		"platforms":      record.Platforms,
		"run_id":         record.RunID,
		"status":         record.Status,
		"conclusion":     record.Conclusion,
		"created_at":     record.CreatedAt,
		"updated_at":     record.UpdatedAt,
		"release_tag":    releaseTag,
		"download_ready": record.RunID > 0 && record.Status == "completed" && record.Conclusion == "success",
	}
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
	if req.Branch == "" {
		req.Branch = "main"
	}

	running, err := h.gh.GetRunningWorkflowCount(cfg.BuildOwner, cfg.BuildRepo, "build.yaml")
	if err == nil && running >= maxConcurrentBuilds {
		jsonError(w, fmt.Sprintf("当前有 %d 个打包任务正在运行，最多允许 %d 个并发打包，请稍后再试", running, maxConcurrentBuilds), 429)
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

	record, err := createBuildRecord(claims.CodeID, claims.CodeName, req.Profile, req.Tag, req.Branch, req.Platforms)
	if err != nil {
		if usageConsumed {
			_ = rollbackBuildUsage(claims.CodeID)
		}
		jsonError(w, "创建打包记录失败", 500)
		return
	}

	requestID := strconv.FormatInt(record.ID, 10)
	inputs := map[string]string{
		"profile":    req.Profile,
		"tag":        req.Tag,
		"platforms":  req.Platforms,
		"branch":     req.Branch,
		"request_id": requestID,
	}

	err = h.gh.TriggerWorkflow(cfg.BuildOwner, cfg.BuildRepo, "build.yaml", inputs)
	if err != nil {
		if usageConsumed {
			_ = rollbackBuildUsage(claims.CodeID)
		}
		updateBuildRecordStatus(record.ID, 0, "completed", "trigger_failed")
		jsonError(w, err.Error(), 500)
		return
	}

	rememberPendingBuild(claims.CodeID, req.Profile, req.Tag, req.Branch, req.Platforms)

	delete(profileRunCache, buildRunCacheKey(claims.CodeID, req.Profile, "", req.Tag, req.Branch, req.Platforms))
	delete(profileRunCache, buildRunCacheKey(claims.CodeID, req.Profile, "", "", "", ""))

	go h.saveBuildHistory(req.Profile, req.Tag, req.Platforms)

	logAudit(claims.CodeID, claims.CodeName, "trigger_build",
		fmt.Sprintf("record_id=%d profile=%s tag=%s platforms=%s branch=%s", record.ID, req.Profile, req.Tag, req.Platforms, req.Branch),
		r.RemoteAddr)

	go h.cleanupOverflowBuildRecords(record.CodeID)

	jsonResponse(w, map[string]interface{}{
		"message":    "打包已提交",
		"record_id":  record.ID,
		"request_id": requestID,
	})
}

func (h *Handlers) saveBuildHistory(profile, tag, platforms string) {
	buildTime := time.Now().Format("2006/1/2 15:04:05")
	err := h.gh.SaveFileWithRetry(buildHistoryPath, func(existing string) string {
		var history map[string]interface{}
		if existing != "" {
			json.Unmarshal([]byte(existing), &history)
		}
		if history == nil {
			history = make(map[string]interface{})
		}
		history[profile] = map[string]interface{}{
			"version":   tag,
			"platforms": platforms,
			"time":      buildTime,
		}
		newContent, _ := json.MarshalIndent(history, "", "  ")
		return string(newContent)
	}, "更新打包版本记录: "+profile+" "+tag, 5)
	if err != nil {
		fmt.Printf("保存构建历史失败: %v\n", err)
	}
}

func (h *Handlers) GetBuildHistory(w http.ResponseWriter, r *http.Request) {
	content, _, err := h.gh.GetFile(buildHistoryPath)
	if err != nil {
		jsonResponse(w, map[string]interface{}{})
		return
	}

	var history map[string]interface{}
	json.Unmarshal([]byte(content), &history)
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
	branches, err := h.gh.ListBranches()
	if err != nil {
		jsonError(w, "获取分支列表失败", 500)
		return
	}
	jsonResponse(w, branches)
}

func (h *Handlers) GetBuildStatus(w http.ResponseWriter, r *http.Request) {
	claims := getClaims(r)
	profile := r.URL.Query().Get("profile")
	requestID := r.URL.Query().Get("request_id")
	tag := r.URL.Query().Get("tag")
	branch := r.URL.Query().Get("branch")
	platforms := r.URL.Query().Get("platforms")

	recordID, _ := strconv.ParseInt(r.URL.Query().Get("record_id"), 10, 64)
	if recordID == 0 && requestID != "" {
		parsedRecordID, err := strconv.ParseInt(requestID, 10, 64)
		if err != nil {
			jsonError(w, "request_id 参数无效", 400)
			return
		}
		recordID = parsedRecordID
	}

	var record *BuildRecord
	if recordID > 0 {
		loadedRecord, err := getBuildRecord(recordID)
		if err != nil {
			jsonError(w, "打包记录不存在", 404)
			return
		}
		if !canAccessBuildRecord(claims, loadedRecord) {
			jsonError(w, "无权访问该打包记录", 403)
			return
		}
		record = loadedRecord
		requestID = strconv.FormatInt(loadedRecord.ID, 10)
		if profile == "" {
			profile = loadedRecord.Profile
		}
		if tag == "" {
			tag = loadedRecord.Tag
		}
		if branch == "" {
			branch = loadedRecord.Branch
		}
		if platforms == "" {
			platforms = loadedRecord.Platforms
		}
	}

	if record == nil && claims.Permissions != "admin" {
		jsonError(w, "普通用户查询打包状态必须提供自己的 record_id 或 request_id", 400)
		return
	}
	if record == nil && profile != "" && !claims.canAccessProfile(profile) {
		jsonError(w, "无权查看该档案的打包状态", 403)
		return
	}

	if profile == "" && requestID == "" {
		jsonError(w, "缺少 profile、request_id 或 record_id 参数", 400)
		return
	}

	pendingCodeID := 0
	if record != nil {
		pendingCodeID = record.CodeID
	} else if claims.Permissions != "admin" {
		pendingCodeID = claims.CodeID
	}
	pendingBuild, hasPendingBuild := getPendingBuild(pendingCodeID, profile, tag, branch, platforms)
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
			if platforms == "" {
				platforms = inferredPending.Platforms
			}
		}
	}
	cacheKey := buildRunCacheKey(pendingCodeID, profile, requestID, tag, branch, platforms)

	var matchedRun *WorkflowRun
	var matchedInputs map[string]string

	if record != nil && record.RunID > 0 {
		run, inputs, err := h.gh.GetWorkflowRun(cfg.BuildOwner, cfg.BuildRepo, record.RunID)
		if err == nil {
			matchedRun = run
			matchedInputs = inputs
			profileRunCache[cacheKey] = run.ID
			if matchedRun.Status == "completed" {
				delete(profileRunCache, cacheKey)
				clearPendingBuild(pendingCodeID, profile, tag, branch, platforms)
			}
		}
	}

	if matchedRun == nil {
		if cachedRunID, ok := profileRunCache[cacheKey]; ok {
			run, inputs, err := h.gh.GetWorkflowRun(cfg.BuildOwner, cfg.BuildRepo, cachedRunID)
			if err == nil {
				if buildRequestMatches(inputs, profile, requestID, tag, branch, platforms) || (hasPendingBuild && matchRunByPendingBuild(run, pendingBuild)) {
					matchedRun = run
					matchedInputs = inputs
					if matchedRun.Status == "completed" {
						delete(profileRunCache, cacheKey)
						clearPendingBuild(pendingCodeID, profile, tag, branch, platforms)
					}
				} else {
					delete(profileRunCache, cacheKey)
				}
			} else {
				delete(profileRunCache, cacheKey)
			}
		}
	}

	if matchedRun == nil {
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
			matchesByInputs := err == nil && buildRequestMatches(inputs, profile, requestID, tag, branch, platforms)
			matchesByPending := hasPendingBuild && matchRunByPendingBuild(run, pendingBuild)
			if matchesByInputs || matchesByPending {
				matchedRun = run
				if err == nil {
					matchedInputs = inputs
				}
				profileRunCache[cacheKey] = run.ID
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
				matchesByInputs := err == nil && buildRequestMatches(inputs, profile, requestID, tag, branch, platforms)
				matchesByPending := hasPendingBuild && matchRunByPendingBuild(run, pendingBuild)
				if matchesByInputs || matchesByPending {
					matchedRun = run
					if err == nil {
						matchedInputs = inputs
					}
					clearPendingBuild(pendingCodeID, profile, tag, branch, platforms)
					break
				}
			}
		}
	}

	if matchedRun == nil {
		if record != nil && record.RunID == 0 && record.Status == "completed" && record.Conclusion == "trigger_failed" {
			fallbackInputs := map[string]string{
				"profile":    record.Profile,
				"tag":        record.Tag,
				"branch":     record.Branch,
				"platforms":  record.Platforms,
				"request_id": strconv.FormatInt(record.ID, 10),
			}
			jsonResponse(w, map[string]interface{}{
				"found":      true,
				"record_id":  record.ID,
				"run_id":     record.RunID,
				"status":     record.Status,
				"conclusion": record.Conclusion,
				"created_at": record.CreatedAt,
				"updated_at": record.UpdatedAt,
				"request_id": fallbackInputs["request_id"],
				"inputs":     fallbackInputs,
			})
			return
		}
		if recordID > 0 {
			updateBuildRecordStatus(recordID, 0, "queued", "")
		}
		jsonResponse(w, map[string]interface{}{
			"found":     false,
			"record_id": recordID,
		})
		return
	}

	conclusion := ""
	if matchedRun.Conclusion != nil {
		conclusion = *matchedRun.Conclusion
	}
	if recordID > 0 {
		updateBuildRecordStatus(recordID, matchedRun.ID, matchedRun.Status, conclusion)
	}

	result := map[string]interface{}{
		"found":      true,
		"record_id":  recordID,
		"run_id":     matchedRun.ID,
		"status":     matchedRun.Status,
		"conclusion": conclusion,
		"created_at": matchedRun.CreatedAt,
		"updated_at": matchedRun.UpdatedAt,
	}

	if matchedInputs != nil && len(matchedInputs) > 0 {
		result["inputs"] = matchedInputs
		if matchedInputs["request_id"] != "" {
			result["request_id"] = matchedInputs["request_id"]
		}
	} else {
		fallbackInputs := map[string]string{}
		if profile != "" {
			fallbackInputs["profile"] = profile
		}
		if tag != "" {
			fallbackInputs["tag"] = tag
		}
		if branch != "" {
			fallbackInputs["branch"] = branch
		}
		if platforms != "" {
			fallbackInputs["platforms"] = platforms
		}
		if requestID != "" {
			fallbackInputs["request_id"] = requestID
		}
		if record != nil {
			if fallbackInputs["profile"] == "" {
				fallbackInputs["profile"] = record.Profile
			}
			if fallbackInputs["tag"] == "" {
				fallbackInputs["tag"] = record.Tag
			}
			if fallbackInputs["branch"] == "" {
				fallbackInputs["branch"] = record.Branch
			}
			if fallbackInputs["platforms"] == "" {
				fallbackInputs["platforms"] = record.Platforms
			}
		}
		if len(fallbackInputs) > 0 {
			result["inputs"] = fallbackInputs
		}
		if requestID != "" {
			result["request_id"] = requestID
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

	jsonResponse(w, result)
}

func (h *Handlers) GetBuildQueue(w http.ResponseWriter, r *http.Request) {
	running, err := h.gh.GetRunningWorkflowCount(cfg.BuildOwner, cfg.BuildRepo, "build.yaml")
	if err != nil {
		jsonResponse(w, map[string]interface{}{
			"running":   0,
			"max":       maxConcurrentBuilds,
			"available": true,
		})
		return
	}
	jsonResponse(w, map[string]interface{}{
		"running":   running,
		"max":       maxConcurrentBuilds,
		"available": running < maxConcurrentBuilds,
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
	if isActiveWorkflowStatus(record.Status) {
		jsonError(w, "正在打包的记录暂不支持删除", 409)
		return
	}

	releaseTag := buildReleaseTag(record)
	if err := h.deleteBuildRecordRelease(record); err != nil {
		jsonError(w, "删除 GitHub 打包产物失败", 500)
		return
	}
	if err := deleteBuildRecord(record.ID); err != nil {
		jsonError(w, "删除打包记录失败", 500)
		return
	}

	delete(profileRunCache, buildRunCacheKey(record.CodeID, record.Profile, strconv.FormatInt(record.ID, 10), record.Tag, record.Branch, record.Platforms))
	clearPendingBuild(record.CodeID, record.Profile, record.Tag, record.Branch, record.Platforms)

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

	releaseTag := buildReleaseTag(record)
	if releaseTag == "" {
		jsonError(w, "当前打包尚未生成可下载产物", 404)
		return
	}

	release, err := h.gh.GetReleaseByTag(cfg.BuildOwner, cfg.BuildRepo, releaseTag)
	if err != nil {
		jsonError(w, "获取打包产物失败", 500)
		return
	}
	if release == nil {
		jsonError(w, "当前打包尚未生成可下载产物", 404)
		return
	}

	var matchedAsset *ReleaseAsset
	for i := range release.Assets {
		if release.Assets[i].ID == assetID {
			matchedAsset = &release.Assets[i]
			break
		}
	}
	if matchedAsset == nil {
		jsonError(w, "打包产物不存在", 404)
		return
	}

	resp, err := h.gh.DownloadReleaseAsset(cfg.BuildOwner, cfg.BuildRepo, matchedAsset.ID)
	if err != nil {
		jsonError(w, "下载打包产物失败", 500)
		return
	}
	defer resp.Body.Close()

	contentType := matchedAsset.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", matchedAsset.Name))
	w.Header().Set("Cache-Control", "private, no-store, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if matchedAsset.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(matchedAsset.Size, 10))
	}
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, resp.Body); err != nil {
		return
	}

	logAudit(claims.CodeID, claims.CodeName, "download_build_asset",
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
		Name            string   `json:"name"`
		MaxUses         int      `json:"max_uses"`
		AllowedProfiles []string `json:"allowed_profiles"`
		ExpiresAt       string   `json:"expires_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "请求格式错误", 400)
		return
	}
	if req.MaxUses < -1 {
		jsonError(w, "可打包次数不能小于 -1", 400)
		return
	}

	ac, err := createCode(req.Name, req.MaxUses, req.AllowedProfiles, req.ExpiresAt)
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
		Name            string   `json:"name"`
		MaxUses         int      `json:"max_uses"`
		UsedCount       int      `json:"used_count"`
		AllowedProfiles []string `json:"allowed_profiles"`
		ExpiresAt       string   `json:"expires_at"`
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

	if err := updateCode(id, req.Name, req.MaxUses, req.UsedCount, req.AllowedProfiles, req.ExpiresAt); err != nil {
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
		"client_updates_limit": getClientUpdatesLimit(),
	})
}

func (h *Handlers) SaveSystemSettings(w http.ResponseWriter, r *http.Request) {
	claims := getClaims(r)
	var req struct {
		ClientUpdatesLimit int `json:"client_updates_limit"`
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

	logAudit(claims.CodeID, claims.CodeName, "update_settings", fmt.Sprintf("client_updates_limit=%d", limit), r.RemoteAddr)
	jsonResponse(w, map[string]interface{}{
		"message":              "设置已保存",
		"client_updates_limit": limit,
	})
}
