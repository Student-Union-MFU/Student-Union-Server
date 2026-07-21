// Package middleware มี HTTP middleware ที่ใช้ร่วมกันหลาย route
package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"su-server/internal/service"
)

type ctxKey string

const claimsKey ctxKey = "wbw_claims"

// WriteError ตอบ error เป็น {"error": "..."} — frontend อ่าน body.error ไปแสดงตรงๆ
// (http.Error ของ Go ส่ง plain text ซึ่ง frontend parse ไม่ได้)
func WriteError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// WriteJSON ตอบ JSON ปกติ
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// RequireAuth ตรวจ Bearer token — prefix "Bearer " ตรงตาม Express เดิม (case-sensitive)
func RequireAuth(tokens *service.WBWTokenService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			if !strings.HasPrefix(header, "Bearer ") {
				WriteError(w, http.StatusUnauthorized, "ต้องล็อกอินก่อน")
				return
			}
			claims, err := tokens.Validate(header[7:])
			if err != nil {
				WriteError(w, http.StatusUnauthorized, "token ไม่ถูกต้องหรือหมดอายุ")
				return
			}
			ctx := context.WithValue(r.Context(), claimsKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireRole ต้องใช้ต่อจาก RequireAuth เสมอ
func RequireRole(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := ClaimsFrom(r.Context())
			if claims == nil || !slices.Contains(roles, claims.Role) {
				WriteError(w, http.StatusForbidden, "ไม่มีสิทธิ์เข้าถึง")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ClaimsFrom ดึง claim ออกจาก context (nil ถ้าไม่ผ่าน RequireAuth)
func ClaimsFrom(ctx context.Context) *service.WBWClaims {
	claims, _ := ctx.Value(claimsKey).(*service.WBWClaims)
	return claims
}
