package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ช่องแยกจากแชทโดยตั้งใจ — SOS มีคนฟังน้อยกว่ามากและห้ามถูกทราฟฟิกแชทกลบ
// ถ้าใช้ช่องเดียวกัน long-poll ของ SOS จะตื่นทุกครั้งที่มีใครพิมพ์ข้อความในทุกกลุ่ม
const sosChannel = "wbw_sos"

type SOSEvents struct {
	pool *pgxpool.Pool
	dial func(context.Context) (*pgx.Conn, error)

	mu      sync.Mutex
	waiters map[chan struct{}]struct{}
}

func NewSOSEvents(pool *pgxpool.Pool, dial func(context.Context) (*pgx.Conn, error)) *SOSEvents {
	return &SOSEvents{pool: pool, dial: dial, waiters: make(map[chan struct{}]struct{})}
}

func (e *SOSEvents) Start(ctx context.Context) { go e.listenLoop(ctx) }

func (e *SOSEvents) listenLoop(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		resetBackoff := func() { backoff = time.Second }
		if err := e.listenOnce(ctx, resetBackoff); err != nil && ctx.Err() == nil {
			slog.Error("sos listener หลุด กำลังต่อใหม่", "err", err, "in", backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
	}
}

func (e *SOSEvents) listenOnce(ctx context.Context, onConnected func()) error {
	conn, err := e.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())

	if _, err := conn.Exec(ctx, "LISTEN "+sosChannel); err != nil {
		return err
	}
	// เมื่อ LISTEN สำเร็จ ถือว่าต่อแล้ว — รีเซต backoff เพื่อให้ disconnect ต่อไปเริ่มจาก 1 วินาที
	if onConnected != nil {
		onConnected()
	}
	slog.Info("sos listener ต่อแล้ว", "channel", sosChannel)

	for {
		if _, err := conn.WaitForNotification(ctx); err != nil {
			return err
		}
		e.dispatch()
	}
}

// dispatch — ปลุกทุกตัวที่จอดอยู่ · ไม่มี payload ให้กรอง เพราะทุกคนที่รออยู่
// ต้อง query ใหม่อยู่ดี (สิทธิ์การมองเห็นคิดจากบทบาทของแต่ละคน ไม่ใช่จากเคส)
func (e *SOSEvents) dispatch() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for ch := range e.waiters {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (e *SOSEvents) Wait(ctx context.Context, timeout time.Duration) bool {
	if timeout <= 0 {
		return false
	}
	ch := make(chan struct{}, 1)

	e.mu.Lock()
	e.waiters[ch] = struct{}{}
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.waiters, ch)
		e.mu.Unlock()
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ch:
		return true
	case <-timer.C:
		return false
	case <-ctx.Done():
		return false
	}
}

// Notify — ยิงสัญญาณให้ทุก process ที่ LISTEN อยู่ (ไม่ใช่แค่เครื่องนี้)
func (e *SOSEvents) Notify(ctx context.Context) error {
	if e.pool == nil {
		return nil
	}
	_, err := e.pool.Exec(ctx, "SELECT pg_notify($1, '')", sosChannel)
	return err
}
