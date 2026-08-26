package repository

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

/*
แปลง unique violation บนเบอร์โทรให้เป็น error ที่ handler อ่านออก

ก่อนหน้านี้ PATCH /clubfair/me ปล่อย *pgconn.PgError ดิบขึ้นไป authError ไม่รู้จัก
เลยตกสาขา default เป็น 401 — แล้วฝั่ง iOS แปล 401 เป็น "เซสชันหมดอายุ" พร้อมล้าง
token ทิ้ง นักศึกษาที่กรอกเบอร์ซ้ำจึงถูกเตะออกจากระบบ โดยที่ข้อความไม่เกี่ยวกับ
สาเหตุจริงเลย (เห็นของจริงใน production log 2026/08/17 01:07:34)

เช็คชื่อ constraint ด้วย ไม่ใช่ดูแค่ SQLSTATE — clubfair_users มี UNIQUE สี่คอลัมน์
(email, phone, student_id, oauth_subject) ทั้งสี่คืน 23505 เหมือนกันหมด แต่คนละเรื่อง
ตัวที่ยังไม่มีข้อความเฉพาะจะต้องผ่านไปเป็น 500 ตามเดิม ไม่ใช่ถูกสวมเป็น "เบอร์ซ้ำ"
*/
func TestTranslateClubFairPhoneConflict(t *testing.T) {
	other := errors.New("some other failure")

	cases := []struct {
		name string
		in   error
		want error
	}{
		{
			name: "23505 บน constraint ของเบอร์",
			in:   &pgconn.PgError{Code: "23505", ConstraintName: "clubfair_users_phone_key"},
			want: ErrClubFairPhoneTaken,
		},
		{
			name: "23505 บน constraint อื่นไม่ถูกแปล",
			in:   &pgconn.PgError{Code: "23505", ConstraintName: "clubfair_users_email_key"},
			want: nil, // ต้องคืน error เดิม ตรวจแยกด้านล่าง
		},
		{
			name: "รหัสอื่นบน constraint เดียวกันไม่ถูกแปล",
			in:   &pgconn.PgError{Code: "23503", ConstraintName: "clubfair_users_phone_key"},
			want: nil,
		},
		{
			name: "error ที่ไม่ใช่ของ postgres",
			in:   other,
			want: nil,
		},
		{
			name: "nil ยังเป็น nil",
			in:   nil,
			want: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := translateClubFairPhoneConflict(tc.in)

			if tc.want != nil {
				if !errors.Is(got, tc.want) {
					t.Fatalf("อยากได้ %v, ได้ %v", tc.want, got)
				}
				return
			}

			if got != tc.in {
				t.Fatalf("ต้องคืน error เดิมโดยไม่แตะ: อยากได้ %v, ได้ %v", tc.in, got)
			}
			if errors.Is(got, ErrClubFairPhoneTaken) {
				t.Fatalf("ไม่ควรถูกสวมเป็นเบอร์ซ้ำ: %v", got)
			}
		})
	}
}

// error ที่ถูกห่อมาอีกชั้นยังต้องแปลได้ — pgx ห่อ error ของ connection บ่อย
func TestTranslateClubFairPhoneConflictThroughWrappedError(t *testing.T) {
	wrapped := errors.Join(
		errors.New("update clubfair_users"),
		&pgconn.PgError{Code: "23505", ConstraintName: "clubfair_users_phone_key"},
	)

	if got := translateClubFairPhoneConflict(wrapped); !errors.Is(got, ErrClubFairPhoneTaken) {
		t.Fatalf("อยากได้ ErrClubFairPhoneTaken, ได้ %v", got)
	}
}
