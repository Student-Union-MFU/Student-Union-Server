package service

import (
	"testing"
	"time"
)

// goSafe ต้องกลืน panic ไว้ในตัวมันเอง ไม่ปล่อยให้ Go runtime ฆ่าโปรเซส
//
// เทสนี้พิสูจน์ตรงๆ ไม่ได้ว่า "โปรเซสไม่ตาย" (ถ้ามันตายจริง เทสทั้ง binary ก็ตายไปด้วย —
// ซึ่งก็คือผลลัพธ์ที่ต้องการพอดี: ลบ recover ออกแล้วรันเทสนี้ ทั้งแพ็กเกจจะพังทันทีพร้อม
// "panic: ระเบิด" ไม่ใช่ FAIL เฉยๆ) ที่ assert ตรงๆ ได้คือ goroutine หลัง panic ยังเดินต่อได้
// และ defer ที่อยู่ใน f ทำงานครบก่อน panic จะไหลไปถึง recover ซึ่งเป็นเงื่อนไขที่ทำให้
// wg.Done() ใน sendChat/sendUser ไม่ค้าง
func TestGoSafeRecoversPanicAndRunsDeferredCleanup(t *testing.T) {
	cleaned := make(chan struct{}, 1)

	goSafe("test.panic", func() {
		defer func() { cleaned <- struct{}{} }()
		panic("ระเบิด")
	})

	select {
	case <-cleaned:
	case <-time.After(2 * time.Second):
		t.Fatal("defer ใน f ต้องทำงานก่อน panic จะไหลไปถึง recover — ไม่งั้น wg.Wait() ค้างถาวร")
	}

	// goroutine ตัวถัดไปต้องยังแตกได้ตามปกติ = โปรเซสรอดจริง ไม่ได้แค่ครั้งเดียวบังเอิญ
	done := make(chan struct{}, 1)
	goSafe("test.normal", func() { done <- struct{}{} })
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("goSafe ต้องรัน f ตามปกติเมื่อไม่มี panic")
	}
}
