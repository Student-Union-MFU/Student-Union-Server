package service

import (
	"log/slog"
	"runtime/debug"
)

// goSafe — แตก goroutine เบื้องหลังที่ panic แล้วไม่ลากทั้งโปรเซสลงไปด้วย
//
// ทำไมต้องมี: chi middleware.Recoverer ครอบให้เฉพาะ goroutine ของ request เท่านั้น
// goroutine ที่เราแตกออกมาเอง (fire-and-forget: push, สร้างแจ้งเตือน) หลุดจากร่มนั้นทันที
// panic ตัวเดียวในนั้น = Go runtime ฆ่าโปรเซสทิ้งทั้งตัว ผู้ใช้ทุกคนหลุดพร้อมกันเพราะการ
// สแกนของคนเดียว · สาขา wbw-feedback ทำให้เส้นนี้ถี่ขึ้นมาก (แจ้งเตือนขอความเห็นยิงทุกการ
// เช็คอินครั้งแรก ~16,000 ครั้งตลอดงาน) ความน่าจะเป็นจึงไม่ใช่ทฤษฎีอีกต่อไป
//
// ทำไมกลืน panic ได้: งานในนี้เป็นงานเสริมทั้งหมด ผลลัพธ์หลัก (แถว check_in / message)
// เขียนลง DB สำเร็จแบบ synchronous ไปก่อนแล้วเสมอ ตกไปหนึ่งครั้งจึงแค่ทำให้แจ้งเตือน/push
// หายไปหนึ่งใบ ซึ่งแอปยังหาเจอเองจาก poll /me/progress · แลกกับเซิร์ฟเวอร์ล่มทั้งเครื่อง
// ไม่คุ้มกันเลย
//
// where = ชื่อจุดที่เรียก ใส่ลง log คู่กับ stack เพื่อให้ตามรอยได้ตอนเกิดจริง — recover
// ที่เงียบสนิทจะกลบบั๊กจริงไว้แทนที่จะเปิดเผย
func goSafe(where string, f func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("goroutine เบื้องหลัง panic — กันไว้ไม่ให้ล้มทั้งโปรเซส",
					"where", where, "panic", r, "stack", string(debug.Stack()))
			}
		}()
		f()
	}()
}
