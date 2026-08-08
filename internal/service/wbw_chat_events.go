package service

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ช่อง NOTIFY ช่องเดียวสำหรับแชททุกกลุ่ม — group id อยู่ใน payload
// (Postgres จำกัดจำนวนช่องต่อ connection ไม่ได้ แต่ LISTEN ทีละกลุ่มแปลว่าต้อง
// LISTEN/UNLISTEN ทุกครั้งที่มีคนเปิด-ปิดจอแชท ซึ่งแพงกว่ากรองใน process มาก)
const chatChannel = "wbw_chat"

// payload = "<groupID>:<actorUserID>" — actor ว่างได้ (เช่นระบบยิงเอง)
func chatPayload(groupID int, actor string) string {
	return strconv.Itoa(groupID) + ":" + actor
}

type chatWaiter struct {
	ch chan struct{}
	// ไม่ปลุกคนนี้ถ้า actor เป็นตัวเขาเอง — คนส่งข้อความไม่ต้องตื่นจาก long-poll
	// ของตัวเอง (เขาได้ข้อความจาก response ของ POST /messages อยู่แล้ว)
	except string
}

// ChatEvents ปลุก long-poll ที่ค้างอยู่เมื่อมีข้อความ/สถานะอ่านใหม่ในกลุ่ม
//
// ทำไมต้องมี: ถ้าไม่มี LISTEN/NOTIFY ข้อความใหม่จะถึงเครื่องช้าสุดเท่ากับรอบ poll
// (25 วิ) ซึ่งใช้ไม่ได้กับแชท · ฝั่ง Node เดิมใช้กลไกเดียวกันเป๊ะ
type ChatEvents struct {
	pool *pgxpool.Pool
	dial func(context.Context) (*pgx.Conn, error)

	mu      sync.Mutex
	waiters map[int]map[*chatWaiter]struct{}
}

func NewChatEvents(pool *pgxpool.Pool, dial func(context.Context) (*pgx.Conn, error)) *ChatEvents {
	return &ChatEvents{
		pool:    pool,
		dial:    dial,
		waiters: make(map[int]map[*chatWaiter]struct{}),
	}
}

// Start เปิด goroutine ฟัง NOTIFY ตลอดอายุ process · redial เองเมื่อหลุด
func (e *ChatEvents) Start(ctx context.Context) {
	go e.listenLoop(ctx)
}

func (e *ChatEvents) listenLoop(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		if err := e.listenOnce(ctx); err != nil && ctx.Err() == nil {
			slog.Error("chat listener dropped, retrying", "err", err, "in", backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
			// เพดาน 30 วิ — ยาวกว่านี้ข้อความจะค้างนานเกินไปหลัง DB กลับมา
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
	}
}

func (e *ChatEvents) listenOnce(ctx context.Context) error {
	conn, err := e.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())

	if _, err := conn.Exec(ctx, "LISTEN "+chatChannel); err != nil {
		return err
	}
	slog.Info("chat listener connected", "channel", chatChannel)

	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		groupID, actor, ok := parseChatPayload(n.Payload)
		if !ok {
			slog.Warn("chat notify: payload อ่านไม่ออก", "payload", n.Payload)
			continue
		}
		e.dispatch(groupID, actor)
	}
}

func parseChatPayload(payload string) (int, string, bool) {
	// actor เป็น UUID ซึ่งไม่มี ":" อยู่ในตัว จึงตัดที่ ":" ตัวแรกพอ
	idStr, actor, found := strings.Cut(payload, ":")
	if !found {
		return 0, "", false
	}
	groupID, err := strconv.Atoi(idStr)
	if err != nil {
		return 0, "", false
	}
	return groupID, actor, true
}

func (e *ChatEvents) dispatch(groupID int, actor string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for w := range e.waiters[groupID] {
		if w.except != "" && w.except == actor {
			continue
		}
		// ช่อง buffer 1 + ส่งแบบไม่บล็อก — ถ้ามีสัญญาณค้างอยู่แล้วก็ไม่ต้องส่งซ้ำ
		// (ผู้รอจะตื่นแล้ว query ใหม่อยู่ดี) และ dispatch ต้องไม่ค้างเพราะผู้รอคนเดียว
		select {
		case w.ch <- struct{}{}:
		default:
		}
	}
}

// ChatWatch — ผู้รอที่ลงทะเบียนไว้แล้ว แต่ยังไม่เริ่มบล็อก
//
// แยก "ลงทะเบียน" ออกจาก "รอ" เพื่อให้ผู้เรียกติดผู้รอได้ก่อน query แรก · NOTIFY ที่มาถึงระหว่าง
// round-trip ของ query นั้นจะตกใส่ ch (buffer 1) แล้ว Wait() คืน true ทันทีโดยไม่ต้องรอจริง
// ถ้าลงทะเบียนหลัง query เหมือนเดิม สัญญาณช่วงนั้นหายเงียบ ต้องค้างจนครบ timeout ทั้งที่มีของใหม่แล้ว
type ChatWatch struct {
	events   *ChatEvents
	groupID  int
	waiter   *chatWaiter
	released sync.Once
}

// Watch ลงทะเบียนผู้รอทันที · ต้องเรียก Release() เสมอ (defer) ไม่งั้น waiter ค้างใน map ตลอดอายุ process
//
// exceptActor คือ user id ของคนที่รออยู่ — เขาจะไม่ถูกปลุกจากการกระทำของตัวเอง
func (e *ChatEvents) Watch(groupID int, exceptActor string) *ChatWatch {
	w := &chatWaiter{ch: make(chan struct{}, 1), except: exceptActor}

	e.mu.Lock()
	if e.waiters[groupID] == nil {
		e.waiters[groupID] = make(map[*chatWaiter]struct{})
	}
	e.waiters[groupID][w] = struct{}{}
	e.mu.Unlock()

	return &ChatWatch{events: e, groupID: groupID, waiter: w}
}

// Release ถอนตัวออกจากรายชื่อผู้รอ · เรียกซ้ำได้ (sync.Once) เพื่อให้ defer ปลอดภัยเสมอ
func (c *ChatWatch) Release() {
	c.released.Do(func() {
		e := c.events
		e.mu.Lock()
		delete(e.waiters[c.groupID], c.waiter)
		// เก็บกวาด map ของกลุ่มที่ไม่มีคนรอแล้ว ไม่งั้น map โตตามจำนวนกลุ่มที่เคยมีคนรอ
		if len(e.waiters[c.groupID]) == 0 {
			delete(e.waiters, c.groupID)
		}
		e.mu.Unlock()
	})
}

// Wait ค้างไว้จนมีเหตุการณ์ของกลุ่มนี้ หรือหมดเวลา หรือ client หลุด
// คืน true เมื่อถูกปลุกจริง · false เมื่อหมดเวลา/ยกเลิก
func (c *ChatWatch) Wait(ctx context.Context, timeout time.Duration) bool {
	if timeout <= 0 {
		return false
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-c.waiter.ch:
		return true
	case <-timer.C:
		return false
	case <-ctx.Done():
		// client หลุดกลางคัน (แอปไปพัก/เน็ตหลุด) — เลิกรอทันที ไม่ query ต่อ
		return false
	}
}

// NotifyGroup ปลุกคนที่รอกลุ่มนี้อยู่ · actor จะไม่ปลุกตัวเอง
func (e *ChatEvents) NotifyGroup(ctx context.Context, groupID int, actor string) {
	if _, err := e.pool.Exec(ctx, "SELECT pg_notify($1, $2)", chatChannel, chatPayload(groupID, actor)); err != nil {
		// ปลุกไม่สำเร็จ = ข้อความยังถึงอยู่ แค่ช้าเท่ารอบ poll ถัดไป ไม่ใช่เหตุให้ request พัง
		slog.Error("chat notify failed", "group_id", groupID, "err", err)
	}
}
