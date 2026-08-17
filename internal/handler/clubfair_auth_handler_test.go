package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"su-server/internal/repository"
)

/*
เบอร์ซ้ำต้องเป็น 409 ไม่ใช่ 401

status ไม่ใช่รายละเอียดภายใน — มันคือสัญญากับแอป ฝั่ง iOS แปล 401 บนเส้นทาง
โปรไฟล์เป็น "เซสชันหมดอายุ" และ APIClient ล้าง token ทิ้งไปแล้วก่อนถึงตรงนั้น
(mapProfileError ใน SUKit/Sources/SUAuth/AuthService.swift) นักศึกษาที่กรอกเบอร์
ซ้ำจึงหลุดออกจากระบบทั้งที่ session ยังดีอยู่ 409 ทำให้แอปโชว์ข้อความจาก server
ตรงๆ แล้วอยู่หน้าเดิมให้แก้เบอร์ได้
*/
func TestAuthErrorPhoneTakenIsConflict(t *testing.T) {
	rec := httptest.NewRecorder()

	authError(rec, repository.ErrClubFairPhoneTaken)

	if rec.Code != http.StatusConflict {
		t.Fatalf("อยากได้ %d, ได้ %d", http.StatusConflict, rec.Code)
	}

	var body struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("อ่าน body ไม่ได้: %v", err)
	}
	if body.Error != "เบอร์นี้ถูกใช้ไปแล้ว" {
		t.Fatalf("ข้อความไม่ตรง: ได้ %q", body.Error)
	}
}
