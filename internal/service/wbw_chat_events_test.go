package service

import (
	"context"
	"testing"
	"time"
)

// จังหวะที่ทำให้เกิดบั๊กเดิม: NOTIFY มาถึงระหว่าง round-trip ของ query แรก ก่อนผู้เรียกจะเริ่มรอ
// โค้ดเดิม (WaitForGroup ที่ลงทะเบียนตอนเริ่มรอ) ทำสัญญาณช่วงนี้หายเงียบ แล้วค้างจนครบ timeout
func TestChatWatchKeepsSignalThatArrivesBeforeWait(t *testing.T) {
	e := NewChatEvents(nil, nil)

	w := e.Watch(7, "me")
	defer w.Release()

	e.dispatch(7, "someone-else") // สัญญาณมาถึงก่อน Wait()

	if !w.Wait(context.Background(), 50*time.Millisecond) {
		t.Fatal("สัญญาณที่มาถึงก่อน Wait() หายไป — lost wake-up กลับมาแล้ว")
	}
}

// ไม่มีสัญญาณ = ต้องคืน false ตอนหมดเวลา ไม่ใช่ค้างตลอดไป (หมดเวลา = ตอบ 200 ว่าง ไม่ใช่ error)
func TestChatWatchTimesOutWithoutSignal(t *testing.T) {
	e := NewChatEvents(nil, nil)

	w := e.Watch(7, "me")
	defer w.Release()

	if w.Wait(context.Background(), 20*time.Millisecond) {
		t.Fatal("ไม่มีสัญญาณแต่ Wait() บอกว่าถูกปลุก")
	}
}

// การกระทำของตัวเองต้องไม่ปลุก long-poll ของตัวเอง — ไม่งั้น heartbeat ของเราเองก็เตะเราตื่น
func TestChatWatchIgnoresOwnAction(t *testing.T) {
	e := NewChatEvents(nil, nil)

	w := e.Watch(7, "me")
	defer w.Release()

	e.dispatch(7, "me")

	if w.Wait(context.Background(), 20*time.Millisecond) {
		t.Fatal("ถูกปลุกจากการกระทำของตัวเอง")
	}
}

// เหตุการณ์ของกลุ่มอื่นต้องไม่ข้ามมาปลุก
func TestChatWatchIgnoresOtherGroup(t *testing.T) {
	e := NewChatEvents(nil, nil)

	w := e.Watch(7, "me")
	defer w.Release()

	e.dispatch(8, "someone-else")

	if w.Wait(context.Background(), 20*time.Millisecond) {
		t.Fatal("ถูกปลุกด้วยเหตุการณ์ของกลุ่มอื่น")
	}
}

// client หลุดกลางคัน — เลิกรอทันที ไม่รอจนครบ timeout
func TestChatWatchStopsWhenContextCanceled(t *testing.T) {
	e := NewChatEvents(nil, nil)

	w := e.Watch(7, "me")
	defer w.Release()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	if w.Wait(ctx, 5*time.Second) {
		t.Fatal("ctx ถูกยกเลิกแล้วแต่ Wait() บอกว่าถูกปลุก")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("รอจนเกือบครบ timeout ทั้งที่ ctx ยกเลิกแล้ว (%v)", elapsed)
	}
}

// Release ต้องเก็บกวาด map ของกลุ่มให้หมด ไม่งั้น map โตตามจำนวนกลุ่มที่เคยมีคนรอ
// และต้องเรียกซ้ำได้ เพราะ Sync ใช้ defer ทุกเส้นทางออก
func TestChatWatchReleaseCleansUpAndIsIdempotent(t *testing.T) {
	e := NewChatEvents(nil, nil)

	w := e.Watch(7, "me")
	if len(e.waiters) != 1 {
		t.Fatalf("Watch() ควรลงทะเบียนผู้รอทันที ได้ %d กลุ่ม", len(e.waiters))
	}

	w.Release()
	w.Release() // ซ้ำต้องไม่ panic และไม่ทำให้ state เพี้ยน

	if len(e.waiters) != 0 {
		t.Fatalf("Release() ไม่ได้เก็บกวาด map เหลือ %d กลุ่ม", len(e.waiters))
	}
}

// ผู้รอหลายคนในกลุ่มเดียวกันต้องถูกปลุกพร้อมกัน ยกเว้นคนที่เป็น actor เอง
func TestChatDispatchWakesEveryoneExceptActor(t *testing.T) {
	e := NewChatEvents(nil, nil)

	actor := e.Watch(7, "a")
	defer actor.Release()
	other := e.Watch(7, "b")
	defer other.Release()

	e.dispatch(7, "a")

	if actor.Wait(context.Background(), 20*time.Millisecond) {
		t.Fatal("actor ถูกปลุกจากการกระทำของตัวเอง")
	}
	if !other.Wait(context.Background(), 20*time.Millisecond) {
		t.Fatal("สมาชิกคนอื่นไม่ถูกปลุก")
	}
}
