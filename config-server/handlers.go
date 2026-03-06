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

	inputs := map[string]string{
		"profile":   req.Profile,
		"tag":       req.Tag,
		"platforms": req.Platforms,
		"branch":    req.Branch,
	}

	err := h.gh.TriggerWorkflow(cfg.BuildOwner, cfg.BuildRepo, "build.yaml", inputs)
	if err != nil {
		jsonError(w, err.Error(), 500)
		return
	}

	go h.saveBuildHistory(req.Profile, req.Tag, req.Platforms)

	logAudit(claims.CodeID, claims.CodeName, "trigger_build",
		fmt.Sprintf("profile=%s tag=%s platforms=%s branch=%s", req.Profile, req.Tag, req.Platforms, req.Branch),
		r.RemoteAddr)

	jsonResponse(w, map[string]string{"message": "构建已触发"})
}

func (h *Handlers) saveBuildHistory(profile, tag, platforms string) {
	content, sha, _ := h.gh.GetFile(buildHistoryPath)
	var history map[string]interface{}
	if content == "" {
		history = make(map[string]interface{})
	} else {
		json.Unmarshal([]byte(content), &history)
		if history == nil {
			history = make(map[string]interface{})
		}
	}

	history[profile] = map[string]interface{}{
		"version":   tag,
		"platforms": platforms,
		"time":      time.Now().Format("2006/1/2 15:04:05"),
	}

	newContent, _ := json.MarshalIndent(history, "", "  ")
	h.gh.SaveFile(buildHistoryPath, string(newContent), sha, "更新打包版本记录: "+profile+" "+tag)
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
