package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

const (
	profileAssetsDir           = "profile-assets"
	maxProfileAssetUploadSize  = 4 << 20
	maxProfileAssetHistoryKeep = 5
)

var profileAssetLabels = map[string]string{
	"app-icon": "应用图标",
}

func validateProfileAssetName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("档案名称不能为空")
	}
	if name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("档案名称非法")
	}
	return nil
}

func validateProfileAssetKind(kind string) error {
	kind = strings.TrimSpace(kind)
	if _, ok := profileAssetLabels[kind]; !ok {
		return fmt.Errorf("不支持的资源类型")
	}
	return nil
}

func buildProfileAssetHistoryURL(r *http.Request, recordID int64) string {
	values := url.Values{}
	values.Set("v", strconv.FormatInt(time.Now().Unix(), 10))
	return (&url.URL{
		Scheme:   requestScheme(r),
		Host:     requestHost(r),
		Path:     fmt.Sprintf("/profile-assets/history/%d", recordID),
		RawQuery: values.Encode(),
	}).String()
}

func detectProfileAssetContentType(data []byte) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("上传文件不能为空")
	}
	contentType := http.DetectContentType(data)
	switch contentType {
	case "image/png", "image/jpeg", "image/webp":
		return contentType, nil
	default:
		return "", fmt.Errorf("仅支持 PNG、JPG、WebP 图片")
	}
}

func profileAssetExtByContentType(contentType string) string {
	switch contentType {
	case "image/png":
		return "png"
	case "image/jpeg":
		return "jpg"
	case "image/webp":
		return "webp"
	default:
		return "bin"
	}
}

func randomProfileAssetSuffix() string {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return hex.EncodeToString(buf)
}

func sanitizeProfileAssetSegment(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "default"
	}

	var b strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastDash = false
		case !lastDash:
			b.WriteByte('-')
			lastDash = true
		}
	}

	result := strings.Trim(b.String(), "-")
	if result == "" {
		return "default"
	}
	return result
}

func buildProfileAssetStoragePath(codeID int, profileName, kind, contentType string) string {
	timestamp := time.Now().UTC().Format("20060102T150405")
	profileSegment := sanitizeProfileAssetSegment(profileName)
	ext := profileAssetExtByContentType(contentType)
	return fmt.Sprintf("%s/%d/%s/%s/%s-%s.%s", profileAssetsDir, codeID, profileSegment, kind, timestamp, randomProfileAssetSuffix(), ext)
}

func (h *Handlers) cleanupOverflowProfileAssets(codeID int, assetKind string) error {
	overflowRecords, err := listOverflowProfileAssetHistoryRecords(codeID, assetKind, maxProfileAssetHistoryKeep)
	if err != nil {
		return err
	}

	for _, record := range overflowRecords {
		if strings.TrimSpace(record.AssetPath) != "" {
			_, sha, err := h.profileGH.GetFile(record.AssetPath)
			if err == nil && strings.TrimSpace(sha) != "" {
				if err := h.profileGH.DeleteFile(record.AssetPath, sha, "清理过期"+profileAssetLabels[assetKind]+": "+record.ProfileName); err != nil && !strings.Contains(err.Error(), "404") {
					return err
				}
			} else if err != nil && !strings.Contains(err.Error(), "404") {
				return err
			}
		}

		if err := deleteProfileAssetHistoryRecord(record.ID); err != nil {
			return err
		}
	}

	return nil
}

func (h *Handlers) UploadProfileAsset(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(chi.URLParam(r, "name"))
	kind := strings.TrimSpace(chi.URLParam(r, "kind"))
	claims := getClaims(r)

	if err := validateProfileAssetName(name); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := validateProfileAssetKind(kind); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !claims.canAccessProfile(name) {
		jsonError(w, "无权操作该档案", http.StatusForbidden)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxProfileAssetUploadSize)
	if err := r.ParseMultipartForm(maxProfileAssetUploadSize); err != nil {
		jsonError(w, "上传失败：文件过大或表单格式错误", http.StatusBadRequest)
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		jsonError(w, "请先选择要上传的图片文件", http.StatusBadRequest)
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		jsonError(w, "读取上传文件失败", http.StatusBadRequest)
		return
	}
	contentType, err := detectProfileAssetContentType(data)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	filePath := buildProfileAssetStoragePath(claims.CodeID, name, kind, contentType)
	label := profileAssetLabels[kind]
	if _, err := h.profileGH.SaveFile(filePath, string(data), "", "上传"+label+": "+name); err != nil {
		jsonError(w, "上传资源失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	record, err := createProfileAssetHistoryRecord(claims.CodeID, claims.CodeName, name, kind, filePath, "", contentType)
	if err != nil {
		jsonError(w, "保存图标历史失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	assetURL := buildProfileAssetHistoryURL(r, record.ID)
	if err := updateProfileAssetHistoryURL(record.ID, assetURL); err != nil {
		jsonError(w, "保存图标地址失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	record.AssetURL = assetURL

	if err := h.cleanupOverflowProfileAssets(claims.CodeID, kind); err != nil {
		log.Printf("清理超额图标历史失败 code_id=%d kind=%s: %v", claims.CodeID, kind, err)
	}

	logAudit(claims.CodeID, claims.CodeName, "upload_profile_asset", fmt.Sprintf("%s:%s:%d", name, kind, record.ID), r.RemoteAddr)

	jsonResponse(w, map[string]interface{}{
		"message": label + "上传成功",
		"record":  record,
		"url":     assetURL,
	})
}

func (h *Handlers) ListProfileAssetHistory(w http.ResponseWriter, r *http.Request) {
	claims := getClaims(r)
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))

	if err := validateProfileAssetKind(kind); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if limit <= 0 || limit > maxProfileAssetHistoryKeep {
		limit = maxProfileAssetHistoryKeep
	}

	records, err := listProfileAssetHistoryRecords(claims.CodeID, kind, limit)
	if err != nil {
		jsonError(w, "加载图标历史失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = []ProfileAssetHistoryRecord{}
	}
	jsonResponse(w, map[string]interface{}{"records": records})
}

func (h *Handlers) GetProfileAsset(w http.ResponseWriter, r *http.Request) {
	recordID, err := strconv.ParseInt(strings.TrimSpace(chi.URLParam(r, "id")), 10, 64)
	if err != nil || recordID <= 0 {
		http.NotFound(w, r)
		return
	}

	record, err := getProfileAssetHistoryRecord(recordID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	content, _, err := h.profileGH.GetFile(record.AssetPath)
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "读取资源失败", http.StatusInternalServerError)
		return
	}

	data := []byte(content)
	contentType := strings.TrimSpace(record.ContentType)
	if contentType == "" {
		contentType, err = detectProfileAssetContentType(data)
		if err != nil {
			http.NotFound(w, r)
			return
		}
	}

	cacheControl := "public, max-age=300"
	if strings.TrimSpace(r.URL.Query().Get("v")) != "" {
		cacheControl = "public, max-age=31536000, immutable"
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", cacheControl)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
