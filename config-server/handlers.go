package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

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

	token, err := generateJWT(ac.ID, ac.Name, ac.Permissions, ac.AllowedProfiles)
	if err != nil {
		jsonError(w, "生成 Token 失败", 500)
		return
	}

	logAudit(ac.ID, ac.Name, "login", "", r.RemoteAddr)

	jsonResponse(w, map[string]interface{}{
		"token":       token,
		"name":        ac.Name,
		"permissions": ac.Permissions,
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

// 缓存 profile 对应的最近 run_id，避免每次轮询都搜索所有 runs
var profileRunCache = make(map[string]int64)

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
		jsonError(w, fmt.Sprintf("当前有 %d 个构建正在运行，最多允许 %d 个并发构建，请稍后再试", running, maxConcurrentBuilds), 429)
		return
	}

	inputs := map[string]string{
		"profile":   req.Profile,
		"tag":       req.Tag,
		"platforms": req.Platforms,
		"branch":    req.Branch,
	}

	err = h.gh.TriggerWorkflow(cfg.BuildOwner, cfg.BuildRepo, "build.yaml", inputs)
	if err != nil {
		jsonError(w, err.Error(), 500)
		return
	}

	// 清除旧的 run 缓存，下次轮询会重新搜索新的 run
	delete(profileRunCache, req.Profile)

	go h.saveBuildHistory(req.Profile, req.Tag, req.Platforms)

	logAudit(claims.CodeID, claims.CodeName, "trigger_build",
		fmt.Sprintf("profile=%s tag=%s platforms=%s branch=%s", req.Profile, req.Tag, req.Platforms, req.Branch),
		r.RemoteAddr)

	jsonResponse(w, map[string]string{"message": "构建已触发"})
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

func (h *Handlers) ListBranches(w http.ResponseWriter, r *http.Request) {
	branches, err := h.gh.ListBranches()
	if err != nil {
		jsonError(w, "获取分支列表失败", 500)
		return
	}
	jsonResponse(w, branches)
}

func (h *Handlers) GetBuildStatus(w http.ResponseWriter, r *http.Request) {
	profile := r.URL.Query().Get("profile")
	if profile == "" {
		jsonError(w, "缺少 profile 参数", 400)
		return
	}

	var matchedRun *WorkflowRun
	var matchedInputs map[string]string

	// 1. 先检查缓存的 run_id，直接查状态（只需 1 次 API 调用）
	if cachedRunID, ok := profileRunCache[profile]; ok {
		run, inputs, err := h.gh.GetWorkflowRun(cfg.BuildOwner, cfg.BuildRepo, cachedRunID)
		if err == nil && inputs["profile"] == profile {
			matchedRun = run
			matchedInputs = inputs
			if matchedRun.Status == "completed" {
				delete(profileRunCache, profile)
			}
		} else {
			delete(profileRunCache, profile)
		}
	}

	// 2. 缓存没有命中，搜索最近的 runs
	if matchedRun == nil {
		runs, err := h.gh.GetRecentWorkflowRuns(cfg.BuildOwner, cfg.BuildRepo, "build.yaml", 10)
		if err != nil {
			jsonError(w, "查询构建状态失败", 500)
			return
		}

		// 优先找正在运行或排队的
		for i := range runs {
			run := &runs[i]
			if run.Status == "in_progress" || run.Status == "queued" {
				inputs, err := h.gh.GetWorkflowRunInputs(cfg.BuildOwner, cfg.BuildRepo, run.ID)
				if err == nil && inputs["profile"] == profile {
					matchedRun = run
					matchedInputs = inputs
					profileRunCache[profile] = run.ID
					break
				}
			}
		}

		// 没有活跃的，找最近完成的
		if matchedRun == nil {
			for i := range runs {
				run := &runs[i]
				if run.Status == "completed" {
					inputs, err := h.gh.GetWorkflowRunInputs(cfg.BuildOwner, cfg.BuildRepo, run.ID)
					if err == nil && inputs["profile"] == profile {
						matchedRun = run
						matchedInputs = inputs
						break
					}
				}
			}
		}
	}

	if matchedRun == nil {
		jsonResponse(w, map[string]interface{}{
			"found": false,
		})
		return
	}

	conclusion := ""
	if matchedRun.Conclusion != nil {
		conclusion = *matchedRun.Conclusion
	}

	result := map[string]interface{}{
		"found":      true,
		"run_id":     matchedRun.ID,
		"status":     matchedRun.Status,
		"conclusion": conclusion,
		"html_url":   matchedRun.HTMLURL,
		"created_at": matchedRun.CreatedAt,
		"updated_at": matchedRun.UpdatedAt,
	}

	// 返回构建参数，让前端展示确认是哪次构建
	if matchedInputs != nil {
		result["inputs"] = matchedInputs
	}

	// 获取 job 详情
	jobs, err := h.gh.GetWorkflowRunJobs(cfg.BuildOwner, cfg.BuildRepo, matchedRun.ID)
	if err == nil {
		jobList := []map[string]interface{}{}
		for _, job := range jobs {
			jobConclusion := ""
			if job.Conclusion != nil {
				jobConclusion = *job.Conclusion
			}
			jobList = append(jobList, map[string]interface{}{
				"name":       job.Name,
				"status":     job.Status,
				"conclusion": jobConclusion,
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
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "请求格式错误", 400)
		return
	}

	if req.MaxUses == 0 {
		req.MaxUses = -1
	}

	ac, err := createCode(req.Name, req.MaxUses, req.AllowedProfiles)
	if err != nil {
		jsonError(w, "创建激活码失败: "+err.Error(), 500)
		return
	}

	claims := getClaims(r)
	logAudit(claims.CodeID, claims.CodeName, "create_code", ac.Name, r.RemoteAddr)

	jsonResponse(w, ac)
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
