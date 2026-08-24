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

// reconnectBackoff — จังหวะต่อใหม่ของ listener
//
// แยกออกมาเป็นชนิดของตัวเองเพราะ listenLoop เองเทสยาก (ต้องมี Postgres จริง)
// แต่ "ล้มติดกันต้องถอยเพิ่ม ล้มหลังจากต่อติดต้องเริ่มใหม่ที่ 1 วิ" คือกฎที่พังเงียบได้
// และเป็นกฎเดียวกับที่เคยพังมาแล้วครั้งหนึ่ง (backoff reset ในโค้ดเดิมค้างชิด)
type reconnectBackoff struct {
	d time.Duration
}

func newReconnectBackoff() *reconnectBackoff {
	return &reconnectBackoff{d: time.Second}
}

// next — ส่งค่าปัจจุบัน แล้วเพิ่มเป็นสองเท่า เพดาน 30 วิ
func (b *reconnectBackoff) next() time.Duration {
	result := b.d
	if b.d < 30*time.Second {
		b.d *= 2
		if b.d > 30*time.Second {
			b.d = 30*time.Second
		}
	}
	return result
}

// reset — กลับไป 1 วิ — เรียกเมื่อต่อติดจริง
func (b *reconnectBackoff) reset() {
	b.d = time.Second
}

type SOSEvents struct {
	pool *pgxpool.Pool
	dial func(context.Context) (*pgx.Conn, error)

	mu      sync.Mutex
	waiters map[chan struct{}]struct{}

	// สถานะ listener — อ่านจากหน้า stats เท่านั้น (ดู listenerState)
	listener listenerState
}

func NewSOSEvents(pool *pgxpool.Pool, dial func(context.Context) (*pgx.Conn, error)) *SOSEvents {
	return &SOSEvents{pool: pool, dial: dial, waiters: make(map[chan struct{}]struct{})}
}

func (e *SOSEvents) Start(ctx context.Context) { go e.listenLoop(ctx) }

func (e *SOSEvents) listenLoop(ctx context.Context) {
	backoff := newReconnectBackoff()
	for ctx.Err() == nil {
		onConnected := func() {
			backoff.reset()
			e.listener.markConnected()
		}
		if err := e.listenOnce(ctx, onConnected); err != nil && ctx.Err() == nil {
			d := backoff.next()
			slog.Error("sos listener หลุด กำลังต่อใหม่", "err", err, "in", d)
			// บันทึกก่อนนอน ด้วยเหตุผลเดียวกับ chat listener
			e.listener.markDropped(err, d)
			select {
			case <-time.After(d):
			case <-ctx.Done():
				return
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

// SOSEventsStats — คนที่จอดรอ SOS long-poll อยู่ กับสถานะ listener
//
// ไม่มี "กลุ่ม" เหมือนแชทเพราะ SOS ยิงถึงทุกคนที่รออยู่ (ดู dispatch) · waiter หนึ่งตัว
// = หนึ่ง goroutine ค้างใน GET /wbw/me/sos/active หรือ GET /wbw/staff/sos
//
// ⚠ listener ตัวนี้สำคัญกว่าของแชท: SOS ที่ช้าไป 25 วินาทีคือคนเจ็บที่รออยู่จริง
// และ listener ที่หลุดเงียบ ๆ ทำให้เกิดแบบนั้นโดยไม่มี error สักบรรทัด
type SOSEventsStats struct {
	Waiters  int           `json:"waiters"`
	Listener ListenerStats `json:"listener"`
}

func (e *SOSEvents) Stats() SOSEventsStats {
	e.mu.Lock()
	out := SOSEventsStats{Waiters: len(e.waiters)}
	e.mu.Unlock()

	out.Listener = e.listener.snapshot()
	return out
}
