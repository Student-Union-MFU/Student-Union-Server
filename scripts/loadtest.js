// โหลดเทสต์สถานการณ์ "คน 2000 คนเปิดแอปพร้อมกัน" — วัดจริง อย่าเดา
//
// ติดตั้ง k6:  https://k6.io/docs/get-started/installation/
// รัน (ยิงตรงที่ backend, ข้าม CDN):
//   BASE=http://localhost:8080/wbw k6 run scripts/loadtest.js
//   BASE=https://api.your-domain k6 run scripts/loadtest.js
//
// ปรับจำนวนผู้ใช้/ระยะเวลาได้ที่ options.scenarios ด้านล่าง
//
// ⚠ สถานการณ์ "register" สร้างแถวจริงใน DB — ปิดไว้ (exec: undefined) โดยดีฟอลต์
//   เปิดเฉพาะตอนทดสอบบน DB ที่ล้างทิ้งได้ · student_id ต้องไม่ชนของจริง

import http from "k6/http";
import { check, sleep } from "k6";
import { Rate, Trend } from "k6/metrics";

const BASE = __ENV.BASE || "http://localhost:8080/wbw";

const errRate = new Rate("errors");
const schoolsT = new Trend("schools_ms", true);

export const options = {
  // เกณฑ์ผ่าน/ไม่ผ่าน — ถ้าไม่ผ่านคือ host เล็กไป ต้องสเกล/แคชเพิ่ม
  thresholds: {
    errors: ["rate<0.01"], // error < 1%
    schools_ms: ["p(95)<400"], // p95 ของ /schools ต่ำกว่า 400ms
    http_req_duration: ["p(95)<800"],
  },
  scenarios: {
    // สถานการณ์หลัก: ทุกคนเปิดหน้าสมัคร → โหลดรายชื่อสำนักวิชา (endpoint ที่ร้อนสุด)
    open_app: {
      executor: "ramping-vus",
      exec: "openApp",
      startVUs: 0,
      stages: [
        { duration: "30s", target: 500 }, // ค่อย ๆ ไต่
        { duration: "1m", target: 2000 }, // พีค 2000 คนพร้อมกัน
        { duration: "1m", target: 2000 }, // ค้างที่พีค
        { duration: "30s", target: 0 }, // ผ่อนลง
      ],
    },
    // สถานการณ์ล็อกอิน (โดน bcrypt) — เบากว่า เพราะไม่ใช่ทุกคนล็อกอินพร้อมกัน
    logins: {
      executor: "constant-arrival-rate",
      exec: "login",
      rate: 50, // 50 ครั้ง/วินาที
      timeUnit: "1s",
      duration: "2m",
      preAllocatedVUs: 100,
      maxVUs: 300,
    },
  },
};

// เปิดแอป = โหลดรายชื่อสำนักวิชา (หน้าสมัครเรียกทุกครั้ง) · ควรมาจาก cache เกือบทั้งหมด
export function openApp() {
  const res = http.get(`${BASE}/admin/schools`);
  schoolsT.add(res.timings.duration);
  errRate.add(res.status !== 200);
  check(res, { "schools 200": (r) => r.status === 200 });
  sleep(Math.random() * 2); // จำลองคนอ่านหน้าจอ
}

// ล็อกอินด้วย credential ปลอม — วัดเส้นทาง bcrypt/DB ตอนโดน throttle โดยไม่สร้างข้อมูล
export function login() {
  const res = http.post(
    `${BASE}/auth/login`,
    JSON.stringify({ username: "6939999999", password: "wrong-password-xyz" }),
    { headers: { "Content-Type": "application/json" } },
  );
  // 401 (รหัสผิด) หรือ 429 (โดน throttle ตอน burst) ถือว่า "ระบบยังตอบอยู่" = ปกติ
  // นับ error เฉพาะ 5xx (เซิร์ฟเวอร์ล่มจริง)
  errRate.add(res.status >= 500);
  check(res, { "login answered": (r) => r.status === 401 || r.status === 429 });
}

/* ── เปิดเฉพาะบน DB ทดสอบ (สร้างแถวจริง) ──────────────────────────
export function register() {
  const sid = "693" + String(Math.floor(1e6 + Math.random() * 8.9e6));
  const res = http.post(`${BASE}/auth/register`, JSON.stringify({
    student_id: sid, password: "loadtest12345",
    profile: { first_name: "load", last_name: "test", school_id: 5, sex: "unspecified" },
    medical: { birthdate: "2004-01-01", weight_kg: 60, height_cm: 170, blood_type: "O+" },
    health: { chronic_conditions: [] },
    consent: { consent_health_data: true, consent_emergency_treatment: true, waiver_accepted: true },
  }), { headers: { "Content-Type": "application/json" } });
  errRate.add(res.status >= 500);
}
──────────────────────────────────────────────────────────────── */
