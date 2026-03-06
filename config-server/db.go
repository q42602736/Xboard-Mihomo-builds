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
	query += ` ORDER BY created_at DESC LIMIT ?`
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
		t, _ := time.Parse("2006-01-02 15:04:05", expiresAt.String)
		if time.Now().After(t) {
			return nil, fmt.Errorf("激活码已过期")
		}
	}

	if ac.MaxUses > 0 && ac.UsedCount >= ac.MaxUses {
		return nil, fmt.Errorf("激活码已达最大使用次数")
	}

	db.Exec("UPDATE activation_codes SET used_count = used_count + 1 WHERE id = ?", ac.ID)
	return ac, nil
}

func generateCode() string {
	b := make([]byte, 8)
	rand.Read(b)
	return "XB-" + hex.EncodeToString(b)
}

func createCode(name string, maxUses int, allowedProfiles []string) (*ActivationCode, error) {
	code := generateCode()
	if allowedProfiles == nil {
		allowedProfiles = []string{}
	}
	profilesJSON, _ := json.Marshal(allowedProfiles)

	_, err := db.Exec(
		"INSERT INTO activation_codes (code, name, permissions, max_uses, allowed_profiles) VALUES (?, ?, 'user', ?, ?)",
		code, name, maxUses, string(profilesJSON),
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
	}, nil
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
