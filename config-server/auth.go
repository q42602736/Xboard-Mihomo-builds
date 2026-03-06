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
	jwt.RegisteredClaims
}

type contextKey string

const claimsKey contextKey = "claims"

func generateJWT(codeID int, codeName, permissions string, allowedProfiles []string) (string, error) {
	claims := &Claims{
		CodeID:          codeID,
		CodeName:        codeName,
		Permissions:     permissions,
		AllowedProfiles: allowedProfiles,
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
