// สร้างบัญชีผู้ดูแลคนแรก (bootstrap) — แทน scripts/create-admin.js ของ Express เดิม
//
// ใช้: go run cmd/createadmin/main.go <username> <password> [display_name]
// role จะเป็น admin เสมอ ถ้ามี username นี้อยู่แล้วจะเปลี่ยนรหัสผ่านให้แทน
package main

import (
	"context"
	"fmt"
	"os"

	"su-server/config"

	"github.com/joho/godotenv"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	godotenv.Load()

	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: createadmin <username> <password> [display_name]")
		os.Exit(1)
	}
	username, password := os.Args[1], os.Args[2]
	var displayName *string
	if len(os.Args) > 3 {
		displayName = &os.Args[3]
	}

	if len(password) < 8 {
		fmt.Fprintln(os.Stderr, "password ต้องยาวอย่างน้อย 8 ตัว")
		os.Exit(1)
	}

	// cost 10 ให้ตรงกับที่ service ใช้
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 10)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hash failed:", err)
		os.Exit(1)
	}

	ctx := context.Background()
	pool, err := config.ConnectPool(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "db connect failed:", err)
		os.Exit(1)
	}
	defer pool.Close()

	var userID string
	err = pool.QueryRow(ctx, `
		INSERT INTO wbw_user (username, password_hash, role, display_name)
		VALUES ($1, $2, 'admin', $3)
		ON CONFLICT (username) DO UPDATE
		  SET password_hash = EXCLUDED.password_hash,
		      role          = 'admin',
		      display_name  = COALESCE(EXCLUDED.display_name, wbw_user.display_name)
		RETURNING user_id::text`,
		username, string(hash), displayName).Scan(&userID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "create admin failed:", err)
		os.Exit(1)
	}

	fmt.Printf("admin พร้อมใช้งาน: %s (user_id=%s)\n", username, userID)
}
