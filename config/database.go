// Package config is used for initializing Database and other configs
package config

import (
	"context"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

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
	pool, err := pgxpool.New(ctx, dsn())
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}


