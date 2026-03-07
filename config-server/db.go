package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var db *sql.DB

func initDB() {
	dataDir := "data"
	os.MkdirAll(dataDir, 0755)

	dbPath := filepath.Join(dataDir, "xboard.db")
	var err error
	db, err = sql.Open("sqlite", dbPath)
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}

	db.Exec("PRAGMA journal_mode=WAL")
	db.Exec("PRAGMA busy_timeout=5000")

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS activation_codes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			code TEXT UNIQUE NOT NULL,
			name TEXT DEFAULT '',
			permissions TEXT DEFAULT 'user',
			max_uses INTEGER DEFAULT -1,
			used_count INTEGER DEFAULT 0,
			allowed_profiles TEXT DEFAULT '[]',
			expires_at DATETIME,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			is_active BOOLEAN DEFAULT 1
		);

		CREATE TABLE IF NOT EXISTS audit_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			code_id INTEGER,
			code_name TEXT,
			action TEXT NOT NULL,
			details TEXT,
			ip_address TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS build_records (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			code_id INTEGER NOT NULL,
			code_name TEXT NOT NULL,
			profile TEXT NOT NULL,
			tag TEXT NOT NULL,
			branch TEXT NOT NULL,
			platforms TEXT NOT NULL,
			run_id INTEGER DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'queued',
			conclusion TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		CREATE INDEX IF NOT EXISTS idx_build_records_code_id_created_at
		ON build_records (code_id, created_at DESC);
	`)
	// 兼容旧表：如果 allowed_profiles 列不存在则添加
	db.Exec("ALTER TABLE activation_codes ADD COLUMN allowed_profiles TEXT DEFAULT '[]'")
	if err != nil {
		log.Fatalf("初始化数据库表失败: %v", err)
	}
}

type ActivationCode struct {
	ID              int      `json:"id"`
	Code            string   `json:"code,omitempty"`
	Name            string   `json:"name"`
	Permissions     string   `json:"permissions"`
	MaxUses         int      `json:"max_uses"`
	UsedCount       int      `json:"used_count"`
	AllowedProfiles []string `json:"allowed_profiles"`
	ExpiresAt       *string  `json:"expires_at"`
	CreatedAt       string   `json:"created_at"`
	IsActive        bool     `json:"is_active"`
}

type BuildRecord struct {
	ID         int64  `json:"id"`
	CodeID     int    `json:"code_id"`
	CodeName   string `json:"code_name"`
	Profile    string `json:"profile"`
	Tag        string `json:"tag"`
	Branch     string `json:"branch"`
	Platforms  string `json:"platforms"`
	RunID      int64  `json:"run_id"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

func createBuildRecord(codeID int, codeName, profile, tag, branch, platforms string) (*BuildRecord, error) {
	result, err := db.Exec(
		`INSERT INTO build_records (code_id, code_name, profile, tag, branch, platforms, status, conclusion)
		 VALUES (?, ?, ?, ?, ?, ?, 'queued', '')`,
		codeID, codeName, profile, tag, branch, platforms,
	)
	if err != nil {
		return nil, err
	}

	recordID, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}

	return getBuildRecord(recordID)
}

func updateBuildRecordStatus(recordID, runID int64, status, conclusion string) error {
	if recordID <= 0 {
		return nil
	}

	_, err := db.Exec(
		`UPDATE build_records
		 SET run_id = CASE WHEN ? > 0 THEN ? ELSE run_id END,
		     status = CASE WHEN ? != '' THEN ? ELSE status END,
		     conclusion = ?,
		     updated_at = CURRENT_TIMESTAMP
		 WHERE id = ?`,
		runID, runID, status, status, conclusion, recordID,
	)
	return err
}

func getBuildRecord(recordID int64) (*BuildRecord, error) {
	var record BuildRecord
	row := db.QueryRow(
		`SELECT id, code_id, code_name, profile, tag, branch, platforms, run_id, status, conclusion, created_at, updated_at
		 FROM build_records WHERE id = ?`,
		recordID,
	)
	if err := row.Scan(
		&record.ID,
		&record.CodeID,
		&record.CodeName,
		&record.Profile,
		&record.Tag,
		&record.Branch,
		&record.Platforms,
		&record.RunID,
		&record.Status,
		&record.Conclusion,
		&record.CreatedAt,
		&record.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &record, nil
}

func listBuildRecords(codeID int, isAdmin bool, limit int) ([]BuildRecord, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	query := `SELECT id, code_id, code_name, profile, tag, branch, platforms, run_id, status, conclusion, created_at, updated_at
		FROM build_records`
	args := []interface{}{}
	if !isAdmin {
		query += ` WHERE code_id = ?`
		args = append(args, codeID)
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := []BuildRecord{}
	for rows.Next() {
		var record BuildRecord
		if err := rows.Scan(
			&record.ID,
			&record.CodeID,
			&record.CodeName,
			&record.Profile,
			&record.Tag,
			&record.Branch,
			&record.Platforms,
			&record.RunID,
			&record.Status,
			&record.Conclusion,
			&record.CreatedAt,
			&record.UpdatedAt,
		); err != nil {
			continue
		}
		records = append(records, record)
	}
	return records, nil
}

func listOverflowBuildRecords(codeID int, keep int) ([]BuildRecord, error) {
	if keep < 0 {
		keep = 0
	}

	rows, err := db.Query(
		`SELECT id, code_id, code_name, profile, tag, branch, platforms, run_id, status, conclusion, created_at, updated_at
		 FROM build_records
		 WHERE code_id = ?
		 ORDER BY created_at DESC, id DESC
		 LIMIT -1 OFFSET ?`,
		codeID, keep,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := []BuildRecord{}
	for rows.Next() {
		var record BuildRecord
		if err := rows.Scan(
			&record.ID,
			&record.CodeID,
			&record.CodeName,
			&record.Profile,
			&record.Tag,
			&record.Branch,
			&record.Platforms,
			&record.RunID,
			&record.Status,
			&record.Conclusion,
			&record.CreatedAt,
			&record.UpdatedAt,
		); err != nil {
			continue
		}
		records = append(records, record)
	}
	return records, nil
}

func deleteBuildRecord(recordID int64) error {
	result, err := db.Exec(`DELETE FROM build_records WHERE id = ?`, recordID)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return fmt.Errorf("打包记录不存在")
	}
	return nil
}

func getActivationCodeByID(id int) (*ActivationCode, error) {
	ac := &ActivationCode{}
	var expiresAt sql.NullString
	var allowedProfilesJSON string

	err := db.QueryRow(
		`SELECT id, code, name, permissions, max_uses, used_count, allowed_profiles, expires_at, created_at, is_active
		 FROM activation_codes WHERE id = ?`,
		id,
	).Scan(&ac.ID, &ac.Code, &ac.Name, &ac.Permissions, &ac.MaxUses, &ac.UsedCount, &allowedProfilesJSON, &expiresAt, &ac.CreatedAt, &ac.IsActive)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("激活码无效")
	}
	if err != nil {
		return nil, err
	}

	json.Unmarshal([]byte(allowedProfilesJSON), &ac.AllowedProfiles)
	if ac.AllowedProfiles == nil {
		ac.AllowedProfiles = []string{}
	}
	if expiresAt.Valid {
		ac.ExpiresAt = &expiresAt.String
	}
	return ac, nil
}

func parseActivationCodeExpiresAt(ac *ActivationCode) (*time.Time, error) {
	if ac == nil || ac.ExpiresAt == nil || strings.TrimSpace(*ac.ExpiresAt) == "" {
		return nil, nil
	}

	t, err := time.ParseInLocation("2006-01-02 15:04:05", *ac.ExpiresAt, time.Local)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func getBuildAvailability(ac *ActivationCode) (bool, string) {
	if ac == nil {
		return false, "激活码无效"
	}
	if !ac.IsActive {
		return false, "激活码已停用"
	}

	expiresAt, err := parseActivationCodeExpiresAt(ac)
	if err == nil && expiresAt != nil && !time.Now().Before(*expiresAt) {
		return false, "激活码已过期"
	}
	if ac.MaxUses > 0 && ac.UsedCount >= ac.MaxUses {
		return false, "激活码已达最大打包次数"
	}
	if ac.MaxUses < 0 {
		return true, "可打包（不限次数）"
	}
	return true, "可打包"
}

func getRemainingBuildUses(ac *ActivationCode) int {
	if ac == nil || ac.MaxUses < 0 {
		return -1
	}
	remaining := ac.MaxUses - ac.UsedCount
	if remaining < 0 {
		return 0
	}
	return remaining
}

func validateCode(code string) (*ActivationCode, error) {
	ac := &ActivationCode{}
	var expiresAt sql.NullString
	var allowedProfilesJSON string

	err := db.QueryRow(
		`SELECT id, code, name, permissions, max_uses, used_count, allowed_profiles, expires_at, created_at, is_active
		 FROM activation_codes WHERE code = ? AND is_active = 1`,
		code,
	).Scan(&ac.ID, &ac.Code, &ac.Name, &ac.Permissions, &ac.MaxUses, &ac.UsedCount, &allowedProfilesJSON, &expiresAt, &ac.CreatedAt, &ac.IsActive)

	json.Unmarshal([]byte(allowedProfilesJSON), &ac.AllowedProfiles)
	if ac.AllowedProfiles == nil {
		ac.AllowedProfiles = []string{}
	}

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("激活码无效")
	}
	if err != nil {
		return nil, err
	}

	if expiresAt.Valid {
		ac.ExpiresAt = &expiresAt.String
		t, err := time.ParseInLocation("2006-01-02 15:04:05", expiresAt.String, time.Local)
		if err == nil && time.Now().After(t) {
			return nil, fmt.Errorf("激活码已过期")
		}
	}

	return ac, nil
}

func consumeBuildUsage(codeID int) error {
	if codeID <= 0 {
		return nil
	}

	result, err := db.Exec(
		`UPDATE activation_codes
		 SET used_count = used_count + 1
		 WHERE id = ?
		   AND is_active = 1
		   AND (expires_at IS NULL OR expires_at = '' OR datetime(expires_at) > datetime('now', 'localtime'))
		   AND (max_uses < 0 OR used_count < max_uses)`,
		codeID,
	)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected > 0 {
		return nil
	}

	ac, err := getActivationCodeByID(codeID)
	if err != nil {
		return err
	}
	canBuild, statusText := getBuildAvailability(ac)
	if !canBuild {
		return fmt.Errorf(statusText)
	}

	return fmt.Errorf("激活码不可用")
}

func rollbackBuildUsage(codeID int) error {
	if codeID <= 0 {
		return nil
	}

	_, err := db.Exec(
		`UPDATE activation_codes
		 SET used_count = CASE WHEN used_count > 0 THEN used_count - 1 ELSE 0 END
		 WHERE id = ?`,
		codeID,
	)
	return err
}

func generateCode() string {
	b := make([]byte, 8)
	rand.Read(b)
	return "XB-" + hex.EncodeToString(b)
}

func normalizeExpiresAt(expiresAt string) (*string, error) {
	expiresAt = strings.TrimSpace(expiresAt)
	if expiresAt == "" {
		return nil, nil
	}

	dateTimeLayouts := []string{
		"2006-01-02T15:04",
		"2006-01-02 15:04",
		"2006-01-02 15:04:05",
		"2006/01/02 15:04",
		"2006/01/02 15:04:05",
		"2006.01.02 15:04",
		"2006.01.02 15:04:05",
		"2006-1-2 15:04",
		"2006-1-2 15:04:05",
		"2006/1/2 15:04",
		"2006/1/2 15:04:05",
		"2006.1.2 15:04",
		"2006.1.2 15:04:05",
	}
	for _, layout := range dateTimeLayouts {
		if parsed, err := time.ParseInLocation(layout, expiresAt, time.Local); err == nil {
			formatted := parsed.Format("2006-01-02 15:04:05")
			return &formatted, nil
		}
	}

	dateOnlyLayouts := []string{
		"2006-01-02",
		"2006/01/02",
		"2006.01.02",
		"2006-1-2",
		"2006/1/2",
		"2006.1.2",
		"2006年1月2日",
		"2006年01月02日",
	}
	for _, layout := range dateOnlyLayouts {
		if parsed, err := time.ParseInLocation(layout, expiresAt, time.Local); err == nil {
			endOfDay := time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 23, 59, 59, 0, time.Local)
			formatted := endOfDay.Format("2006-01-02 15:04:05")
			return &formatted, nil
		}
	}

	return nil, fmt.Errorf("到期时间格式错误，支持如 2026-3-7、2026/3/7、2026.3.7")
}

func createCode(name string, maxUses int, allowedProfiles []string, expiresAt string) (*ActivationCode, error) {
	code := generateCode()
	if allowedProfiles == nil {
		allowedProfiles = []string{}
	}
	profilesJSON, _ := json.Marshal(allowedProfiles)

	normalizedExpiresAt, err := normalizeExpiresAt(expiresAt)
	if err != nil {
		return nil, err
	}

	var expiresValue interface{}
	if normalizedExpiresAt != nil {
		expiresValue = *normalizedExpiresAt
	}

	_, err = db.Exec(
		"INSERT INTO activation_codes (code, name, permissions, max_uses, allowed_profiles, expires_at) VALUES (?, ?, 'user', ?, ?, ?)",
		code, name, maxUses, string(profilesJSON), expiresValue,
	)
	if err != nil {
		return nil, err
	}

	return &ActivationCode{
		Code:            code,
		Name:            name,
		Permissions:     "user",
		MaxUses:         maxUses,
		AllowedProfiles: allowedProfiles,
		ExpiresAt:       normalizedExpiresAt,
	}, nil
}

func updateCode(id int, name string, maxUses int, usedCount int, allowedProfiles []string, expiresAt string) error {
	if allowedProfiles == nil {
		allowedProfiles = []string{}
	}
	profilesJSON, _ := json.Marshal(allowedProfiles)

	normalizedExpiresAt, err := normalizeExpiresAt(expiresAt)
	if err != nil {
		return err
	}

	var expiresValue interface{}
	if normalizedExpiresAt != nil {
		expiresValue = *normalizedExpiresAt
	}

	result, err := db.Exec(
		"UPDATE activation_codes SET name = ?, max_uses = ?, used_count = ?, allowed_profiles = ?, expires_at = ? WHERE id = ?",
		name, maxUses, usedCount, string(profilesJSON), expiresValue, id,
	)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return fmt.Errorf("激活码不存在")
	}

	return nil
}

func listCodes() ([]ActivationCode, error) {
	rows, err := db.Query(
		`SELECT id, code, name, permissions, max_uses, used_count, allowed_profiles, expires_at, created_at, is_active
		 FROM activation_codes ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var codes []ActivationCode
	for rows.Next() {
		var ac ActivationCode
		var expiresAt sql.NullString
		var allowedProfilesJSON string
		err := rows.Scan(&ac.ID, &ac.Code, &ac.Name, &ac.Permissions, &ac.MaxUses, &ac.UsedCount, &allowedProfilesJSON, &expiresAt, &ac.CreatedAt, &ac.IsActive)
		if err != nil {
			continue
		}
		json.Unmarshal([]byte(allowedProfilesJSON), &ac.AllowedProfiles)
		if ac.AllowedProfiles == nil {
			ac.AllowedProfiles = []string{}
		}
		if expiresAt.Valid {
			ac.ExpiresAt = &expiresAt.String
		}
		codes = append(codes, ac)
	}
	return codes, nil
}

func deleteCode(id int) error {
	_, err := db.Exec("DELETE FROM activation_codes WHERE id = ?", id)
	return err
}

func logAudit(codeID int, codeName, action, details, ip string) {
	db.Exec(
		"INSERT INTO audit_logs (code_id, code_name, action, details, ip_address) VALUES (?, ?, ?, ?, ?)",
		codeID, codeName, action, details, ip,
	)
}

func getAuditLogs(limit int) ([]map[string]interface{}, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := db.Query(
		`SELECT id, code_id, code_name, action, details, ip_address, created_at
		 FROM audit_logs ORDER BY created_at DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []map[string]interface{}
	for rows.Next() {
		var id, codeID int
		var codeName, action, createdAt string
		var detailsNull, ipNull sql.NullString

		err := rows.Scan(&id, &codeID, &codeName, &action, &detailsNull, &ipNull, &createdAt)
		if err != nil {
			continue
		}

		entry := map[string]interface{}{
			"id":         id,
			"code_id":    codeID,
			"code_name":  codeName,
			"action":     action,
			"details":    "",
			"ip":         "",
			"created_at": createdAt,
		}
		if detailsNull.Valid {
			entry["details"] = detailsNull.String
		}
		if ipNull.Valid {
			entry["ip"] = ipNull.String
		}
		logs = append(logs, entry)
	}
	return logs, nil
}
