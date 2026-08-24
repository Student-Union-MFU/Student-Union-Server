// Package config is used for initializing Database and other configs
package config

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// envInt อ่าน env เป็น int · ไม่มี/ผิดรูปแบบ = ใช้ค่า default
func envInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil && v > 0 {
		return v
	}
	return def
}

func dsn() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s",
		os.Getenv("DB_USER"),
		os.Getenv("DB_PASS"),
		os.Getenv("DB_HOST"),
		os.Getenv("DB_PORT"),
		os.Getenv("DB_NAME"),
	)
}

// ConnectDB is GONE ON PURPOSE. Do not bring it back.
//
// It returned a single shared *pgx.Conn, and pgx documents that type as "not
// safe for concurrent usage" (conn.go) with no internal lock and no busy-conn
// error to catch the mistake — concurrent requests genuinely interleave on the
// wire. It backed the four oldest SU repositories (event, user, step,
// leaderboard) until every one of them moved onto the pool below, which is why
// nothing calls it any more.
//
// Anything serving HTTP wants ConnectPool. A one-shot CLI wants ConnectPool too
// (cmd/createadmin and cmd/createclubfairstaff both use it) — there is no case
// left in this repo that needs a bare connection except LISTEN, immediately
// below, which needs it for the opposite reason.

// ConnectListener opens a DEDICATED connection for Postgres LISTEN/NOTIFY.
//
// It must not come from the pool: a listening connection blocks inside
// WaitForNotification for as long as nothing is published, so borrowing one from
// the pool would pin a slot forever and starve HTTP handlers. The chat long-poll
// owns this connection and redials it itself when the link drops.
func ConnectListener(ctx context.Context) (*pgx.Conn, error) {
	return pgx.Connect(ctx, dsn())
}

// ConnectPool returns a connection POOL.
//
// Use this for anything serving HTTP: a single *pgx.Conn is NOT safe for
// concurrent use, so two requests hitting it at the same time can interleave on
// the wire and corrupt each other's results. Every repository in the server now
// takes this pool — SU, WBW and Club Fair alike — so it is the one place a
// connection ceiling has to be reasoned about.
func ConnectPool(ctx context.Context) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn())
	if err != nil {
		return nil, err
	}

	// จำกัดจำนวน connection ให้พอดีกับที่ Postgres รับไหว — งานนี้คนเข้าพร้อมกันหลักพัน
	// แต่ 2000 users ≠ 2000 db connections · pool คิว request ที่เกินไว้ ไม่เปิด conn
	// เพิ่มไม่จำกัดจน Postgres ล้ม (max_connections ปริยาย 100) · ปรับผ่าน env ได้
	//
	// ตั้ง DB_MAX_CONNS ให้ (จำนวน replica ของ backend) × ค่านี้ ≤ Postgres max_connections
	// เผื่อ superuser/สำรองไว้ด้วย เช่น 4 replica × 20 = 80 < 100
	cfg.MaxConns = int32(envInt("DB_MAX_CONNS", 20))
	cfg.MinConns = int32(envInt("DB_MIN_CONNS", 2)) // อุ่น conn ไว้บ้าง กัน latency ตอน burst แรก
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.HealthCheckPeriod = 1 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}
