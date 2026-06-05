package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Claims struct {
	CodeID          int      `json:"code_id"`
	CodeName        string   `json:"code_name"`
	Permissions     string   `json:"permissions"`
	AllowedProfiles []string `json:"allowed_profiles,omitempty"`
	AllowedClients  []string `json:"allowed_clients,omitempty"`
	jwt.RegisteredClaims
}

type BuildAssetDownloadClaims struct {
	RecordID int64 `json:"record_id"`
	AssetID  int64 `json:"asset_id"`
	jwt.RegisteredClaims
}

type contextKey string

const claimsKey contextKey = "claims"
const buildAssetDownloadTokenSubject = "build_asset_download"

func generateJWT(codeID int, codeName, permissions string, allowedProfiles []string, allowedClients []string) (string, error) {
	claims := &Claims{
		CodeID:          codeID,
		CodeName:        codeName,
		Permissions:     permissions,
		AllowedProfiles: allowedProfiles,
		AllowedClients:  allowedClients,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(cfg.JWTSecret))
}

func parseJWT(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		return []byte(cfg.JWTSecret), nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("无效的 Token")
	}

	return claims, nil
}

func generateBuildAssetDownloadToken(recordID, assetID int64, ttl time.Duration) (string, time.Time, error) {
	expireAt := time.Now().Add(ttl)
	claims := &BuildAssetDownloadClaims{
		RecordID: recordID,
		AssetID:  assetID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   buildAssetDownloadTokenSubject,
			ExpiresAt: jwt.NewNumericDate(expireAt),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now().Add(-10 * time.Second)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(cfg.JWTSecret))
	if err != nil {
		return "", time.Time{}, err
	}
	return tokenString, expireAt, nil
}

func parseBuildAssetDownloadToken(tokenString string) (*BuildAssetDownloadClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &BuildAssetDownloadClaims{}, func(token *jwt.Token) (interface{}, error) {
		return []byte(cfg.JWTSecret), nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*BuildAssetDownloadClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("无效的下载令牌")
	}
	if claims.Subject != buildAssetDownloadTokenSubject {
		return nil, fmt.Errorf("无效的下载令牌")
	}
	if claims.RecordID <= 0 || claims.AssetID <= 0 {
		return nil, fmt.Errorf("无效的下载令牌")
	}

	return claims, nil
}

func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth == "" || !strings.HasPrefix(auth, "Bearer ") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "未授权"})
			return
		}

		tokenStr := strings.TrimPrefix(auth, "Bearer ")
		claims, err := parseJWT(tokenStr)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "Token 无效或已过期"})
			return
		}

		ctx := context.WithValue(r.Context(), claimsKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func AdminMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := getClaims(r)
		if claims.Permissions != "admin" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]string{"error": "需要管理员权限"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func getClaims(r *http.Request) *Claims {
	return r.Context().Value(claimsKey).(*Claims)
}

// canAccessProfile 检查当前用户是否有权访问指定档案
// 管理员或 AllowedProfiles 为空（未限制）时允许所有访问
func (c *Claims) canAccessProfile(name string) bool {
	if c.Permissions == "admin" || len(c.AllowedProfiles) == 0 {
		return true
	}
	for _, p := range c.AllowedProfiles {
		if p == name {
			return true
		}
	}
	return false
}

// canAccessClient 检查当前用户是否有权访问指定客户端
// 管理员或 AllowedClients 为空（未限制）时允许所有访问
func (c *Claims) canAccessClient(client string) bool {
	if c.Permissions == "admin" || len(c.AllowedClients) == 0 {
		return true
	}
	client = normalizeBuildClient(client)
	for _, allowedClient := range c.AllowedClients {
		if normalizeBuildClient(allowedClient) == client {
			return true
		}
	}
	return false
}
