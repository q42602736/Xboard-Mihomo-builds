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
	"strconv"
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
			allowed_platforms TEXT DEFAULT '[]',
			allowed_clients TEXT DEFAULT '[]',
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
				request_id TEXT NOT NULL UNIQUE,
				client TEXT NOT NULL DEFAULT 'xboard_mihomo_sub',
				profile TEXT NOT NULL,
				tag TEXT NOT NULL,
				branch TEXT NOT NULL,
				core TEXT NOT NULL DEFAULT 'mihomo',
				platforms TEXT NOT NULL,
				run_id INTEGER DEFAULT 0,
				run_url TEXT DEFAULT '',
				release_tag TEXT DEFAULT '',
				status TEXT NOT NULL DEFAULT 'queued',
				conclusion TEXT DEFAULT '',
				status_source TEXT DEFAULT '',
				progress_percent INTEGER DEFAULT 0,
				progress_text TEXT DEFAULT '',
				progress_stage TEXT DEFAULT '',
				progress_details TEXT DEFAULT '',
				usage_counted INTEGER DEFAULT 0,
				bound_at DATETIME,
				finished_at DATETIME,
				last_sync_at DATETIME,
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
			);

		CREATE TABLE IF NOT EXISTS system_settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS profile_asset_history (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			code_id INTEGER NOT NULL,
			code_name TEXT NOT NULL,
			client TEXT NOT NULL DEFAULT 'xboard_mihomo_sub',
			profile_name TEXT NOT NULL DEFAULT '',
			asset_kind TEXT NOT NULL,
			asset_path TEXT NOT NULL UNIQUE,
			asset_url TEXT NOT NULL,
			content_type TEXT NOT NULL DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		CREATE INDEX IF NOT EXISTS idx_build_records_code_id_created_at
		ON build_records (code_id, created_at DESC);

		CREATE INDEX IF NOT EXISTS idx_profile_asset_history_code_kind_created_at
		ON profile_asset_history (code_id, asset_kind, created_at DESC, id DESC);
	`)
	// 兼容旧表：如果 allowed_profiles 列不存在则添加
	db.Exec("ALTER TABLE activation_codes ADD COLUMN allowed_profiles TEXT DEFAULT '[]'")
	db.Exec("ALTER TABLE activation_codes ADD COLUMN allowed_platforms TEXT DEFAULT '[]'")
	db.Exec("ALTER TABLE activation_codes ADD COLUMN allowed_clients TEXT DEFAULT '[]'")
	db.Exec("ALTER TABLE profile_asset_history ADD COLUMN client TEXT NOT NULL DEFAULT 'xboard_mihomo_sub'")
	db.Exec("CREATE INDEX IF NOT EXISTS idx_profile_asset_history_code_client_kind_created_at ON profile_asset_history (code_id, client, asset_kind, created_at DESC, id DESC)")
	ensureBuildRecordColumn("request_id", "TEXT DEFAULT ''")
	ensureBuildRecordColumn("client", "TEXT DEFAULT 'xboard_mihomo_sub'")
	ensureBuildRecordColumn("core", "TEXT DEFAULT 'mihomo'")
	ensureBuildRecordColumn("run_url", "TEXT DEFAULT ''")
	ensureBuildRecordColumn("release_tag", "TEXT DEFAULT ''")
	ensureBuildRecordColumn("status_source", "TEXT DEFAULT ''")
	ensureBuildRecordColumn("progress_percent", "INTEGER DEFAULT 0")
	ensureBuildRecordColumn("progress_text", "TEXT DEFAULT ''")
	ensureBuildRecordColumn("progress_stage", "TEXT DEFAULT ''")
	ensureBuildRecordColumn("progress_details", "TEXT DEFAULT ''")
	usageCountedColumnAdded := ensureBuildRecordColumn("usage_counted", "INTEGER DEFAULT 0")
	if usageCountedColumnAdded {
		db.Exec("UPDATE build_records SET usage_counted = 1")
	}
	ensureBuildRecordColumn("bound_at", "DATETIME")
	ensureBuildRecordColumn("finished_at", "DATETIME")
	ensureBuildRecordColumn("last_sync_at", "DATETIME")
	db.Exec("UPDATE build_records SET request_id = CAST(id AS TEXT) WHERE request_id IS NULL OR TRIM(request_id) = ''")
	db.Exec("UPDATE build_records SET last_sync_at = COALESCE(updated_at, created_at) WHERE last_sync_at IS NULL")
	db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_build_records_request_id ON build_records (request_id)")
	if err != nil {
		log.Fatalf("初始化数据库表失败: %v", err)
	}
}

func ensureBuildRecordColumn(columnName, definition string) bool {
	_, err := db.Exec(fmt.Sprintf("ALTER TABLE build_records ADD COLUMN %s %s", columnName, definition))
	return err == nil
}

type ActivationCode struct {
	ID               int      `json:"id"`
	Code             string   `json:"code,omitempty"`
	Name             string   `json:"name"`
	Permissions      string   `json:"permissions"`
	MaxUses          int      `json:"max_uses"`
	UsedCount        int      `json:"used_count"`
	AllowedProfiles  []string `json:"allowed_profiles"`
	AllowedPlatforms []string `json:"allowed_platforms"`
	AllowedClients   []string `json:"allowed_clients"`
	ExpiresAt        *string  `json:"expires_at"`
	CreatedAt        string   `json:"created_at"`
	IsActive         bool     `json:"is_active"`
}

type CustomFeatureGroup struct {
	Name            string   `json:"name"`
	IntegrationCode string   `json:"integration_code"`
	FeatureKeys     []string `json:"feature_keys"`
}

type BuildRecord struct {
	ID              int64  `json:"id"`
	CodeID          int    `json:"code_id"`
	CodeName        string `json:"code_name"`
	RequestID       string `json:"request_id"`
	Client          string `json:"client"`
	Profile         string `json:"profile"`
	Tag             string `json:"tag"`
	Branch          string `json:"branch"`
	Core            string `json:"core"`
	Platforms       string `json:"platforms"`
	RunID           int64  `json:"run_id"`
	RunURL          string `json:"run_url"`
	ReleaseTag      string `json:"release_tag"`
	Status          string `json:"status"`
	Conclusion      string `json:"conclusion"`
	StatusSource    string `json:"status_source"`
	Progress        int    `json:"progress_percent"`
	ProgressText    string `json:"progress_text"`
	ProgressStage   string `json:"progress_stage"`
	ProgressDetails string `json:"progress_details"`
	UsageCounted    int    `json:"usage_counted"`
	BoundAt         string `json:"bound_at"`
	FinishedAt      string `json:"finished_at"`
	LastSyncAt      string `json:"last_sync_at"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

type ProfileAssetHistoryRecord struct {
	ID          int64  `json:"id"`
	CodeID      int    `json:"code_id"`
	CodeName    string `json:"code_name"`
	Client      string `json:"client"`
	ProfileName string `json:"profile_name"`
	AssetKind   string `json:"asset_kind"`
	AssetPath   string `json:"asset_path"`
	AssetURL    string `json:"asset_url"`
	ContentType string `json:"content_type"`
	CreatedAt   string `json:"created_at"`
}

func createBuildRecord(codeID int, codeName, client, profile, tag, branch, core, platforms string) (*BuildRecord, error) {
	return createBuildRecordWithProgressDetails(codeID, codeName, client, profile, tag, branch, core, platforms, "")
}

func createBuildRecordWithProgressDetails(codeID int, codeName, client, profile, tag, branch, core, platforms, progressDetails string) (*BuildRecord, error) {
	requestID := generateBuildRequestID()
	result, err := db.Exec(
		`INSERT INTO build_records (code_id, code_name, request_id, client, profile, tag, branch, core, platforms, status, conclusion, status_source, progress_percent, progress_text, progress_stage, progress_details, usage_counted, last_sync_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'dispatching', '', 'server', 5, '已提交打包请求，等待 GitHub Actions 接收', 'dispatching', ?, 0, CURRENT_TIMESTAMP)`,
		codeID, codeName, requestID, client, profile, tag, branch, core, platforms, progressDetails,
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
	return updateBuildRecordStatusExt(recordID, runID, status, conclusion, "", "", "")
}

func updateBuildRecordStatusExt(recordID, runID int64, status, conclusion, statusSource, runURL, releaseTag string) error {
	return updateBuildRecordStatusProgressExt(recordID, runID, status, conclusion, statusSource, runURL, releaseTag, -1, "", "", "")
}

func updateBuildRecordStatusProgressExt(recordID, runID int64, status, conclusion, statusSource, runURL, releaseTag string, progress int, progressText, progressStage, progressDetails string) error {
	if recordID <= 0 {
		return nil
	}
	if progress > 100 {
		progress = 100
	}

	_, err := db.Exec(
		`UPDATE build_records
		 SET run_id = CASE WHEN ? > 0 THEN ? ELSE run_id END,
		     run_url = CASE WHEN ? != '' THEN ? ELSE run_url END,
		     release_tag = CASE WHEN ? != '' THEN ? ELSE release_tag END,
		     status = CASE WHEN ? != '' THEN ? ELSE status END,
		     conclusion = ?,
		     status_source = CASE WHEN ? != '' THEN ? ELSE status_source END,
		     progress_percent = CASE WHEN ? >= 0 AND ? >= COALESCE(progress_percent, 0) THEN ? ELSE progress_percent END,
		     progress_text = CASE WHEN ? != '' AND (? < 0 OR ? >= COALESCE(progress_percent, 0)) THEN ? ELSE progress_text END,
		     progress_stage = CASE WHEN ? != '' AND (? < 0 OR ? >= COALESCE(progress_percent, 0)) THEN ? ELSE progress_stage END,
		     progress_details = CASE WHEN ? != '' THEN ? ELSE progress_details END,
		     bound_at = CASE WHEN ? > 0 AND (bound_at IS NULL OR bound_at = '') THEN CURRENT_TIMESTAMP ELSE bound_at END,
		     finished_at = CASE WHEN ? = 'completed' THEN CURRENT_TIMESTAMP ELSE finished_at END,
		     last_sync_at = CURRENT_TIMESTAMP,
		     updated_at = CURRENT_TIMESTAMP
		 WHERE id = ?`,
		runID, runID,
		runURL, runURL,
		releaseTag, releaseTag,
		status, status,
		conclusion,
		statusSource, statusSource,
		progress, progress, progress,
		progressText, progress, progress, progressText,
		progressStage, progress, progress, progressStage,
		progressDetails, progressDetails,
		runID,
		status,
		recordID,
	)
	return err
}

func getBuildRecord(recordID int64) (*BuildRecord, error) {
	var record BuildRecord
	row := db.QueryRow(
		`SELECT id, code_id, code_name, request_id, COALESCE(client, 'xboard_mihomo_sub'), profile, tag, branch, COALESCE(core, 'mihomo'), platforms, run_id, run_url, release_tag, status, conclusion, status_source, COALESCE(progress_percent, 0), COALESCE(progress_text, ''), COALESCE(progress_stage, ''), COALESCE(progress_details, ''), COALESCE(usage_counted, 0),
		        COALESCE(bound_at, ''), COALESCE(finished_at, ''), COALESCE(last_sync_at, ''), created_at, updated_at
		 FROM build_records WHERE id = ?`,
		recordID,
	)
	if err := row.Scan(
		&record.ID,
		&record.CodeID,
		&record.CodeName,
		&record.RequestID,
		&record.Client,
		&record.Profile,
		&record.Tag,
		&record.Branch,
		&record.Core,
		&record.Platforms,
		&record.RunID,
		&record.RunURL,
		&record.ReleaseTag,
		&record.Status,
		&record.Conclusion,
		&record.StatusSource,
		&record.Progress,
		&record.ProgressText,
		&record.ProgressStage,
		&record.ProgressDetails,
		&record.UsageCounted,
		&record.BoundAt,
		&record.FinishedAt,
		&record.LastSyncAt,
		&record.CreatedAt,
		&record.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &record, nil
}

func getBuildRecordByRequestID(requestID string) (*BuildRecord, error) {
	if strings.TrimSpace(requestID) == "" {
		return nil, fmt.Errorf("构建请求 ID 不能为空")
	}
	var record BuildRecord
	row := db.QueryRow(
		`SELECT id, code_id, code_name, request_id, COALESCE(client, 'xboard_mihomo_sub'), profile, tag, branch, COALESCE(core, 'mihomo'), platforms, run_id, run_url, release_tag, status, conclusion, status_source, COALESCE(progress_percent, 0), COALESCE(progress_text, ''), COALESCE(progress_stage, ''), COALESCE(progress_details, ''), COALESCE(usage_counted, 0),
		        COALESCE(bound_at, ''), COALESCE(finished_at, ''), COALESCE(last_sync_at, ''), created_at, updated_at
		 FROM build_records WHERE request_id = ?`,
		requestID,
	)
	if err := row.Scan(
		&record.ID,
		&record.CodeID,
		&record.CodeName,
		&record.RequestID,
		&record.Client,
		&record.Profile,
		&record.Tag,
		&record.Branch,
		&record.Core,
		&record.Platforms,
		&record.RunID,
		&record.RunURL,
		&record.ReleaseTag,
		&record.Status,
		&record.Conclusion,
		&record.StatusSource,
		&record.Progress,
		&record.ProgressText,
		&record.ProgressStage,
		&record.ProgressDetails,
		&record.UsageCounted,
		&record.BoundAt,
		&record.FinishedAt,
		&record.LastSyncAt,
		&record.CreatedAt,
		&record.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &record, nil
}

func listBuildRecords(codeID int, isAdmin bool, limit int) ([]BuildRecord, error) {
	return listBuildRecordsByClient(codeID, isAdmin, "", limit)
}

func listBuildRecordsByClient(codeID int, isAdmin bool, client string, limit int) ([]BuildRecord, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	query := `SELECT id, code_id, code_name, request_id, COALESCE(client, 'xboard_mihomo_sub'), profile, tag, branch, COALESCE(core, 'mihomo'), platforms, run_id, run_url, release_tag, status, conclusion, status_source, COALESCE(progress_percent, 0), COALESCE(progress_text, ''), COALESCE(progress_stage, ''), COALESCE(progress_details, ''), COALESCE(usage_counted, 0),
	                 COALESCE(bound_at, ''), COALESCE(finished_at, ''), COALESCE(last_sync_at, ''), created_at, updated_at
		FROM build_records`
	args := []interface{}{}
	whereParts := []string{}
	if !isAdmin {
		whereParts = append(whereParts, `code_id = ?`)
		args = append(args, codeID)
	}
	if strings.TrimSpace(client) != "" {
		whereParts = append(whereParts, `COALESCE(client, 'xboard_mihomo_sub') = ?`)
		args = append(args, client)
	}
	if len(whereParts) > 0 {
		query += ` WHERE ` + strings.Join(whereParts, ` AND `)
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
			&record.RequestID,
			&record.Client,
			&record.Profile,
			&record.Tag,
			&record.Branch,
			&record.Core,
			&record.Platforms,
			&record.RunID,
			&record.RunURL,
			&record.ReleaseTag,
			&record.Status,
			&record.Conclusion,
			&record.StatusSource,
			&record.Progress,
			&record.ProgressText,
			&record.ProgressStage,
			&record.ProgressDetails,
			&record.UsageCounted,
			&record.BoundAt,
			&record.FinishedAt,
			&record.LastSyncAt,
			&record.CreatedAt,
			&record.UpdatedAt,
		); err != nil {
			continue
		}
		records = append(records, record)
	}
	return records, nil
}

func listActiveBuildRecordsForQueue(client string, since time.Time, limit int) ([]BuildRecord, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	client = strings.TrimSpace(client)
	rows, err := db.Query(
		`SELECT id, code_id, code_name, request_id, COALESCE(client, 'xboard_mihomo_sub'), profile, tag, branch, COALESCE(core, 'mihomo'), platforms, run_id, run_url, release_tag, status, conclusion, status_source, COALESCE(progress_percent, 0), COALESCE(progress_text, ''), COALESCE(progress_stage, ''), COALESCE(progress_details, ''), COALESCE(usage_counted, 0),
		        COALESCE(bound_at, ''), COALESCE(finished_at, ''), COALESCE(last_sync_at, ''), created_at, updated_at
		 FROM build_records
		 WHERE status != 'completed'
		   AND COALESCE(conclusion, '') != 'trigger_failed'
		   AND datetime(COALESCE(updated_at, created_at)) >= datetime(?)
		   AND (? = '' OR COALESCE(client, 'xboard_mihomo_sub') = ?)
		 ORDER BY created_at DESC, id DESC
		 LIMIT ?`,
		since.Format("2006-01-02 15:04:05"),
		client, client,
		limit,
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
			&record.RequestID,
			&record.Client,
			&record.Profile,
			&record.Tag,
			&record.Branch,
			&record.Core,
			&record.Platforms,
			&record.RunID,
			&record.RunURL,
			&record.ReleaseTag,
			&record.Status,
			&record.Conclusion,
			&record.StatusSource,
			&record.Progress,
			&record.ProgressText,
			&record.ProgressStage,
			&record.ProgressDetails,
			&record.UsageCounted,
			&record.BoundAt,
			&record.FinishedAt,
			&record.LastSyncAt,
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
	return listOverflowBuildRecordsByClient(codeID, "", keep)
}

func listOverflowBuildRecordsByClient(codeID int, client string, keep int) ([]BuildRecord, error) {
	if keep < 0 {
		keep = 0
	}

	rows, err := db.Query(
		`SELECT id, code_id, code_name, request_id, COALESCE(client, 'xboard_mihomo_sub'), profile, tag, branch, COALESCE(core, 'mihomo'), platforms, run_id, run_url, release_tag, status, conclusion, status_source, COALESCE(progress_percent, 0), COALESCE(progress_text, ''), COALESCE(progress_stage, ''), COALESCE(progress_details, ''), COALESCE(usage_counted, 0),
		        COALESCE(bound_at, ''), COALESCE(finished_at, ''), COALESCE(last_sync_at, ''), created_at, updated_at
		 FROM build_records
		 WHERE code_id = ? AND (? = '' OR COALESCE(client, 'xboard_mihomo_sub') = ?)
		 ORDER BY created_at DESC, id DESC
		 LIMIT -1 OFFSET ?`,
		codeID, client, client, keep,
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
			&record.RequestID,
			&record.Client,
			&record.Profile,
			&record.Tag,
			&record.Branch,
			&record.Core,
			&record.Platforms,
			&record.RunID,
			&record.RunURL,
			&record.ReleaseTag,
			&record.Status,
			&record.Conclusion,
			&record.StatusSource,
			&record.Progress,
			&record.ProgressText,
			&record.ProgressStage,
			&record.ProgressDetails,
			&record.UsageCounted,
			&record.BoundAt,
			&record.FinishedAt,
			&record.LastSyncAt,
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

func renameProfileReferences(oldName, newName string) error {
	oldName = strings.TrimSpace(oldName)
	newName = strings.TrimSpace(newName)
	if oldName == "" || newName == "" {
		return fmt.Errorf("档案名称不能为空")
	}
	if oldName == newName {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	rows, err := tx.Query(`SELECT id, allowed_profiles FROM activation_codes`)
	if err != nil {
		return err
	}

	type codeProfileUpdate struct {
		id       int
		profiles []string
	}
	updates := []codeProfileUpdate{}
	for rows.Next() {
		var id int
		var allowedProfilesJSON string
		if err := rows.Scan(&id, &allowedProfilesJSON); err != nil {
			rows.Close()
			return err
		}

		var profiles []string
		_ = json.Unmarshal([]byte(allowedProfilesJSON), &profiles)
		changed := false
		seen := map[string]struct{}{}
		renamedProfiles := make([]string, 0, len(profiles))
		for _, profile := range profiles {
			profile = strings.TrimSpace(profile)
			if profile == "" {
				continue
			}
			if profile == oldName {
				profile = newName
				changed = true
			}
			if _, ok := seen[profile]; ok {
				changed = true
				continue
			}
			seen[profile] = struct{}{}
			renamedProfiles = append(renamedProfiles, profile)
		}
		if changed {
			updates = append(updates, codeProfileUpdate{id: id, profiles: renamedProfiles})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, update := range updates {
		profilesJSON, _ := json.Marshal(update.profiles)
		if _, err := tx.Exec(`UPDATE activation_codes SET allowed_profiles = ? WHERE id = ?`, string(profilesJSON), update.id); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(`UPDATE build_records SET profile = ? WHERE profile = ?`, newName, oldName); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE profile_asset_history SET profile_name = ? WHERE profile_name = ?`, newName, oldName); err != nil {
		return err
	}

	return tx.Commit()
}

func appendAllowedProfileToCode(codeID int, profileName string) error {
	profileName = strings.TrimSpace(profileName)
	if codeID <= 0 || profileName == "" {
		return nil
	}

	var allowedProfilesJSON string
	if err := db.QueryRow(`SELECT allowed_profiles FROM activation_codes WHERE id = ?`, codeID).Scan(&allowedProfilesJSON); err != nil {
		return err
	}

	var profiles []string
	_ = json.Unmarshal([]byte(allowedProfilesJSON), &profiles)
	if len(profiles) == 0 {
		return nil
	}
	for _, profile := range profiles {
		if strings.TrimSpace(profile) == profileName {
			return nil
		}
	}

	profiles = append(profiles, profileName)
	nextJSON, _ := json.Marshal(profiles)
	_, err := db.Exec(`UPDATE activation_codes SET allowed_profiles = ? WHERE id = ?`, string(nextJSON), codeID)
	return err
}

func createProfileAssetHistoryRecord(codeID int, codeName, client, profileName, assetKind, assetPath, assetURL, contentType string) (*ProfileAssetHistoryRecord, error) {
	client = normalizeBuildClient(client)
	result, err := db.Exec(
		`INSERT INTO profile_asset_history (code_id, code_name, client, profile_name, asset_kind, asset_path, asset_url, content_type)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		codeID, codeName, client, profileName, assetKind, assetPath, assetURL, contentType,
	)
	if err != nil {
		return nil, err
	}

	recordID, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}
	return getProfileAssetHistoryRecord(recordID)
}

func updateProfileAssetHistoryURL(recordID int64, assetURL string) error {
	_, err := db.Exec(
		`UPDATE profile_asset_history
		 SET asset_url = ?
		 WHERE id = ?`,
		assetURL, recordID,
	)
	return err
}

func getProfileAssetHistoryRecord(recordID int64) (*ProfileAssetHistoryRecord, error) {
	var record ProfileAssetHistoryRecord
	err := db.QueryRow(
		`SELECT id, code_id, code_name, COALESCE(client, 'xboard_mihomo_sub'), profile_name, asset_kind, asset_path, asset_url, content_type, created_at
		 FROM profile_asset_history
		 WHERE id = ?`,
		recordID,
	).Scan(
		&record.ID,
		&record.CodeID,
		&record.CodeName,
		&record.Client,
		&record.ProfileName,
		&record.AssetKind,
		&record.AssetPath,
		&record.AssetURL,
		&record.ContentType,
		&record.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("图标历史不存在")
	}
	if err != nil {
		return nil, err
	}
	record.Client = normalizeBuildClient(record.Client)
	return &record, nil
}

func listProfileAssetHistoryRecords(codeID int, client, assetKind string, limit int) ([]ProfileAssetHistoryRecord, error) {
	if limit <= 0 || limit > 50 {
		limit = 5
	}
	client = normalizeBuildClient(client)

	rows, err := db.Query(
		`SELECT id, code_id, code_name, COALESCE(client, 'xboard_mihomo_sub'), profile_name, asset_kind, asset_path, asset_url, content_type, created_at
		 FROM profile_asset_history
		 WHERE code_id = ? AND client = ? AND asset_kind = ?
		 ORDER BY created_at DESC, id DESC
		 LIMIT ?`,
		codeID, client, assetKind, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := []ProfileAssetHistoryRecord{}
	for rows.Next() {
		var record ProfileAssetHistoryRecord
		if err := rows.Scan(
			&record.ID,
			&record.CodeID,
			&record.CodeName,
			&record.Client,
			&record.ProfileName,
			&record.AssetKind,
			&record.AssetPath,
			&record.AssetURL,
			&record.ContentType,
			&record.CreatedAt,
		); err != nil {
			continue
		}
		record.Client = normalizeBuildClient(record.Client)
		records = append(records, record)
	}
	return records, nil
}

func listOverflowProfileAssetHistoryRecords(codeID int, client, assetKind string, keep int) ([]ProfileAssetHistoryRecord, error) {
	if keep < 0 {
		keep = 0
	}
	client = normalizeBuildClient(client)

	rows, err := db.Query(
		`SELECT id, code_id, code_name, COALESCE(client, 'xboard_mihomo_sub'), profile_name, asset_kind, asset_path, asset_url, content_type, created_at
		 FROM profile_asset_history
		 WHERE code_id = ? AND client = ? AND asset_kind = ?
		 ORDER BY created_at DESC, id DESC
		 LIMIT -1 OFFSET ?`,
		codeID, client, assetKind, keep,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := []ProfileAssetHistoryRecord{}
	for rows.Next() {
		var record ProfileAssetHistoryRecord
		if err := rows.Scan(
			&record.ID,
			&record.CodeID,
			&record.CodeName,
			&record.Client,
			&record.ProfileName,
			&record.AssetKind,
			&record.AssetPath,
			&record.AssetURL,
			&record.ContentType,
			&record.CreatedAt,
		); err != nil {
			continue
		}
		record.Client = normalizeBuildClient(record.Client)
		records = append(records, record)
	}
	return records, nil
}

func deleteProfileAssetHistoryRecord(recordID int64) error {
	result, err := db.Exec(`DELETE FROM profile_asset_history WHERE id = ?`, recordID)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return fmt.Errorf("图标历史不存在")
	}
	return nil
}

func getActivationCodeByID(id int) (*ActivationCode, error) {
	ac := &ActivationCode{}
	var expiresAt sql.NullString
	var allowedProfilesJSON string
	var allowedPlatformsJSON string
	var allowedClientsJSON string

	err := db.QueryRow(
		`SELECT id, code, name, permissions, max_uses, used_count, allowed_profiles, allowed_platforms, allowed_clients, expires_at, created_at, is_active
		 FROM activation_codes WHERE id = ?`,
		id,
	).Scan(&ac.ID, &ac.Code, &ac.Name, &ac.Permissions, &ac.MaxUses, &ac.UsedCount, &allowedProfilesJSON, &allowedPlatformsJSON, &allowedClientsJSON, &expiresAt, &ac.CreatedAt, &ac.IsActive)
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
	json.Unmarshal([]byte(allowedPlatformsJSON), &ac.AllowedPlatforms)
	if ac.AllowedPlatforms == nil {
		ac.AllowedPlatforms = []string{}
	}
	json.Unmarshal([]byte(allowedClientsJSON), &ac.AllowedClients)
	if ac.AllowedClients == nil {
		ac.AllowedClients = []string{}
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

func countPendingBuildUsage(codeID int) (int, error) {
	if codeID <= 0 {
		return 0, nil
	}

	var count int
	err := db.QueryRow(
		`SELECT COUNT(*)
		 FROM build_records
		 WHERE code_id = ?
		   AND COALESCE(usage_counted, 0) = 0
		   AND status != 'completed'
		   AND COALESCE(conclusion, '') != 'trigger_failed'`,
		codeID,
	).Scan(&count)
	return count, err
}

func getBuildSubmissionAvailability(ac *ActivationCode) (bool, string) {
	canBuild, statusText := getBuildAvailability(ac)
	if !canBuild {
		return false, statusText
	}
	if ac == nil || ac.MaxUses < 0 {
		return true, statusText
	}

	pendingCount, err := countPendingBuildUsage(ac.ID)
	if err != nil {
		return false, "查询待完成打包次数失败"
	}
	if ac.UsedCount+pendingCount >= ac.MaxUses {
		return false, "激活码已达最大打包次数"
	}
	return true, statusText
}

func ensureBuildRecordSubmissionSlot(record *BuildRecord) error {
	if record == nil || record.CodeID <= 0 {
		return nil
	}

	var maxUses int
	var usedCount int
	err := db.QueryRow(
		`SELECT max_uses, used_count
		 FROM activation_codes
		 WHERE id = ?`,
		record.CodeID,
	).Scan(&maxUses, &usedCount)
	if err == sql.ErrNoRows {
		return fmt.Errorf("激活码无效")
	}
	if err != nil {
		return err
	}
	if maxUses < 0 {
		return nil
	}

	var pendingBeforeOrAtRecord int
	err = db.QueryRow(
		`SELECT COUNT(*)
		 FROM build_records
		 WHERE code_id = ?
		   AND id <= ?
		   AND COALESCE(usage_counted, 0) = 0
		   AND status != 'completed'
		   AND COALESCE(conclusion, '') != 'trigger_failed'`,
		record.CodeID,
		record.ID,
	).Scan(&pendingBeforeOrAtRecord)
	if err != nil {
		return err
	}
	if usedCount+pendingBeforeOrAtRecord > maxUses {
		return fmt.Errorf("激活码已达最大打包次数")
	}
	return nil
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

func getRemainingBuildSubmissions(ac *ActivationCode) int {
	if ac == nil || ac.MaxUses < 0 {
		return -1
	}
	pendingCount, err := countPendingBuildUsage(ac.ID)
	if err != nil {
		return getRemainingBuildUses(ac)
	}
	remaining := ac.MaxUses - ac.UsedCount - pendingCount
	if remaining < 0 {
		return 0
	}
	return remaining
}

func validateCode(code string) (*ActivationCode, error) {
	ac := &ActivationCode{}
	var expiresAt sql.NullString
	var allowedProfilesJSON string
	var allowedPlatformsJSON string
	var allowedClientsJSON string

	err := db.QueryRow(
		`SELECT id, code, name, permissions, max_uses, used_count, allowed_profiles, allowed_platforms, allowed_clients, expires_at, created_at, is_active
		 FROM activation_codes WHERE code = ? AND is_active = 1`,
		code,
	).Scan(&ac.ID, &ac.Code, &ac.Name, &ac.Permissions, &ac.MaxUses, &ac.UsedCount, &allowedProfilesJSON, &allowedPlatformsJSON, &allowedClientsJSON, &expiresAt, &ac.CreatedAt, &ac.IsActive)

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
	json.Unmarshal([]byte(allowedPlatformsJSON), &ac.AllowedPlatforms)
	if ac.AllowedPlatforms == nil {
		ac.AllowedPlatforms = []string{}
	}
	json.Unmarshal([]byte(allowedClientsJSON), &ac.AllowedClients)
	if ac.AllowedClients == nil {
		ac.AllowedClients = []string{}
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

func recordCompletedBuildUsageTx(tx *sql.Tx, codeID int) error {
	if codeID <= 0 {
		return nil
	}

	result, err := tx.Exec(
		`UPDATE activation_codes
		 SET used_count = used_count + 1
		 WHERE id = ?`,
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

	return nil
}

func consumeBuildUsageForCompletedRecord(recordID int64) error {
	if recordID <= 0 {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.Exec(
		`UPDATE build_records
		 SET usage_counted = 1
		 WHERE id = ?
		   AND COALESCE(usage_counted, 0) = 0
		   AND status = 'completed'
		   AND conclusion = 'success'`,
		recordID,
	)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return nil
	}

	var codeID int
	err = tx.QueryRow(`SELECT code_id FROM build_records WHERE id = ?`, recordID).Scan(&codeID)
	if err == sql.ErrNoRows {
		return fmt.Errorf("打包记录不存在")
	}
	if err != nil {
		return err
	}
	if err := recordCompletedBuildUsageTx(tx, codeID); err != nil {
		return err
	}
	return tx.Commit()
}

func generateCode() string {
	b := make([]byte, 8)
	rand.Read(b)
	return "XB-" + hex.EncodeToString(b)
}

func generateBuildRequestID() string {
	b := make([]byte, 12)
	rand.Read(b)
	return "BR-" + hex.EncodeToString(b)
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

func createCode(name string, maxUses int, allowedProfiles []string, allowedPlatforms []string, allowedClients []string, expiresAt string) (*ActivationCode, error) {
	code := generateCode()
	if allowedProfiles == nil {
		allowedProfiles = []string{}
	}
	if allowedPlatforms == nil {
		allowedPlatforms = []string{}
	}
	if allowedClients == nil {
		allowedClients = []string{}
	}
	profilesJSON, _ := json.Marshal(allowedProfiles)
	platformsJSON, _ := json.Marshal(allowedPlatforms)
	clientsJSON, _ := json.Marshal(allowedClients)

	normalizedExpiresAt, err := normalizeExpiresAt(expiresAt)
	if err != nil {
		return nil, err
	}

	var expiresValue interface{}
	if normalizedExpiresAt != nil {
		expiresValue = *normalizedExpiresAt
	}

	_, err = db.Exec(
		"INSERT INTO activation_codes (code, name, permissions, max_uses, allowed_profiles, allowed_platforms, allowed_clients, expires_at) VALUES (?, ?, 'user', ?, ?, ?, ?, ?)",
		code, name, maxUses, string(profilesJSON), string(platformsJSON), string(clientsJSON), expiresValue,
	)
	if err != nil {
		return nil, err
	}

	return &ActivationCode{
		Code:             code,
		Name:             name,
		Permissions:      "user",
		MaxUses:          maxUses,
		AllowedProfiles:  allowedProfiles,
		AllowedPlatforms: allowedPlatforms,
		AllowedClients:   allowedClients,
		ExpiresAt:        normalizedExpiresAt,
	}, nil
}

func updateCode(id int, name string, maxUses int, usedCount int, allowedProfiles []string, allowedPlatforms []string, allowedClients []string, expiresAt string) error {
	if allowedProfiles == nil {
		allowedProfiles = []string{}
	}
	if allowedPlatforms == nil {
		allowedPlatforms = []string{}
	}
	if allowedClients == nil {
		allowedClients = []string{}
	}
	profilesJSON, _ := json.Marshal(allowedProfiles)
	platformsJSON, _ := json.Marshal(allowedPlatforms)
	clientsJSON, _ := json.Marshal(allowedClients)

	normalizedExpiresAt, err := normalizeExpiresAt(expiresAt)
	if err != nil {
		return err
	}

	var expiresValue interface{}
	if normalizedExpiresAt != nil {
		expiresValue = *normalizedExpiresAt
	}

	result, err := db.Exec(
		"UPDATE activation_codes SET name = ?, max_uses = ?, used_count = ?, allowed_profiles = ?, allowed_platforms = ?, allowed_clients = ?, expires_at = ? WHERE id = ?",
		name, maxUses, usedCount, string(profilesJSON), string(platformsJSON), string(clientsJSON), expiresValue, id,
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

func updateCodesAllowedClients(ids []int, allowedClients []string) (int, error) {
	if len(ids) == 0 {
		return 0, fmt.Errorf("请选择要批量编辑的激活码")
	}
	if allowedClients == nil {
		allowedClients = []string{}
	}
	clientsJSON, _ := json.Marshal(allowedClients)

	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`UPDATE activation_codes SET allowed_clients = ? WHERE id = ?`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	updatedCount := 0
	seen := map[int]struct{}{}
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}

		result, err := stmt.Exec(string(clientsJSON), id)
		if err != nil {
			return 0, err
		}
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		updatedCount += int(rowsAffected)
	}
	if updatedCount == 0 {
		return 0, fmt.Errorf("未找到可更新的激活码")
	}
	return updatedCount, tx.Commit()
}

func listCodes() ([]ActivationCode, error) {
	rows, err := db.Query(
		`SELECT id, code, name, permissions, max_uses, used_count, allowed_profiles, allowed_platforms, allowed_clients, expires_at, created_at, is_active
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
		var allowedPlatformsJSON string
		var allowedClientsJSON string
		err := rows.Scan(&ac.ID, &ac.Code, &ac.Name, &ac.Permissions, &ac.MaxUses, &ac.UsedCount, &allowedProfilesJSON, &allowedPlatformsJSON, &allowedClientsJSON, &expiresAt, &ac.CreatedAt, &ac.IsActive)
		if err != nil {
			continue
		}
		json.Unmarshal([]byte(allowedProfilesJSON), &ac.AllowedProfiles)
		if ac.AllowedProfiles == nil {
			ac.AllowedProfiles = []string{}
		}
		json.Unmarshal([]byte(allowedPlatformsJSON), &ac.AllowedPlatforms)
		if ac.AllowedPlatforms == nil {
			ac.AllowedPlatforms = []string{}
		}
		json.Unmarshal([]byte(allowedClientsJSON), &ac.AllowedClients)
		if ac.AllowedClients == nil {
			ac.AllowedClients = []string{}
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

const (
	defaultClientUpdatesLimit     = 10
	maxClientUpdatesLimit         = 50
	customFeatureGroupsSettingKey = "custom_feature_groups"
)

func normalizeClientUpdatesLimit(limit int) int {
	if limit <= 0 {
		return defaultClientUpdatesLimit
	}
	if limit > maxClientUpdatesLimit {
		return maxClientUpdatesLimit
	}
	return limit
}

func getSystemSetting(key string) (string, error) {
	var value string
	err := db.QueryRow(`SELECT value FROM system_settings WHERE key = ?`, key).Scan(&value)
	if err != nil {
		return "", err
	}
	return value, nil
}

func setSystemSetting(key, value string) error {
	_, err := db.Exec(
		`INSERT INTO system_settings (key, value, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`,
		key, value,
	)
	return err
}

func getClientUpdatesLimit() int {
	value, err := getSystemSetting("client_updates_limit")
	if err != nil {
		return defaultClientUpdatesLimit
	}
	limit, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return defaultClientUpdatesLimit
	}
	return normalizeClientUpdatesLimit(limit)
}

func setClientUpdatesLimit(limit int) error {
	return setSystemSetting("client_updates_limit", strconv.Itoa(normalizeClientUpdatesLimit(limit)))
}

func normalizeCustomFeatureGroups(groups []CustomFeatureGroup) []CustomFeatureGroup {
	if groups == nil {
		return []CustomFeatureGroup{}
	}
	result := make([]CustomFeatureGroup, 0, len(groups))
	for index, group := range groups {
		name := strings.TrimSpace(group.Name)
		code := strings.TrimSpace(group.IntegrationCode)
		features := make([]string, 0, len(group.FeatureKeys))
		seen := map[string]struct{}{}
		for _, featureKey := range group.FeatureKeys {
			featureKey = strings.TrimSpace(featureKey)
			if featureKey == "" {
				continue
			}
			if _, ok := seen[featureKey]; ok {
				continue
			}
			seen[featureKey] = struct{}{}
			features = append(features, featureKey)
		}
		if name == "" && code == "" && len(features) == 0 {
			continue
		}
		if name == "" {
			name = fmt.Sprintf("自定义功能%d", index+1)
		}
		result = append(result, CustomFeatureGroup{
			Name:            name,
			IntegrationCode: code,
			FeatureKeys:     features,
		})
	}
	return result
}

func getCustomFeatureGroups() []CustomFeatureGroup {
	value, err := getSystemSetting(customFeatureGroupsSettingKey)
	if err != nil {
		return []CustomFeatureGroup{}
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return []CustomFeatureGroup{}
	}
	var groups []CustomFeatureGroup
	if err := json.Unmarshal([]byte(value), &groups); err != nil {
		return []CustomFeatureGroup{}
	}
	return normalizeCustomFeatureGroups(groups)
}

func setCustomFeatureGroups(groups []CustomFeatureGroup) error {
	normalized := normalizeCustomFeatureGroups(groups)
	payload, err := json.Marshal(normalized)
	if err != nil {
		return err
	}
	return setSystemSetting(customFeatureGroupsSettingKey, string(payload))
}
