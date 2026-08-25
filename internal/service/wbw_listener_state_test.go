package service

import (
	"errors"
	"testing"
	"time"
)

/*
listenerState คือสิ่งเดียวที่บอกได้ว่า listener ตายอยู่ · ถ้ามันโกหก อาการ "แชทช้า
25 วิ" จะไม่มีที่มาให้สืบเหมือนเดิม จึงต้องเทสต์ว่าค่าที่รายงานตรงกับความจริง
*/

// ยังไม่เคยต่อติดเลยตั้งแต่ boot — ต่างจาก "เคยติดแล้วหลุด" คนละเรื่องกัน และหน้า stats
// ต้องแยกออก เพราะอย่างแรกแปลว่า config พัง อย่างหลังแปลว่าเน็ตสะดุด
func TestListenerStateStartsHavingNeverConnected(t *testing.T) {
	var s listenerState
	got := s.snapshot()

	if got.Connected {
		t.Error("ยังไม่ได้ต่อเลย แต่รายงานว่าต่ออยู่")
	}
	if got.LastConnectedAt != nil {
		t.Errorf("ไม่เคยต่อติด LastConnectedAt ต้องเป็น nil ไม่ใช่ %v", got.LastConnectedAt)
	}
	if got.LastErrorAt != nil {
		t.Errorf("ยังไม่มี error LastErrorAt ต้องเป็น nil ไม่ใช่ %v", got.LastErrorAt)
	}
	if got.LastError != "" {
		t.Errorf("ยังไม่มี error แต่ได้ %q", got.LastError)
	}
}

func TestListenerStateReportsConnectedOnceConnected(t *testing.T) {
	var s listenerState
	s.markConnected()
	got := s.snapshot()

	if !got.Connected {
		t.Error("ต่อติดแล้วแต่รายงานว่าหลุด")
	}
	if got.LastConnectedAt == nil {
		t.Fatal("ต่อติดแล้วต้องมีเวลาที่ต่อติด")
	}
	if got.RetryInSeconds != 0 {
		t.Errorf("ต่ออยู่ไม่ต้องรอ retry แต่ได้ %v วิ", got.RetryInSeconds)
	}
}

// backoff ปัจจุบันคือคำตอบของ "หลุดมานานแค่ไหนแล้ว" · ต้องออกมาเป็นวินาที ไม่ใช่ nanosecond
func TestListenerStateReportsRetryDelayWhenDropped(t *testing.T) {
	var s listenerState
	s.markDropped(errors.New("ต่อไม่ติด"), 4*time.Second)
	got := s.snapshot()

	if got.Connected {
		t.Error("หลุดแล้วแต่ยังรายงานว่าต่ออยู่ — อาการที่ state ตัวนี้มีไว้จับพอดี")
	}
	if got.RetryInSeconds != 4 {
		t.Errorf("ต้องได้ 4 วินาที ไม่ใช่ %v", got.RetryInSeconds)
	}
	if got.LastError != "ต่อไม่ติด" {
		t.Errorf("ต้องเก็บ error ล่าสุดไว้ ได้ %q", got.LastError)
	}
	if got.LastErrorAt == nil {
		t.Error("มี error แล้วต้องมีเวลาที่เกิด")
	}
}

// ต่อติดใหม่ต้องล้าง backoff ทิ้ง ไม่งั้นหน้า stats จะโชว์ "กำลังจะต่อใหม่ในอีก 30 วิ"
// ค้างอยู่ทั้งที่ต่อติดแล้ว
func TestListenerStateClearsBackoffOnReconnect(t *testing.T) {
	var s listenerState
	s.markDropped(errors.New("หลุด"), 30*time.Second)
	s.markConnected()
	got := s.snapshot()

	if !got.Connected {
		t.Error("ต่อติดใหม่แล้วแต่ยังรายงานว่าหลุด")
	}
	if got.RetryInSeconds != 0 {
		t.Errorf("ต่อติดแล้ว retryIn ต้องถูกล้างเป็น 0 ไม่ใช่ %v", got.RetryInSeconds)
	}
}

// error ตัวเก่ากับเวลาที่หลุดล่าสุดต้องอยู่ต่อหลังต่อติดใหม่ — คือหลักฐานว่าเพิ่งหลุดไป
// ไม่ใช่ต่อติดมาตลอด · listener ที่หลุด ๆ ติด ๆ ทุกนาทีต้องดูออกจากหน้า stats
func TestListenerStateKeepsDropHistoryAfterReconnect(t *testing.T) {
	var s listenerState
	s.markDropped(errors.New("connection reset"), 2*time.Second)
	s.markConnected()
	got := s.snapshot()

	if got.LastError != "connection reset" {
		t.Errorf("ประวัติ error หายไปหลังต่อติดใหม่ ได้ %q", got.LastError)
	}
	if got.LastErrorAt == nil {
		t.Error("เวลาที่หลุดล่าสุดหายไปหลังต่อติดใหม่")
	}
}

// markDropped ตอน ctx ถูกยกเลิก (ปิดเซิร์ฟเวอร์) ส่ง err เป็น nil ได้ · ต้องไม่ panic
// และต้องไม่ล้างข้อความ error เดิมที่มีประโยชน์กว่าทิ้ง
func TestListenerStateNilErrorDoesNotClearThePreviousOne(t *testing.T) {
	var s listenerState
	s.markDropped(errors.New("อันนี้คือสาเหตุจริง"), time.Second)
	s.markDropped(nil, time.Second)

	if got := s.snapshot().LastError; got != "อันนี้คือสาเหตุจริง" {
		t.Errorf("ข้อความ error เดิมถูกล้างทิ้งโดย err ที่เป็น nil ได้ %q", got)
	}
}

// snapshot คืน "สำเนา" ของเวลา ไม่ใช่ pointer เข้าไปใน struct · ถ้าคืน pointer ตรง ๆ
// ผู้เรียกจะอ่านค่าที่ listener แก้อยู่นอก mu ซึ่งคือ data race
func TestListenerStateSnapshotReturnsACopy(t *testing.T) {
	var s listenerState
	s.markConnected()

	first := s.snapshot()
	before := *first.LastConnectedAt

	time.Sleep(2 * time.Millisecond)
	s.markConnected() // ต่อติดใหม่ เวลาเปลี่ยน

	if !first.LastConnectedAt.Equal(before) {
		t.Error("snapshot เก่าเปลี่ยนตามไปด้วย — คืน pointer เข้า struct จริงอยู่")
	}
}
