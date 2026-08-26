package handler

import (
	"encoding/csv"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"su-server/internal/middleware"
	"su-server/internal/repository"
)

/* ============================================================
   ส่งออก CSV

   เขียนตรงลง http.ResponseWriter ไม่ประกอบไฟล์ทั้งก้อนในหน่วยความจำก่อน —
   สองพันแถวยังไม่ใหญ่ แต่การเขียนแบบ stream ไม่ได้แพงกว่ากันและไม่ต้องกลับมา
   แก้ตอนข้อมูลโตขึ้น

   BOM นำหน้าไฟล์: Excel บนวินโดวส์เดาการเข้ารหัสจากไบต์แรก ถ้าไม่มี BOM มันจะ
   อ่าน UTF-8 เป็น cp874 แล้วชื่อไทยทุกชื่อกลายเป็นอักขระขยะ — ซึ่งคนเปิดไฟล์
   จะสรุปว่า "ระบบส่งออกข้อมูลพัง" ไม่ใช่ "Excel เดาผิด" · LibreOffice กับ
   Google Sheets ไม่ต้องการ BOM แต่ก็ไม่เดือดร้อนกับมัน
   ============================================================ */

const utf8BOM = "\xEF\xBB\xBF"

type WBWExportHandler struct {
	repo *repository.WBWExportRepository
}

func NewWBWExportHandler(repo *repository.WBWExportRepository) *WBWExportHandler {
	return &WBWExportHandler{repo: repo}
}

// writeCSV — หัวตาราง + แถว พร้อม header ที่ทำให้เบราว์เซอร์ดาวน์โหลดเป็นไฟล์
//
// ชื่อไฟล์มีวันที่ติดมาด้วย เพราะไฟล์พวกนี้จะไปกองรวมกันในโฟลเดอร์ดาวน์โหลดของ
// ใครสักคน และ "participants.csv (3)" ไม่ได้บอกว่าอันไหนคือของวันงาน
func (h *WBWExportHandler) writeCSV(w http.ResponseWriter, r *http.Request, name string, header []string, rows [][]string) {
	stamp := time.Now().In(bangkok()).Format("2006-01-02")
	filename := fmt.Sprintf("%s-%s.csv", name, stamp)

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	// ห้าม cache: ไฟล์นี้เป็นภาพของข้อมูล ณ วินาทีที่กด ไม่ใช่ทรัพยากรคงที่
	w.Header().Set("Cache-Control", "no-store")

	if _, err := w.Write([]byte(utf8BOM)); err != nil {
		return
	}
	cw := csv.NewWriter(w)
	if err := cw.Write(header); err != nil {
		slog.Error("เขียนหัวตาราง CSV ไม่สำเร็จ", "err", err)
		return
	}
	for _, row := range rows {
		if err := cw.Write(row); err != nil {
			// เขียน header ตอบไปแล้ว เปลี่ยนเป็น 500 ไม่ได้ — บันทึกไว้แล้วหยุด
			// ไฟล์ที่ได้จะสั้นกว่าจริง ซึ่งคนเปิดสังเกตได้ ต่างจากไฟล์ที่เงียบ
			slog.Error("เขียนแถว CSV ไม่สำเร็จ", "err", err)
			return
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		slog.Error("ปิดไฟล์ CSV ไม่สำเร็จ", "err", err)
	}
}

// bangkok — โซนเวลาของงาน · ถ้าเครื่องไม่มี tzdata ให้ตกไปใช้ UTC แทนที่จะ panic
func bangkok() *time.Location {
	loc, err := time.LoadLocation("Asia/Bangkok")
	if err != nil {
		return time.UTC
	}
	return loc
}

// Participants GET /wbw/admin/export/participants.csv
func (h *WBWExportHandler) Participants(w http.ResponseWriter, r *http.Request) {
	rows, err := h.repo.Participants(r.Context())
	if err != nil {
		slog.Error("ส่งออกผู้เข้าร่วมไม่สำเร็จ", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "ส่งออกข้อมูลไม่สำเร็จ")
		return
	}
	h.writeCSV(w, r, "wbw-participants", repository.ParticipantHeader, rows)
}

// Staff GET /wbw/admin/export/staff.csv
func (h *WBWExportHandler) Staff(w http.ResponseWriter, r *http.Request) {
	rows, err := h.repo.Staff(r.Context())
	if err != nil {
		slog.Error("ส่งออกเจ้าหน้าที่ไม่สำเร็จ", "err", err)
		middleware.WriteError(w, http.StatusInternalServerError, "ส่งออกข้อมูลไม่สำเร็จ")
		return
	}
	h.writeCSV(w, r, "wbw-staff", repository.StaffHeader, rows)
}
