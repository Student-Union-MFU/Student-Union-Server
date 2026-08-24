package service

import (
	"sync"
	"time"
)

/*
สถานะการต่อของ listener LISTEN/NOTIFY — ใช้ร่วมกันทั้งแชทและ SOS

ทำไมต้องมี: listener ที่หลุดแล้วต่อไม่ติด "ไม่มีอาการ" เลยจากฝั่งผู้ใช้ · ไม่มี error
ไม่มี request ไหนพัง แชทยังส่งได้และยังอ่านได้ครบ แค่ข้อความใหม่ไปถึงช้าสุดเท่ารอบ
long-poll (25 วิ) แทนที่จะเกือบทันที · อาการแบบนี้คนรายงานว่า "แอปช้า" ไม่ใช่ "แอปพัง"
และไม่มีอะไรในโปรเซสบอกได้ว่าเป็นเพราะ listener ตาย

เก็บ backoff ปัจจุบันไว้ด้วยเพราะมันคือคำตอบของ "หลุดมานานแค่ไหนแล้ว" — backoff ที่
ไต่ไปถึงเพดาน 30 วิ แปลว่าต่อไม่ติดติดกันหลายรอบ ไม่ใช่เพิ่งสะดุดครั้งเดียว

ทุกฟิลด์อยู่ใต้ mu ตัวเดียว · เขียนน้อยมาก (เฉพาะตอนต่อติด/หลุด) อ่านก็เฉพาะตอนเปิด
หน้า stats จึงไม่ต้องคิดเรื่อง contention เลย
*/
type listenerState struct {
	mu              sync.Mutex
	connected       bool
	retryIn         time.Duration
	lastConnectedAt time.Time
	lastErrorAt     time.Time
	lastError       string
}

// markConnected — เรียกเมื่อ LISTEN สำเร็จจริง ไม่ใช่แค่ dial ติด
func (s *listenerState) markConnected() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connected = true
	s.retryIn = 0
	s.lastConnectedAt = time.Now()
}

// markDropped — เรียกก่อนนอนรอ retry เพื่อให้หน้า stats เห็นว่ากำลังจะต่อใหม่ในอีกกี่วิ
func (s *listenerState) markDropped(err error, retryIn time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connected = false
	s.retryIn = retryIn
	s.lastErrorAt = time.Now()
	if err != nil {
		s.lastError = err.Error()
	}
}

// ListenerStats — รูปที่ส่งออกทาง JSON ของหน้า stats
type ListenerStats struct {
	Connected bool `json:"connected"`
	// วินาทีที่จะรอก่อนต่อใหม่ · 0 เมื่อต่ออยู่ · ไต่ถึง 30 = หลุดติดกันหลายรอบแล้ว
	RetryInSeconds float64 `json:"retry_in_seconds"`
	// nil = ยังไม่เคยต่อติดเลยตั้งแต่ boot ซึ่งต่างจาก "เคยติดแล้วเพิ่งหลุด" คนละเรื่องกัน
	LastConnectedAt *time.Time `json:"last_connected_at"`
	LastErrorAt     *time.Time `json:"last_error_at"`
	LastError       string     `json:"last_error,omitempty"`
}

func (s *listenerState) snapshot() ListenerStats {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := ListenerStats{
		Connected:      s.connected,
		RetryInSeconds: s.retryIn.Seconds(),
		LastError:      s.lastError,
	}
	if !s.lastConnectedAt.IsZero() {
		t := s.lastConnectedAt
		out.LastConnectedAt = &t
	}
	if !s.lastErrorAt.IsZero() {
		t := s.lastErrorAt
		out.LastErrorAt = &t
	}
	return out
}
