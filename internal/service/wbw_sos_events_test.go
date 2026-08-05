package service

import (
	"context"
	"testing"
	"time"
)

func TestSOSEventsWaitReturnsFalseOnTimeout(t *testing.T) {
	e := NewSOSEvents(nil, nil)
	ctx := context.Background()
	start := time.Now()
	if e.Wait(ctx, 100*time.Millisecond) {
		t.Fatal("ไม่มีเหตุการณ์ ต้องคืน false")
	}
	if time.Since(start) < 90*time.Millisecond {
		t.Fatal("ต้องรอจนหมดเวลาจริง ไม่ใช่คืนทันที")
	}
}

func TestSOSEventsWakesEveryParkedWaiter(t *testing.T) {
	e := NewSOSEvents(nil, nil)
	ctx := context.Background()

	woke := make(chan bool, 3)
	for i := 0; i < 3; i++ {
		go func() { woke <- e.Wait(ctx, 2*time.Second) }()
	}
	time.Sleep(50 * time.Millisecond) // ให้ทุกตัวเข้าไปจอดก่อน
	e.dispatch()

	for i := 0; i < 3; i++ {
		select {
		case ok := <-woke:
			if !ok {
				t.Fatal("ต้องถูกปลุก ไม่ใช่หมดเวลา")
			}
		case <-time.After(time.Second):
			t.Fatal("ตัวที่จอดอยู่ไม่ถูกปลุก")
		}
	}
}

func TestSOSEventsWaitWithZeroTimeoutReturnsImmediately(t *testing.T) {
	e := NewSOSEvents(nil, nil)
	if e.Wait(context.Background(), 0) {
		t.Fatal("timeout 0 ต้องคืน false ทันที")
	}
}

func TestReconnectBackoffNextDoubles(t *testing.T) {
	b := newReconnectBackoff()

	if b.next() != time.Second {
		t.Fatal("ครั้งแรกต้อง 1 วิ")
	}
	if b.next() != 2*time.Second {
		t.Fatal("ครั้งที่สองต้อง 2 วิ")
	}
	if b.next() != 4*time.Second {
		t.Fatal("ครั้งที่สามต้อง 4 วิ")
	}
}

func TestReconnectBackoffCapAt30Seconds(t *testing.T) {
	b := newReconnectBackoff()

	// เพิ่มไปเรื่อย ๆ จนถึง 30 วิ
	for i := 0; i < 10; i++ {
		b.next()
	}

	// ต้องค้างที่ 30 วิ ไม่เพิ่มต่อ
	if b.next() != 30*time.Second {
		t.Fatal("ต้องค้าง 30 วิ")
	}
	if b.next() != 30*time.Second {
		t.Fatal("ต้องค้าง 30 วิ ต่อไป")
	}
}

func TestReconnectBackoffResetAfterFailures(t *testing.T) {
	b := newReconnectBackoff()

	// ล้มติดกันสองครั้ง — ต้อง 1s แล้ว 2s
	if b.next() != time.Second {
		t.Fatal("ล้มครั้งแรก 1 วิ")
	}
	if b.next() != 2*time.Second {
		t.Fatal("ล้มครั้งที่สอง 2 วิ")
	}

	// เรียก reset เมื่อต่อติด
	b.reset()

	// ล้มครั้งต่อไปต้องเริ่มจาก 1 วิ
	if b.next() != time.Second {
		t.Fatal("หลังจาก reset ต้อง 1 วิ")
	}
	if b.next() != 2*time.Second {
		t.Fatal("หลังจาก reset ครั้งที่สองต้อง 2 วิ")
	}
}

func TestReconnectBackoffResetOnFreshInstance(t *testing.T) {
	b := newReconnectBackoff()
	b.reset()

	// reset ในอินสแตนซใหม่ไม่ต้องทำอะไร
	if b.next() != time.Second {
		t.Fatal("fresh instance หลัง reset ต้อง 1 วิ")
	}
}
