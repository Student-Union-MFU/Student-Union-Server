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

func ConnectDB() (*pgx.Conn, error) {
	conn, err := pgx.Connect(context.Background(), dsn())

	if err != nil {
		return  nil, err
	}

	return conn, nil
}

// ConnectPool returns a connection POOL.
//
// Prefer this over ConnectDB for anything serving HTTP: a single *pgx.Conn is
// NOT safe for concurrent use, so two requests hitting it at the same time can
// interleave on the wire and corrupt each other's results. The WBW handlers use
// this pool. The older repositories still take a *pgx.Conn and should be
// migrated to the pool too.
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


