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
