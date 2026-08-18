package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"su-server/config"
	"su-server/internal/handler"
	appmw "su-server/internal/middleware"
	"su-server/internal/repository"
	"su-server/internal/service"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/joho/godotenv"
)

// envInt อ่าน env เป็น int · ไม่มี/ผิดรูปแบบ = ใช้ค่า default
func envInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil && v > 0 {
		return v
	}
	return def
}

func main() {

	godotenv.Load()

	r := chi.NewRouter()

	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Origins differ per way in: localhost when web-next runs on this box, a
	// LAN address when it runs on another laptop on the same network, and a
	// *.trycloudflare.com host through the tunnel. Anything extra goes in
	// CORS_ALLOWED_ORIGINS (comma-separated) and is ADDED to these defaults,
	// so a new frontend origin needs no rebuild.
	allowedOrigins := []string{
		"http://localhost:3000",
		"http://localhost:3001",
		// Quick tunnels get a fresh hostname on every restart, so match the
		// whole domain instead of chasing the URL in .env each time.
		"https://*.trycloudflare.com",
	}
	for o := range strings.SplitSeq(os.Getenv("CORS_ALLOWED_ORIGINS"), ",") {
		if o = strings.TrimSpace(o); o != "" {
			allowedOrigins = append(allowedOrigins, o)
		}
	}
	slog.Info("CORS origins", "allowed", allowedOrigins)

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   allowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		AllowCredentials: true,
	}))

	db, err := config.ConnectDB()
	if err != nil {
		slog.Error("DB connection failed:", "err", err)
	} else {
		slog.Info("DB CONNECTED")
	}

	// ใช้ connection pool ไม่ใช่ *pgx.Conn เดี่ยว — conn เดียวไม่ปลอดภัยเมื่อมี request พร้อมกัน
	pool, err := config.ConnectPool(context.Background())
	if err != nil {
		slog.Error("pool connection failed", "err", err)
		os.Exit(1)
	}
	defer pool.Close()
	slog.Info("POOL CONNECTED")

	eventRepository := repository.NewEventRepository(db)
	eventService := service.NewEventService(eventRepository)
	eventHandler := handler.NewEventHandler(eventService)

	boothRepository := repository.NewBoothRepository(pool)
	boothService := service.NewBoothService(boothRepository)
	boothHandler := handler.NewBoothHandler(boothService)

	userRepository := repository.NewUserRepository(db)
	userService := service.NewUserService(userRepository)
	userHandler := handler.NewUserHandler(userService)

	jwtService := service.NewJWTService()

	oauthService := service.NewOAuthService(userService)
	oauthHandler := handler.NewOAuthHandler(oauthService, jwtService)

	stepRepository := repository.NewStepsRepository(db)
	stepService := service.NewStepsService(stepRepository)
	stepHandler := handler.NewStepsHandler(stepService)

	leaderboardRepository := repository.NewLeaderboardRepository(db)
	leaderboardService := service.NewLeaderboardService(leaderboardRepository)
	leaderboardHandler := handler.NewLeaderboardHandler(leaderboardService)

	// ---------- WBW (เดินรอบดอย) ----------
	wbwTokens := service.NewWBWTokenService()

	wbwAuthRepo := repository.NewWBWAuthRepository(pool)
	wbwAuthService := service.NewWBWAuthService(wbwAuthRepo, wbwTokens)
	wbwAuthHandler := handler.NewWBWAuthHandler(wbwAuthService)

	wbwAdminRepo := repository.NewWBWAdminRepository(pool)
	wbwCheckpointRepo := repository.NewWBWCheckpointRepository(pool)
	wbwAdminService := service.NewWBWAdminService(wbwAdminRepo, wbwCheckpointRepo)
	wbwAdminHandler := handler.NewWBWAdminHandler(wbwAdminService)

	wbwNotiRepo := repository.NewWBWNotificationRepository(pool)
	wbwNotiService := service.NewWBWNotificationService(wbwNotiRepo)
	wbwNotiHandler := handler.NewWBWNotificationHandler(wbwNotiService, wbwAdminService)

	// แชท v2 — long-poll ผ่าน Postgres LISTEN/NOTIFY
	// ต้องใช้ connection แยกจาก pool: connection ที่ LISTEN อยู่จะค้างรอ notification
	// ถ้าไปยืมจาก pool จะกินสล็อตค้างตลอด handler อื่นไม่มี connection ใช้
	chatEvents := service.NewChatEvents(pool, config.ConnectListener)
	chatEvents.Start(context.Background())

	wbwDeviceRepo := repository.NewWBWDeviceRepository(pool)
	// ไม่มี GOOGLE_APPLICATION_CREDENTIALS = push ปิดเงียบ แชทในแอปยังครบทุกอย่าง
	wbwPushService := service.NewWBWPushService(context.Background(), wbwDeviceRepo)

	wbwChatRepo := repository.NewWBWChatRepository(pool)
	wbwChatService := service.NewWBWChatService(wbwChatRepo, chatEvents, wbwPushService)
	wbwChatHandler := handler.NewWBWChatHandler(wbwChatService)

	wbwGroupRepo := repository.NewWBWGroupRepository(pool)
	wbwGroupService := service.NewWBWGroupService(wbwGroupRepo, chatEvents)
	wbwGroupHandler := handler.NewWBWGroupHandler(wbwGroupService)

	wbwStaffRepo := repository.NewWBWStaffRepository(pool)
	wbwStaffService := service.NewWBWStaffService(wbwStaffRepo, wbwNotiService, wbwPushService)
	wbwStaffHandler := handler.NewWBWStaffHandler(wbwStaffService)

	// เบอร์กลางงาน — อ่านครั้งเดียวที่นี่แล้วส่งต่อให้ทั้ง progress (ด้านล่าง) และ SOS
	// (ถัดไป) ไม่ใช่ os.Getenv ซ้ำที่จุดสร้างแต่ละอัน · ว่างได้ตอน dev แอปมีเบอร์ default
	// ของตัวเองอยู่แล้ว
	emergencyPhone := os.Getenv("WBW_EMERGENCY_PHONE")

	// ความคืบหน้าเช็คอินของตัวเอง — ใช้ repo เดิมที่ wbwAdminService ใช้อยู่แล้ว · แนบเบอร์
	// กลางไปด้วยเพราะ /me/progress ถูก poll ทุก 60 วิ เป็นจุดที่แอป cache เบอร์ไว้ก่อนเกิดเหตุ
	wbwProgressService := service.NewWBWProgressService(wbwCheckpointRepo, emergencyPhone)
	wbwProgressHandler := handler.NewWBWProgressHandler(wbwProgressService)

	wbwFeedbackRepo := repository.NewWBWFeedbackRepository(pool)
	wbwFeedbackService := service.NewWBWFeedbackService(wbwFeedbackRepo)
	wbwFeedbackHandler := handler.NewWBWFeedbackHandler(wbwFeedbackService)

	wbwDeviceService := service.NewWBWDeviceService(wbwDeviceRepo)
	wbwDeviceHandler := handler.NewWBWDeviceHandler(wbwDeviceService)

	// SOS ฉุกเฉิน — ช่อง LISTEN/NOTIFY แยกจากแชท (ดูคอมเมนต์ที่ sosChannel) ต้อง Start
	// listener เองเหมือน chatEvents ข้างบน · emergencyPhone อ่านไว้ครั้งเดียวข้างบนแล้ว
	// (ตัวเดียวกับที่ progress service ใช้)
	sosRepo := repository.NewWBWSOSRepository(pool)
	sosEvents := service.NewSOSEvents(pool, config.ConnectListener)
	sosEvents.Start(context.Background())
	wbwSOSHandler := handler.NewWBWSOSHandler(
		service.NewWBWSOSService(sosRepo, sosEvents, wbwPushService, wbwNotiService, emergencyPhone))

	// ต้องผ่าน RequireAuth ก่อนเสมอ แล้วจึงเช็ค role
	requireAuth := appmw.RequireAuth(wbwTokens)
	requireAdmin := appmw.RequireRole("admin")
	requireStaff := appmw.RequireRole("admin", "staff")

	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"message": "SU Backend running"}`))
	})

	r.Route("/su-server", func(r chi.Router) {
		// Public: the only way to obtain a token, plus the reads another
		// client is known to call. Closing those is a following round, once
		// the Android client's owner has confirmed what he uses.
		r.Route("/auth", func(r chi.Router) {
			r.Get("/google", oauthHandler.GoogleLogin)
			r.Get("/google/callback", oauthHandler.GoogleCallback)
			r.Post("/google/verify", oauthHandler.GoogleVerify)
		})

		r.Route("/events", func(r chi.Router) {
			r.Get("/", eventHandler.GetAllEvents)
			r.Get("/{id}", eventHandler.GetOneEvents)

			r.Group(func(r chi.Router) {
				r.Use(appmw.RequireSUAuth(jwtService), appmw.RequireSUStaff())
				r.Post("/", eventHandler.CreateOneEvent)
				r.Put("/{id}", eventHandler.UpdateOneEvent)
				r.Delete("/{id}", eventHandler.DeleteOneEvents)
			})
		})

		r.Route("/booths", func(r chi.Router) {
			r.Use(appmw.RequireSUAuth(jwtService))
			r.Get("/", boothHandler.GetAllBooths)
		})

		r.Route("/users", func(r chi.Router) {
			// Auth is attached per-route (not via r.Use on this subrouter)
			// so that DELETE /{id} — no longer registered on any verb below —
			// falls through to chi's native 405, instead of being intercepted
			// by the auth middleware before chi ever gets to ask "does this
			// verb exist here", which would answer 401 and make it look like
			// the removed route were still guarded rather than gone. /events,
			// /steps and /leaderboard avoid this the other way, by nesting
			// their auth-only verbs inside an r.Group, which shares the
			// parent's routing tree instead of wrapping the whole subrouter.

			// A record that belongs to one person.
			r.With(appmw.RequireSUAuth(jwtService), appmw.RequireSelfOrStaff("id")).Get("/{id}", userHandler.GetUserByID)
			r.With(appmw.RequireSUAuth(jwtService), appmw.RequireSelfOrStaff("id")).Patch("/{id}", userHandler.UpdateUser)

			// Ownership cannot be expressed against an email, and knowing an
			// address should not hand over the profile behind it.
			r.With(appmw.RequireSUAuth(jwtService), appmw.RequireSUStaff()).Get("/email/{email}", userHandler.GetUserByEmail)

			// DELETE /{id} is gone. It pointed at eventHandler.DeleteOneEvents
			// and deleted events; UserHandler has no delete method to re-point
			// it at, so this was a route added with nothing behind it. Writing
			// that handler is a different project: nothing defines what becomes
			// of a deleted student's check-ins or step records.

			r.With(appmw.RequireSUAuth(jwtService), appmw.RequireSUStaff()).Post("/insert", userHandler.InsertUser)
			r.With(appmw.RequireSUAuth(jwtService), appmw.RequireSUStaff()).Post("/upsert", userHandler.UpsertUser)
		})

		r.Route("/steps", func(r chi.Router) {
			// A step history is a day-by-day record of where one named
			// person was. Paired with the public leaderboard (which hands
			// out id-to-name), a bare token would turn "anyone signed in"
			// into "read anyone's movements" — so these require the caller
			// to be the subject or staff, same as /users/{id}.
			r.With(appmw.RequireSUAuth(jwtService), appmw.RequireSelfOrStaff("userID")).Get("/{userID}", stepHandler.GetStepsByUserID)
			r.With(appmw.RequireSUAuth(jwtService), appmw.RequireSelfOrStaff("userID")).Get("/{userID}/range", stepHandler.GetStepsByDateRange)

			r.Group(func(r chi.Router) {
				// Still trusts a body-supplied user_id — see the "critical"
				// finding above the users routes. Deriving it from claims
				// instead means changing SyncSteps/SyncManySteps, which is
				// a separate piece of work.
				r.Use(appmw.RequireSUAuth(jwtService))
				r.Post("/sync", stepHandler.SyncSteps)
				r.Post("/sync/bulk", stepHandler.SyncManySteps)
			})
		})

		r.Route("/leaderboard", func(r chi.Router) {
			// Public and deliberately left that way: a leaderboard is the
			// campaign's front page, and far likelier to have a live caller
			// than the routes below it.
			r.Get("/", leaderboardHandler.GetLeaderboard)
			r.Get("/{userID}", leaderboardHandler.GetUserRank)

			r.Group(func(r chi.Router) {
				r.Use(appmw.RequireSUAuth(jwtService), appmw.RequireSUStaff())
				r.Post("/update", leaderboardHandler.UpdateEntry)
				r.Post("/reset", leaderboardHandler.Reset)
			})
		})
	})

	// ---------- Club Fair ----------
	// Its own token service and its own secret: a Club Fair token must never
	// verify as an SU one, because clubfair_users.id and users.id are different
	// people. See ClubFairTokenService for the whole argument.
	clubFairTokens := service.NewClubFairTokenService()

	clubFairAuthRepo := repository.NewClubFairAuthRepository(pool)
	clubFairAuthService := service.NewClubFairAuthService(clubFairAuthRepo, clubFairTokens)
	clubFairAuthHandler := handler.NewClubFairAuthHandler(clubFairAuthService)

	clubFairFairRepo := repository.NewClubFairFairRepository(pool)
	clubFairCheckInService := service.NewClubFairCheckInService(clubFairFairRepo)
	clubFairFairHandler := handler.NewClubFairFairHandler(clubFairCheckInService)

	clubFairChannelRepo := repository.NewClubFairChannelRepository(pool)
	clubFairChannelService := service.NewClubFairChannelService(clubFairChannelRepo)
	clubFairChannelHandler := handler.NewClubFairChannelHandler(clubFairChannelService)

	// The fair's own details and its running order — migration 000023.
	clubFairContentRepo := repository.NewClubFairContentRepository(pool)
	clubFairContentService := service.NewClubFairContentService(clubFairContentRepo)
	clubFairContentHandler := handler.NewClubFairContentHandler(clubFairContentService)

	// The staff dashboard: the participant roster and the prize tiers.
	clubFairAdminRepo := repository.NewClubFairAdminRepository(pool)
	clubFairAdminService := service.NewClubFairAdminService(clubFairAdminRepo)
	clubFairAdminHandler := handler.NewClubFairAdminHandler(clubFairAdminService)

	// The write half of the booth table. Shares BoothService with the SU app's
	// read-only /su-server/booths, which is why the handler is separate rather
	// than the service — see ClubFairBoothHandler.
	clubFairBoothHandler := handler.NewClubFairBoothHandler(boothService)

	requireClubFair := appmw.RequireClubFairAuth(clubFairTokens)
	requireClubFairStaff := appmw.RequireClubFairRole(
		appmw.ClubFairRoleStaff, appmw.ClubFairRoleAdmin)

	// Who may ask a booth what to put on its screen.
	//
	// ⚠ Deliberately a second, wider list rather than an addition to
	// requireClubFairStaff. A booth owner is not staff: adding the role there
	// would hand every booth's volunteers the announcements channel, the
	// participant roster and the prize table in one line. This gate is used on
	// exactly one route, and the per-booth half of the check is in
	// ClubFairCheckInService.CurrentCode.
	requireClubFairBoothScreen := appmw.RequireClubFairRole(
		appmw.ClubFairRoleStaff, appmw.ClubFairRoleAdmin, appmw.ClubFairRoleBoothOwner)

	/* ============================================================
	   WBW routes — เว็บ web-next proxy /api/* มาที่นี่
	   next.config.ts ตัด /api ออก แล้วยิงไป ${API_UPSTREAM}/:path*
	   ตั้ง API_UPSTREAM=http://localhost:8080/wbw
	   ============================================================ */
	r.Route("/wbw", func(r chi.Router) {
		r.Route("/auth", func(r chi.Router) {
			// register/login ทำ bcrypt (cost 10 ≈ 80ms CPU/ครั้ง) — ถ้าคนสมัคร/ล็อกอิน
			// พร้อมกันหลักพันจะเผา CPU จนล่ม · จำกัดจำนวนที่ประมวลผลพร้อมกัน ที่เหลือเข้าคิว
			// (backlog) รอถึง timeout แล้วค่อยตอบ 429 — เป็นการ "หน่วง" ไม่ใช่ "ปฏิเสธ"
			// throughput ที่ 40 พร้อมกัน ≈ 500 req/s ยังเหลือเฟือ · ปรับผ่าน env ได้
			r.Use(middleware.ThrottleBacklog(
				envInt("AUTH_THROTTLE_LIMIT", 40),
				envInt("AUTH_THROTTLE_BACKLOG", 2000),
				time.Duration(envInt("AUTH_THROTTLE_TIMEOUT_SEC", 25))*time.Second,
			))
			r.Post("/register", wbwAuthHandler.Register)
			r.Post("/login", wbwAuthHandler.Login)
			// เจ้าหน้าที่สมัครเอง — สร้างบัญชี pending รอแอดมินอนุมัติ (throttle เดียวกับ auth)
			r.Post("/staff-register", wbwAuthHandler.RegisterStaff)
		})

		// จำนวนที่นั่งคงเหลือ — เปิดสาธารณะ หน้าสมัครเรียกก่อนให้กรอกฟอร์ม
		// อยู่นอกกลุ่ม /auth ตั้งใจ: ไม่ต้องไปเข้าคิว throttle ของ bcrypt เพราะอ่านแถวเดียว
		r.Get("/capacity", wbwAuthHandler.Capacity)

		r.Route("/admin", func(r chi.Router) {
			// หน้าสมัครเรียกก่อนล็อกอิน — ต้องเปิดสาธารณะ (ตรงกับของเดิม)
			r.Get("/schools", wbwAdminHandler.ListSchools)

			r.Group(func(r chi.Router) {
				r.Use(requireAuth, requireAdmin)

				r.Get("/dashboard", wbwAdminHandler.Dashboard)
				r.Get("/logs", wbwAdminHandler.ListLogs)
				r.Get("/bases-overview", wbwAdminHandler.BasesOverview)
				r.Get("/feedback", wbwFeedbackHandler.AdminList)

				r.Route("/participants", func(r chi.Router) {
					r.Get("/", wbwAdminHandler.ListParticipants)
					r.Get("/{id}/detail", wbwAdminHandler.ParticipantDetail)
					r.Patch("/{id}", wbwAdminHandler.UpdateParticipant)
					r.Post("/{id}/reset-password", wbwAdminHandler.ResetParticipantPassword)
					r.Delete("/{id}", wbwAdminHandler.DeleteParticipant)
				})

				r.Route("/checkpoints", func(r chi.Router) {
					r.Get("/", wbwAdminHandler.ListCheckpoints)
					r.Post("/", wbwAdminHandler.CreateCheckpoint)
					r.Patch("/{id}", wbwAdminHandler.UpdateCheckpoint)
					r.Delete("/{id}", wbwAdminHandler.DeleteCheckpoint)
					r.Post("/{id}/staff", wbwAdminHandler.AssignStaff)
					r.Delete("/{id}/staff/{userId}", wbwAdminHandler.RemoveStaff)
				})

				r.Route("/users", func(r chi.Router) {
					r.Get("/", wbwAdminHandler.ListUsers)
					r.Post("/", wbwAdminHandler.CreateUser)
					r.Patch("/{id}", wbwAdminHandler.UpdateUser)
					r.Post("/{id}/password", wbwAdminHandler.SetUserPassword)
					r.Delete("/{id}", wbwAdminHandler.DeleteUser)
				})

				// คำขอเป็นเจ้าหน้าที่ (สมัครเอง รออนุมัติ)
				r.Route("/staff-requests", func(r chi.Router) {
					r.Get("/", wbwAdminHandler.ListStaffRequests)
					r.Post("/{id}/approve", wbwAdminHandler.ApproveStaff)
					r.Post("/{id}/reject", wbwAdminHandler.RejectStaff)
				})
			})
		})

		// โปรไฟล์ของตัวเอง — ผู้เข้าร่วมที่ล็อกอินอ่านข้อมูลตัวเองได้ (ไม่ต้องเป็น admin)
		r.With(requireAuth).Get("/me", wbwAdminHandler.Me)
		// แก้ได้เฉพาะรูปตัวเอง — ฟิลด์อื่นเป็นของ admin (ดู UpdateOwnPhoto)
		r.With(requireAuth).Patch("/me", wbwAdminHandler.PatchMe)
		// ความคืบหน้าเช็คอินของตัวเอง — แอปใช้คุมขั้นต้นไม้ที่หน้า Home
		r.With(requireAuth).Get("/me/progress", wbwProgressHandler.MyProgress)
		// ความเห็นต่อฐาน — ผู้เข้าร่วมส่งของตัวเอง
		r.With(requireAuth).Post("/me/feedback", wbwFeedbackHandler.Submit)

		// SOS ฉุกเฉิน — กดได้จากทุกหน้า ไม่ผูกกับฐานไหน
		r.With(requireAuth).Post("/me/sos", wbwSOSHandler.Raise)
		r.With(requireAuth).Get("/me/sos/active", wbwSOSHandler.Active)
		r.With(requireAuth).Get("/me/sos/{id}", wbwSOSHandler.Get)
		r.With(requireAuth).Post("/me/sos/{id}/cancel", wbwSOSHandler.Cancel)

		r.Route("/groups", func(r chi.Router) {
			r.Use(requireAuth)
			r.Get("/", wbwAdminHandler.ListGroups)

			// path คงที่มาก่อน {groupId} เพื่อให้อ่านง่าย · chi จัดลำดับ static
			// มาก่อน param ให้เองอยู่แล้ว แต่คนอ่านโค้ดไม่ควรต้องรู้เรื่องนั้น
			r.Get("/members/index", wbwGroupHandler.MembersIndex)
			r.Post("/leave", wbwGroupHandler.Leave)

			r.Route("/{groupId}", func(r chi.Router) {
				r.Get("/members", wbwGroupHandler.Members)
				r.Post("/join", wbwGroupHandler.Join)

				// แชท — messages คือ poll แบบเดิม, chat/sync คือ long-poll
				r.Get("/messages", wbwChatHandler.Messages)
				r.Post("/messages", wbwChatHandler.Send)
				r.Get("/chat/sync", wbwChatHandler.Sync)
				r.Post("/chat/read", wbwChatHandler.Read)
			})
		})

		// push — แอปลงทะเบียน FCM token ตอนล็อกอิน ถอนตอน logout
		r.Route("/devices", func(r chi.Router) {
			r.Use(requireAuth)
			r.Post("/register", wbwDeviceHandler.Register)
			r.Post("/unregister", wbwDeviceHandler.Unregister)
		})

		// เจ้าหน้าที่หน้าฐาน — เช็คอินผู้เข้าร่วมจาก QR หรือ BIB
		r.Route("/staff", func(r chi.Router) {
			r.Use(requireAuth, requireStaff)
			r.Get("/checkpoints", wbwStaffHandler.Checkpoints)
			r.Post("/checkin", wbwStaffHandler.Checkin)

			// SOS ฉุกเฉิน — ฝั่งเจ้าหน้าที่
			r.Get("/sos", wbwSOSHandler.StaffFeed)
			r.Post("/sos/{id}/ack", wbwSOSHandler.Ack)
			r.Post("/sos/{id}/resolve", wbwSOSHandler.Resolve)
		})

		r.Route("/notifications", func(r chi.Router) {
			// ประกาศสาธารณะ (audience=all) — หน้า /announcements เปิดดูได้โดยไม่ต้องล็อกอิน
			// ต้องอยู่ก่อน r.Use(requireAuth) เพราะ middleware มีผลกับ route ที่ประกาศตามหลัง
			r.Get("/public", wbwNotiHandler.ListPublic)

			r.Group(func(r chi.Router) {
				r.Use(requireAuth)

				// ผู้เข้าร่วมอ่านประกาศของตัวเองได้ (all + ที่เจาะจงกลุ่ม/สำนัก/รายบุคคล)
				r.Get("/", wbwNotiHandler.List)
				r.Post("/{id}/read", wbwNotiHandler.MarkRead)

				r.Group(func(r chi.Router) {
					r.Use(requireStaff)
					r.Post("/", wbwNotiHandler.Create)
					r.Get("/sent", wbwNotiHandler.ListSent)
					r.Get("/draft", wbwNotiHandler.GetDraft)
					r.Put("/draft", wbwNotiHandler.SaveDraft)
					r.Delete("/draft", wbwNotiHandler.DeleteDraft)
					r.Get("/presets", wbwNotiHandler.ListPresets)
					r.Post("/presets", wbwNotiHandler.CreatePreset)
					r.Delete("/presets/{id}", wbwNotiHandler.DeletePreset)
				})
			})
		})
	})

	/* ============================================================
	   Club Fair routes.

	   Registered only when CLUBFAIR_JWT_SECRET is set. That is deliberate: with
	   no signing key these endpoints could neither issue nor verify a token, and
	   a 404 on a new surface is a far better failure than a live endpoint with no
	   security. The server still starts, so a missing variable does not take
	   Walk-Bike-Week and the SU app down with it — see NewClubFairTokenService.
	   ============================================================ */
	if clubFairTokens.IsEnabled() {
		r.Route("/clubfair", func(r chi.Router) {
			r.Route("/auth", func(r chi.Router) {
				// bcrypt at cost 10 is ~80ms of CPU per call, and a fair means
				// hundreds of students signing in within the same few minutes.
				// Same throttle the WBW auth routes use: excess requests queue
				// and are delayed rather than refused.
				r.Use(middleware.ThrottleBacklog(
					envInt("AUTH_THROTTLE_LIMIT", 40),
					envInt("AUTH_THROTTLE_BACKLOG", 2000),
					time.Duration(envInt("AUTH_THROTTLE_TIMEOUT_SEC", 25))*time.Second,
				))
				r.Post("/google", clubFairAuthHandler.SignInWithGoogle)
				r.Post("/login", clubFairAuthHandler.SignInWithPassword)
				r.Post("/register", clubFairAuthHandler.Register)
			})

			// The open endpoints. All five are the same list for everyone, none
			// carries a secret, and a student deciding whether to come should not
			// have to sign in to read any of them — which is also what lets the
			// public website render them with no token at all.
			//
			// /info and /prizes exist to stop the clients holding their own
			// copies of this data. The fair's dates were a constant in the
			// Android app and another in the website, and the prize thresholds
			// were a third; each was a thing the Student Union could change in a
			// row but not without a release.
			r.Get("/booths", boothHandler.GetAllBooths)
			r.Get("/zones", clubFairFairHandler.ListZones)
			r.Get("/info", clubFairContentHandler.Info)
			r.Get("/program", clubFairContentHandler.Program)
			r.Get("/prizes", clubFairAdminHandler.ListPrizes)

			r.Group(func(r chi.Router) {
				r.Use(requireClubFair)

				r.Get("/me", clubFairAuthHandler.Me)
				r.Patch("/me", clubFairAuthHandler.UpdateMe)
				r.Put("/me/password", clubFairAuthHandler.SetPassword)
				// The booths the caller runs, which is what a booth owner's own
				// screen loads. No role gate: an account with no assignments
				// gets an empty list, and that is the true answer for every
				// student at the fair.
				r.Get("/me/booths", clubFairAdminHandler.MyBooths)

				r.Get("/progress", clubFairFairHandler.Progress)
				r.Get("/checkins", clubFairFairHandler.ListCheckIns)
				r.Post("/checkins", clubFairFairHandler.CreateCheckIn)

				r.Route("/announcements", func(r chi.Router) {
					// Authenticated even though the posts are the same for
					// everyone: `mine` on each reaction chip is per-student.
					r.Get("/", clubFairChannelHandler.List)
					r.Post("/{id}/reactions", clubFairChannelHandler.React)

					r.Group(func(r chi.Router) {
						r.Use(requireClubFairStaff)
						r.Post("/", clubFairChannelHandler.Post)
						r.Delete("/{id}", clubFairChannelHandler.Delete)
					})
				})

				/*
				   What the display at a booth polls.
				   ⚠ Gated by requireClubFairBoothScreen, NOT
				   requireClubFairStaff, and the handler is not the
				   whole of the check. The middleware admits staff,
				   admin and booth_owner; CurrentCode then refuses a
				   booth owner any booth not assigned to them, because
				   "may this user see THIS booth" is a question about a
				   row, which no role middleware can answer.

				   Before booth_owner existed this was staff-only, which
				   meant a screen on a booth's table held a credential
				   that could also post to two thousand students and read
				   the whole roster. See migration 000024.
				*/
				r.With(requireClubFairBoothScreen).
					Get("/booths/{id}/checkin-code", clubFairFairHandler.BoothCheckInCode)

				r.Group(func(r chi.Router) {
					r.Use(requireClubFairStaff)
					// A prize is a physical object leaving a table.
					r.Post("/prizes/claim", clubFairFairHandler.ClaimPrize)
				})

				/* ----------------------------------------------------
				   The staff dashboard.

				   Grouped under /admin the way WBW's is, so the split
				   between "what a student may read" and "what staff may
				   change" is visible in the URL rather than only in this
				   file. Every route below is staff or above; the two that
				   are admin-only enforce it in the service, because the
				   rule is about the *edit* (moving a role) rather than
				   about the route.
				   ---------------------------------------------------- */
				r.Route("/admin", func(r chi.Router) {
					r.Use(requireClubFairStaff)

					// The fair itself. PUT, not PATCH: the dashboard shows
					// the whole thing, so a cleared box means cleared.
					r.Put("/info", clubFairContentHandler.SaveInfo)

					// Drafts included, unlike the public /program.
					r.Get("/program", clubFairContentHandler.ProgramForAdmin)
					r.Post("/program", clubFairContentHandler.CreateProgramEntry)
					r.Put("/program/{id}", clubFairContentHandler.UpdateProgramEntry)
					r.Delete("/program/{id}", clubFairContentHandler.DeleteProgramEntry)

					// Booth writes. The read stays on the public route —
					// there is one booth list and no reason for staff to
					// see a different one.
					r.Get("/booth-categories", clubFairBoothHandler.Categories)
					r.Post("/booths", clubFairBoothHandler.Create)
					r.Put("/booths/{id}", clubFairBoothHandler.Update)
					r.Delete("/booths/{id}", clubFairBoothHandler.Delete)

					// Retired tiers and claim counts, which the public list
					// deliberately does not carry.
					r.Get("/prizes", clubFairAdminHandler.ListPrizesForAdmin)
					r.Post("/prizes", clubFairAdminHandler.CreatePrize)
					r.Put("/prizes/{id}", clubFairAdminHandler.UpdatePrize)
					r.Delete("/prizes/{id}", clubFairAdminHandler.DeletePrize)

					r.Get("/participants", clubFairAdminHandler.ListParticipants)
					r.Get("/participants/{id}", clubFairAdminHandler.ParticipantDetail)
					// Creating an account with any role above student is
					// admin-only, for the same reason promoting one is:
					// they give away exactly the same thing.
					r.Post("/participants", clubFairAdminHandler.CreateParticipant)
					// Role changes inside this are admin-only — see
					// ClubFairAdminService.UpdateParticipant.
					r.Patch("/participants/{id}", clubFairAdminHandler.UpdateParticipant)
					// Booth assignment is staff-level, unlike the role
					// itself: granting booth_owner is the decision that
					// matters and is an admin's, while deciding that the
					// person already running A4 also runs A5 happens twice
					// an hour during setup.
					r.Put("/participants/{id}/booths", clubFairAdminHandler.SetParticipantBooths)
					// Admin-only, and refused on your own account —
					// see SetParticipantPassword. It is the only way
					// back for someone an admin created who cannot
					// sign in, and it does not end a live session.
					r.Put("/participants/{id}/password", clubFairAdminHandler.SetParticipantPassword)
				})
			})
		})
	} else {
		slog.Warn("/clubfair routes are NOT registered — set CLUBFAIR_JWT_SECRET to enable them")
	}

	// SERVER_PORT is what .env sets; PORT is the convention most hosting
	// platforms inject. Check ours first, then fall back to theirs.
	serverPort := os.Getenv("SERVER_PORT")
	if serverPort == "" {
		serverPort = os.Getenv("PORT")
	}
	if serverPort == "" {
		serverPort = "8080"
	}

	slog.Info("Server running :", "port", serverPort)

	// http.ListenAndServe เปล่า ๆ ไม่มี timeout สักตัว — connection ที่เปิดค้างไว้
	// (เน็ตมือถือหลุดกลางคัน หรือคนยิงมั่ว ๆ) จะกองอยู่จนหมด fd ของเครื่อง
	// ตั้ง server เองเพื่อกำหนดขอบเวลาให้ครบ
	srv := &http.Server{
		Addr:    ":" + serverPort,
		Handler: r,
		// กัน slowloris: ส่ง header ไม่จบใน 10 วิ ตัดทิ้ง
		ReadHeaderTimeout: 10 * time.Second,
		// ฟอร์มสมัครเป็น JSON ก้อนเล็ก 30 วิเหลือเฟือแม้เน็ตช้า
		ReadTimeout: 30 * time.Second,
		// ⚠ ห้ามตั้ง WriteTimeout: แชท/แจ้งเตือน/SOS เป็น long-poll ค้างได้ถึง 25 วิ
		// (maxWaitSeconds ใน wbw_chat_service.go) ถ้าตั้งสั้นกว่านั้น long-poll จะถูกตัดกลางคัน
		// ตัวคุมจริงคือ ReadTimeout + IdleTimeout ข้างบน/ข้างล่าง
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 1 << 20, // 1 MB
	}
	if err := srv.ListenAndServe(); err != nil {
		slog.Error("SERVER RUN FAILED", "err", err)
	}
}
