package main

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/joho/godotenv"
)

//go:embed web
var webFS embed.FS

type Config struct {
	Port                string
	GithubToken         string
	GithubOwner         string
	GithubRepo          string
	GithubBranch        string
	GithubProfileBranch string
	BuildOwner          string
	BuildRepo           string
	BuildEventToken     string
	GitHubWebhookSecret string
	JWTSecret           string
	AdminPassword       string
	ConfigFilepath      string
}

var cfg Config

func loadConfig() {
	godotenv.Load()

	cfg = Config{
		Port:                getEnv("PORT", "8080"),
		GithubToken:         mustEnv("GITHUB_TOKEN"),
		GithubOwner:         mustEnv("GITHUB_OWNER"),
		GithubRepo:          mustEnv("GITHUB_REPO"),
		GithubBranch:        getEnv("GITHUB_BRANCH", "main"),
		GithubProfileBranch: getEnv("GITHUB_PROFILE_BRANCH", ""),
		BuildOwner:          getEnv("BUILD_OWNER", ""),
		BuildRepo:           getEnv("BUILD_REPO", ""),
		BuildEventToken:     getEnv("BUILD_EVENT_TOKEN", ""),
		GitHubWebhookSecret: getEnv("GITHUB_WEBHOOK_SECRET", ""),
		JWTSecret:           mustEnv("JWT_SECRET"),
		AdminPassword:       mustEnv("ADMIN_PASSWORD"),
		ConfigFilepath:      getEnv("CONFIG_FILEPATH", "assets/config/xboard.config.yaml"),
	}

	if cfg.BuildOwner == "" {
		cfg.BuildOwner = cfg.GithubOwner
	}
	if cfg.BuildRepo == "" {
		cfg.BuildRepo = "Xboard-Mihomo-builds"
	}
	if cfg.GithubProfileBranch == "" {
		cfg.GithubProfileBranch = cfg.GithubBranch
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("必需的环境变量 %s 未设置", key)
	}
	return v
}

func main() {
	loadConfig()
	initDB()

	gh := NewGitHubClient(
		cfg.GithubToken,
		cfg.GithubOwner,
		cfg.GithubRepo,
		cfg.GithubBranch,
	)
	profileGH := NewGitHubClient(
		cfg.GithubToken,
		cfg.GithubOwner,
		cfg.GithubRepo,
		cfg.GithubProfileBranch,
	)
	h := NewHandlers(gh, profileGH)

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// 认证接口（公开）
	r.Post("/api/auth/login", h.Login)
	r.Post("/api/auth/admin", h.AdminLogin)
	r.Post("/api/internal/build-events/bind", h.InternalBindBuildRun)
	r.Post("/api/internal/build-events/complete", h.InternalCompleteBuildRun)
	r.Post("/api/internal/github/webhook", h.HandleGitHubWebhook)
	r.Get("/download/build/records/{id}/assets/{assetID}", h.DownloadBuildRecordAssetByToken)
	r.Get("/profile-assets/history/{id}", h.GetProfileAsset)

	// 需要登录的 API
	r.Group(func(r chi.Router) {
		r.Use(AuthMiddleware)

		r.Get("/api/auth/me", h.GetCurrentUserInfo)
		r.Get("/api/profiles", h.ListProfiles)
		r.Get("/api/profiles/{name}", h.GetProfile)
		r.Post("/api/custom-features/ui-colors/load", h.GetPublicUIColorCustomConfig)
		r.Put("/api/custom-features/ui-colors", h.SavePublicUIColorCustomConfig)
		r.Put("/api/profiles/{name}", h.SaveProfile)
		r.Post("/api/profiles/{name}/assets/{kind}", h.UploadProfileAsset)
		r.Get("/api/profile-assets/history", h.ListProfileAssetHistory)
		r.Delete("/api/profiles/{name}", h.DeleteProfile)
		r.Get("/api/branches", h.ListBranches)
		r.Post("/api/build/trigger", h.TriggerBuild)
		r.Get("/api/build/history", h.GetBuildHistory)
		r.Get("/api/build/records", h.ListBuildRecords)
		r.Post("/api/build/records/{id}/cancel", h.CancelBuildRecord)
		r.Delete("/api/build/records/{id}", h.DeleteBuildRecord)
		r.Get("/api/build/records/{id}/assets", h.GetBuildRecordAssets)
		r.Post("/api/build/records/{id}/download/{assetID}/link", h.CreateBuildRecordAssetDownloadLink)
		r.Get("/api/build/records/{id}/download/{assetID}", h.DownloadBuildRecordAsset)
		r.Get("/api/build/queue", h.GetBuildQueue)
		r.Get("/api/build/status", h.GetBuildStatus)
		r.Get("/api/client-updates", h.GetClientUpdates)
	})

	// 管理员 API
	r.Group(func(r chi.Router) {
		r.Use(AuthMiddleware)
		r.Use(AdminMiddleware)

		r.Get("/api/admin/codes", h.ListCodes)
		r.Post("/api/admin/codes", h.CreateCode)
		r.Put("/api/admin/codes/{id}", h.UpdateCode)
		r.Delete("/api/admin/codes/{id}", h.DeleteCode)
		r.Get("/api/admin/logs", h.GetAuditLogs)
		r.Get("/api/admin/settings", h.GetSystemSettings)
		r.Put("/api/admin/settings", h.SaveSystemSettings)
	})

	// 静态文件（前端页面）
	webContent, _ := fs.Sub(webFS, "web")
	fileServer := http.FileServer(http.FS(webContent))
	r.Handle("/*", fileServer)

	addr := ":" + cfg.Port
	fmt.Printf("NexGen Client 配置服务器已启动: http://localhost%s\n", addr)
	log.Fatal(http.ListenAndServe(addr, r))
}
