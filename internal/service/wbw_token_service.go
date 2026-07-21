package service

import (
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

/*
Token ของ WBW แยกจาก JWTService เดิม เพราะ claim คนละแบบ:
JWTService ใช้ user_id เป็น int (users.id ของ su-server)
แต่ WBW ใช้ app_user.user_id เป็น UUID + ต้องมี role กับ username ใน claim

รูปแบบ claim ตรงกับ Express เดิม ({sub, role, username}, HS256, อายุ 30 วัน)
token ที่ออกจาก Express เดิมจึงยังใช้ต่อได้ ถ้า JWT_SECRET เป็นค่าเดียวกัน
*/

const wbwTokenTTL = 30 * 24 * time.Hour

type WBWClaims struct {
	Role     string `json:"role"`
	Username string `json:"username"`
	jwt.RegisteredClaims
}

type WBWTokenService struct {
	secret []byte
}

func NewWBWTokenService() *WBWTokenService {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		// ตรงกับ fallback ของ Express เดิม — ⚠ ต้องตั้ง JWT_SECRET จริงตอน deploy
		secret = "change_me_in_production"
	}
	return &WBWTokenService{secret: []byte(secret)}
}

// Sign ออก token ให้ user — sub = user_id (UUID)
func (s *WBWTokenService) Sign(userID, role, username string) (string, error) {
	claims := WBWClaims{
		Role:     role,
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(wbwTokenTTL)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.secret)
}

func (s *WBWTokenService) Validate(tokenString string) (*WBWClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &WBWClaims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.secret, nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*WBWClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}
	return claims, nil
}
