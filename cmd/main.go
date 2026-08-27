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

	// Per-route request metrics for the stats page — count, latency and status
	// classes, held in memory behind a mutex. The long-poll routes are named
	// here because their 25-second latency is CORRECT (see maxWaitSeconds in
	// wbw_chat_service.go and docs/chat-v2-deploy.md); flagged rather than
	// excluded, so a chat/sync that starts failing fast is still visible.
	requestMetrics := appmw.NewRequestMetrics(
		"GET /wbw/groups/{groupId}/chat/sync",
		"GET /wbw/me/sos/active",
		"GET /wbw/staff/sos",
	)

	// ⚠ Mounted ABOVE Recoverer, not below it. chi nests middleware
	// outermost-first, so a handler that panics under Recoverer unwinds through
	// whatever sits below it BEFORE the recover() runs — a recorder mounted
	// there would file the request as status 0 and never see the 500 the client
	// received. Above, Recoverer writes its 500 into the wrapped writer first
	// and the recorder reads what actually went out.
	// (docs/stats-dashboard.md asks for the opposite order for this exact
	// reason; the reason is right and the ordering it prescribes defeats it.)
	r.Use(appmw.RecordRequests(requestMetrics))

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

	// ใช้ connection pool ไม่ใช่ *pgx.Conn เดี่ยว — conn เดียวไม่ปลอดภัยเมื่อมี request พร้อมกัน
	//
	// เมื่อก่อนตรงนี้เปิด *pgx.Conn เดี่ยวอีกเส้น (config.ConnectDB) แล้วแจกให้ repository
	// ชุดเก่าของ SU สี่ตัว — event, user, step, leaderboard · pgx เขียนไว้ชัดว่า *pgx.Conn
	// "is not safe for concurrent usage" และไม่มีล็อกข้างในเลย สอง request ที่เข้ามาพร้อมกัน
	// จึงเขียนทับกันบนสายเดียวกันได้จริง ผลที่ได้คือแถวผิดตัว/decode พัง ไม่ใช่ error ที่อ่านออก
	// · แถม ConnectDB ล้มแล้วแค่ log ต่อ ไม่ยอมตาย ทุก request ของสี่เส้นนั้นจึง nil panic
	// ทีละใบ · ตอนนี้ทั้งสี่ตัวใช้ pool ร่วมกับที่เหลือแล้ว ไม่ได้เพิ่ม connection สักเส้น
	pool, err := config.ConnectPool(context.Background())
	if err != nil {
		slog.Error("pool connection failed", "err", err)
		os.Exit(1)
	}
	defer pool.Close()
	slog.Info("POOL CONNECTED")

	eventRepository := repository.NewEventRepository(pool)
	eventService := service.NewEventService(eventRepository)
	eventHandler := handler.NewEventHandler(eventService)

	boothRepository := repository.NewBoothRepository(pool)
	boothService := service.NewBoothService(boothRepository)
	boothHandler := handler.NewBoothHandler(boothService)

	userRepository := repository.NewUserRepository(pool)
	userService := service.NewUserService(userRepository)
	userHandler := handler.NewUserHandler(userService)

	jwtService := service.NewJWTService()

	oauthService := service.NewOAuthService(userService)
	oauthHandler := handler.NewOAuthHandler(oauthService, jwtService)

	stepRepository := repository.NewStepsRepository(pool)
	stepService := service.NewStepsService(stepRepository)
	stepHandler := handler.NewStepsHandler(stepService)

	leaderboardRepository := repository.NewLeaderboardRepository(pool)
	leaderboardService := service.NewLeaderboardService(leaderboardRepository)
	leaderboardHandler := handler.NewLeaderboardHandler(leaderboardService)

	// Reads pool.Stat() straight off the pool — no repository, no service, and
	// deliberately so. See the comment block on DBPoolHandler.
	dbPoolHandler := handler.NewDBPoolHandler(pool)

	// ---------- WBW (เดินรอบดอย) ----------
	wbwTokens := service.NewWBWTokenService()

	// อีเมลขาออก — ตอนนี้มีผู้ใช้รายเดียวคือลิงก์ตั้งรหัสผ่านใหม่ · ไม่ตั้ง SMTP_HOST
	// = ปิดเงียบแบบเดียวกับ push ที่ไม่มี service account (ดู mail_service.go)
	wbwMail := service.NewMailService()

	// ฐานของลิงก์ในอีเมล — ที่อยู่ของ "เว็บ" ไม่ใช่ของ API ตัวนี้ · ต้องมาจาก env
	// ฝั่งเซิร์ฟเวอร์เท่านั้น ห้ามประกอบจาก Host header ของ request ที่ขอ (ไม่งั้น
	// ใครก็ทำให้ลิงก์รีเซ็ตของคนอื่นชี้ไปเว็บตัวเองได้)
	webBaseURL := os.Getenv("WBW_WEB_BASE_URL")
	if webBaseURL == "" {
		webBaseURL = "http://localhost:3000"
		slog.Warn("ไม่ได้ตั้ง WBW_WEB_BASE_URL — ลิงก์ตั้งรหัสผ่านใหม่จะชี้ไป localhost", "fallback", webBaseURL)
	}

	wbwAuthRepo := repository.NewWBWAuthRepository(pool)
	wbwAuthService := service.NewWBWAuthService(wbwAuthRepo, wbwTokens, wbwMail, webBaseURL)
	wbwAuthHandler := handler.NewWBWAuthHandler(wbwAuthService)

	wbwAdminRepo := repository.NewWBWAdminRepository(pool)
	wbwCheckpointRepo := repository.NewWBWCheckpointRepository(pool)
	wbwAdminService := service.NewWBWAdminService(wbwAdminRepo, wbwCheckpointRepo, wbwAuthService)
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
	wbwCheckpointService := service.NewWBWCheckpointService(wbwCheckpointRepo)
	wbwCheckpointHandler := handler.NewWBWCheckpointHandler(wbwCheckpointService)

	wbwFeedbackRepo := repository.NewWBWFeedbackRepository(pool)
	wbwFeedbackService := service.NewWBWFeedbackService(wbwFeedbackRepo)
	wbwFeedbackHandler := handler.NewWBWFeedbackHandler(wbwFeedbackService)

	// สถิติรวมสำหรับแท็บ "วิเคราะห์" ของแผงผู้ดูแล — อ่านอย่างเดียว ไม่แตะ repo อื่น
	wbwAnalyticsRepo := repository.NewWBWAnalyticsRepository(pool)
	wbwAnalyticsHandler := handler.NewWBWAnalyticsHandler(service.NewWBWAnalyticsService(wbwAnalyticsRepo))

	wbwDeviceService := service.NewWBWDeviceService(wbwDeviceRepo)
	wbwDeviceHandler := handler.NewWBWDeviceHandler(wbwDeviceService)

	// SOS ฉุกเฉิน — ช่อง LISTEN/NOTIFY แยกจากแชท (ดูคอมเมนต์ที่ sosChannel) ต้อง Start
	// listener เองเหมือน chatEvents ข้างบน · emergencyPhone อ่านไว้ครั้งเดียวข้างบนแล้ว
	// (ตัวเดียวกับที่ progress service ใช้)
	sosRepo := repository.NewWBWSOSRepository(pool)
	sosEvents := service.NewSOSEvents(pool, config.ConnectListener)
	sosEvents.Start(context.Background())
	// service ตัวเดียวถูกใช้สองทาง: handler ของเจ้าหน้าที่หน้างาน และ handler ของแอดมิน
	// เคสที่แอดมินเปิดจากแผงจึงเดินเส้นทางแจ้งเตือน/push เส้นเดียวกับเคสที่กดจากแอป
	// ถ้าแยกกันสร้างสอง instance เคสจากแผงจะไม่ปลุก long-poll ที่ instance อีกตัวถืออยู่
	wbwSOSService := service.NewWBWSOSService(sosRepo, sosEvents, wbwPushService, wbwNotiService, emergencyPhone)
	wbwSOSHandler := handler.NewWBWSOSHandler(wbwSOSService)
	wbwSOSAdminHandler := handler.NewWBWSOSAdminHandler(
		service.NewWBWSOSAdminService(sosRepo, wbwSOSService, wbwAdminRepo))

	// การจัดการแชทโดยผู้ดูแล — ใช้ chatRepo กับ chatEvents ตัวเดียวกับเส้นทางปกติ
	// เพื่อให้การลบ/เซ็นเซอร์ปลุก long-poll เส้นเดียวกับที่แอปฟังอยู่
	wbwChatAdminHandler := handler.NewWBWChatAdminHandler(
		service.NewWBWChatAdminService(wbwChatRepo, wbwAdminRepo, chatEvents))

	// ส่งออก CSV — อ่านอย่างเดียว ไม่มี service ชั้นกลางเพราะไม่มีตรรกะให้แยก
	// (handler แปลงแถวเป็นไฟล์ · repository ดึงแถว) การใส่ passthrough ไว้ตรงกลาง
	// จะเป็นไฟล์ที่ไม่ทำอะไรเลยนอกจากส่งต่อ
	wbwExportHandler := handler.NewWBWExportHandler(repository.NewWBWExportRepository(pool))

	/*
	   The two auth throttles.

	   Built here rather than inline at each r.Use so the stats handler can
	   report them, and wrapped by appmw.Throttle rather than used raw so a
	   refused caller gets {"error": "..."} and a Retry-After instead of chi's
	   plain text with no backoff hint — see internal/middleware/throttle.go.
	   The queueing itself is still chi's, unchanged.

	   ⚠ Two INDEPENDENT throttlers, not one shared quota. Each gets its own
	   limit + backlog, which is why they are reported apart on the stats page.
	*/
	throttleLimit := envInt("AUTH_THROTTLE_LIMIT", 40)
	throttleBacklog := envInt("AUTH_THROTTLE_BACKLOG", 2500)
	throttleTimeout := time.Duration(envInt("AUTH_THROTTLE_TIMEOUT_SEC", 25)) * time.Second

	wbwAuthThrottle := appmw.NewThrottle("wbw-auth", throttleLimit, throttleBacklog, throttleTimeout)
	clubFairAuthThrottle := appmw.NewThrottle("clubfair-auth", throttleLimit, throttleBacklog, throttleTimeout)

	// The stats page and its data endpoints — docs/stats-dashboard.md.
	//
	// Holds the pool, the metric stores and the two event fan-outs directly,
	// with a repository only for the part that reads Postgres. The comment
	// block on StatsHandler argues the split; the same argument as DBPoolHandler
	// above, which is why neither has a service under it.
	statsRepository := repository.NewStatsRepository(pool)
	statsHandler := handler.NewStatsHandler(
		pool, statsRepository, requestMetrics,
		chatEvents, sosEvents, wbwPushService,
		wbwAuthThrottle, clubFairAuthThrottle,
	)

	// ต้องผ่าน RequireAuth ก่อนเสมอ แล้วจึงเช็ค role
	requireAuth := appmw.RequireAuth(wbwTokens)
	requireAdmin := appmw.RequireRole("admin")
	requireStaff := appmw.RequireRole("admin", "staff")

	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"message": "SU Backend running"}`))
	})

	// Public legal pages for the App Store listing (privacy-policy URL and
	// support URL). No auth, no service dependencies, registered outside the
	// clubfair conditional so they stay up even without CLUBFAIR_JWT_SECRET.
	r.Get("/privacy", handler.LegalPrivacyPage)
	r.Get("/support", handler.LegalSupportPage)

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

		// Operational, not a product feature — the live state of the one pool
		// every product shares. It lives under /su-server because that is where
		// the staff identity is, but the numbers it reports cover WBW and Club
		// Fair traffic too.
		//
		// Staff-only: pool headroom tells anyone probing how close the server is
		// to starving. Read it during load, not after — the counters are
		// cumulative since boot, so a quiet morning dilutes a bad afternoon.
		r.With(appmw.RequireSUAuth(jwtService), appmw.RequireSUStaff()).
			Get("/admin/db-pool", dbPoolHandler.Stats)

		/*
		   The server stats dashboard — docs/stats-dashboard.md.

		   ⚠ The PAGE is public, and must stay that way. A browser navigating
		   to a URL cannot send an Authorization header, so auth middleware
		   here would 401 every visit and could never be satisfied. The page
		   carries no numbers at all: it is a shell that signs in with a staff
		   token and then fetches. The gate is on the data below, which is
		   where it can actually work — the same arrangement
		   /clubfair/dashboard uses.

		   It lives under /su-server because that is where the server-wide
		   staff identity is, but what it reports spans all three products.
		*/
		r.Get("/stats", statsHandler.StatsDashboardPage)

		/*
		   The numbers. SU staff only, like /admin/db-pool above and for the
		   same reason: they describe how much headroom is left before the
		   server starves, which is precisely what someone probing wants.

		   /admin/stats is the composite the page polls — one call rather than
		   six, so a refresh cannot render half a state and costs one pool
		   acquisition instead of six. The three beside it exist for reading a
		   single section with curl during an incident.
		*/
		r.Group(func(r chi.Router) {
			r.Use(appmw.RequireSUAuth(jwtService), appmw.RequireSUStaff())
			r.Get("/admin/stats", statsHandler.All)
			r.Get("/admin/runtime", statsHandler.Runtime)
			r.Get("/admin/requests", statsHandler.Requests)
			r.Get("/admin/postgres", statsHandler.Postgres)
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
	requireClubFairAdmin := appmw.RequireClubFairRole(appmw.ClubFairRoleAdmin)

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
			//
			// backlog 2500 (เดิม 2000) — ความจุจริงคือ limit + backlog = 2540 คน
			// ที่ค้างในระบบได้พร้อมกัน (chi สร้าง backlogTokens ขนาดนั้น) ที่เกินไปโดน
			// 429 ทันทีแบบไม่เข้าคิว · เพดานที่ตั้งได้ไม่ใช่ "ยิ่งเยอะยิ่งดี" แต่คือเท่าที่
			// ระบายทันภายใน backlogTimeout (25 วิ) — คิวที่ยาวกว่านั้นแปลว่าคนท้ายแถว
			// รอครบ 25 วิแล้วโดนปฏิเสธอยู่ดี ซึ่งแย่กว่าโดน 429 ตั้งแต่แรก · ที่ 40
			// พร้อมกันเราระบายได้เกิน 100 req/s แน่ ๆ ดังนั้น 2500 ยังระบายทัน
			//
			// ⚠ ค่านี้เป็นของ "แต่ละกลุ่ม" ไม่ใช่ของทั้งเซิร์ฟเวอร์ — /clubfair/auth
			// เรียก r.Use แยกอีกชุด จึงได้โควตา 2540 ของตัวเองต่างหาก
			//
			// ตัว throttle เป็นของ chi เหมือนเดิมทุกอย่าง แค่ห่อให้คนที่โดนปฏิเสธได้ JSON
			// ที่แอปอ่านออก ({"error": "..."}) พร้อม Retry-After แทน plain text เปล่า ๆ
			// และนับแยกตามสาเหตุ เพราะ "คิวเต็ม" กับ "รอจนหมดเวลา" ต้องแก้คนละทางกัน
			r.Use(wbwAuthThrottle.Handler())
			r.Post("/register", wbwAuthHandler.Register)
			r.Post("/login", wbwAuthHandler.Login)
			// เจ้าหน้าที่สมัครเอง — สร้างบัญชี pending รอแอดมินอนุมัติ (throttle เดียวกับ auth)
			r.Post("/staff-register", wbwAuthHandler.RegisterStaff)
			// ลืมรหัสผ่าน — ขอลิงก์ทางอีเมล แล้วเอาตั๋วจากลิงก์มาตั้งรหัสใหม่
			// อยู่ในกลุ่มนี้เพราะ /reset ทำ bcrypt เหมือน login ทุกประการ ส่วน /forgot
			// ไม่ทำ แต่เป็น endpoint สาธารณะที่ยิงซ้ำได้ไม่จำกัด จึงควรอยู่ในคิวเดียวกัน
			// (โควตาต่อบัญชีอยู่คนละชั้น — ดู resetMaxPerHour ใน wbw_auth_service.go)
			r.Post("/forgot", wbwAuthHandler.Forgot)
			r.Post("/reset", wbwAuthHandler.Reset)
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
				r.Get("/analytics", wbwAnalyticsHandler.Analytics)

				// เคสฉุกเฉินในมุมแอดมิน — เห็นทุกเคสรวมที่ปิดแล้ว (ต่างจาก
				// /staff/sos ที่ตอบเฉพาะเคสที่ยังต้องจัดการ) และแก้สถานะด้วยมือได้
				r.Route("/sos", func(r chi.Router) {
					r.Get("/", wbwSOSAdminHandler.List)
					r.Post("/", wbwSOSAdminHandler.Create)
					r.Patch("/{id}", wbwSOSAdminHandler.Patch)
				})

				// ส่งออกเป็นไฟล์ · นามสกุล .csv อยู่ใน path ไม่ใช่ query param
				// เพื่อให้ลิงก์ที่ถูกแชร์/บันทึกไว้ยังบอกได้ว่าปลายทางคือไฟล์อะไร
				r.Get("/export/participants.csv", wbwExportHandler.Participants)
				r.Get("/export/staff.csv", wbwExportHandler.Staff)

				// แชทกลุ่มในมุมผู้ดูแล — อ่านได้ทุกห้อง ลบ/เซ็นเซอร์ได้ทีละข้อความ
				r.Route("/chat", func(r chi.Router) {
					r.Get("/", wbwChatAdminHandler.Rooms)
					// "search" ต้องมาก่อน "{groupId}" ไม่งั้น chi จับ "search" เป็น
					// เลขกลุ่มแล้วตอบ 400 ทุกครั้ง — route ตายตัวชนะ pattern เสมอ
					// ก็ต่อเมื่อประกาศไว้ก่อนในไฟล์นี้
					r.Get("/search", wbwChatAdminHandler.Search)
					r.Get("/{groupId}", wbwChatAdminHandler.Messages)
					r.Post("/messages/{id}", wbwChatAdminHandler.Moderate)
				})

				r.Route("/participants", func(r chi.Router) {
					r.Get("/", wbwAdminHandler.ListParticipants)
					r.Post("/", wbwAdminHandler.CreateParticipant)
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
				// เจ้าหน้าที่ประจำกลุ่ม — คนที่เห็น SOS ขั้นแรกของกลุ่มนั้น ก่อนยกระดับ
				r.Route("/groups/{id}/staff", func(r chi.Router) {
					r.Get("/", wbwAdminHandler.GroupStaff)
					r.Post("/", wbwAdminHandler.AssignGroupStaff)
					r.Delete("/{userId}", wbwAdminHandler.RemoveGroupStaff)
				})

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
		// ลบบัญชีตัวเอง — หน้า /privacy ของเว็บและหน้าตั้งค่าของแอปเรียกตัวนี้
		// ลบทันที ไม่ใช่คำขอที่รออนุมัติ (สโตร์บังคับ) · เฉพาะ role participant
		r.With(requireAuth).Delete("/me", wbwAdminHandler.DeleteMe)
		// รายการฐานทั้งงาน — **ไม่ใช่ของแอดมิน** ของแอดมินที่ /admin/checkpoints คืนรายชื่อ
		// เจ้าหน้าที่ประจำฐานมาด้วย ซึ่งผู้เข้าร่วมไม่ควรได้ · ตัวนี้คืนแค่ชื่อ/กิจกรรม/ชนิด
		//
		// requireAuth ไม่ใช่เปิดสาธารณะ: แท็บแผนที่ที่เรียกมันอยู่หลังล็อกอินอยู่แล้ว จึงไม่มีเหตุ
		// ให้เปิดกว้างกว่าที่ผู้ใช้จริงต้องการ (ต่างจาก /capacity ที่หน้าสมัครเรียกก่อนล็อกอิน)
		r.With(requireAuth).Get("/checkpoints", wbwCheckpointHandler.List)
		// ความคืบหน้าเช็คอินของตัวเอง — แอปใช้คุมขั้นต้นไม้ที่หน้า Home
		r.With(requireAuth).Get("/me/progress", wbwProgressHandler.MyProgress)
		// ความเห็นต่อฐาน — ผู้เข้าร่วมส่งของตัวเอง
		r.With(requireAuth).Post("/me/feedback", wbwFeedbackHandler.Submit)
		// ความเห็นต่อการเดินทั้งงาน — แอปถามครั้งเดียวตอนเช็คอินครบทุกฐาน · ตารางแยกจาก
		// checkin_feedback เพราะไม่ได้ผูกกับฐานไหน (ดู migration 000033)
		r.With(requireAuth).Post("/me/event-feedback", wbwFeedbackHandler.SubmitEvent)

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
			// รายงานผลหลังไปถึง — ปิดเคสหรือยกระดับ แล้วแต่ outcome (ดู service.Report)
			r.Post("/sos/{id}/report", wbwSOSHandler.Report)
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
				//
				// Backlog raised 2000 → 2500 after the 2026-08-22 fair (~3,000
				// students). Real capacity is limit + backlog = 2,540 held at
				// once — chi sizes its backlogTokens channel that way — and
				// anything past it is refused instantly with no queue slot.
				//
				// A backlog is only worth as much as it can DRAIN inside
				// backlogTimeout (25s). Queue depth beyond throughput × timeout
				// is a promise that cannot be kept: the students at the back
				// wait the full 25 seconds and are refused anyway, which is
				// worse than being refused at once. At 40 concurrent we clear
				// well over 100 req/s, so 25s covers 2,500 with room to spare.
				//
				// Queueing beats refusing here for a second reason: a student
				// who gets an instant 429 retries immediately and adds load,
				// while a queued one resolves without touching the server
				// again. Queued requests hold a goroutine and a socket but NOT
				// a database connection — they have not reached a handler yet.
				//
				// ⚠ This value is per-group, not server-wide. The WBW auth
				// routes call r.Use separately and get their own 2,540.
				//
				// Wrapped, for the same reasons as the WBW group: chi's
				// throttle refuses with plain text and no Retry-After, which
				// the apps cannot read and which pushes a refused student
				// straight into a retry. See internal/middleware/throttle.go.
				r.Use(clubFairAuthThrottle.Handler())
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

			// The admin console page. Public like /booths — it is an empty
			// shell; every number on it comes from /clubfair/admin/dashboard,
			// which is where the admin gate lives.
			r.Get("/dashboard", clubFairAdminHandler.DashboardPage)

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
				r.Delete("/me", clubFairAuthHandler.DeleteMe)

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

					/*
					   The rota the website's staff screen shows —
					   migration 000025.

					   ⚠ Here rather than under /admin, and the two are
					   about to stop meaning the same thing. The website
					   has made its dashboard admins-only; the matching
					   server change is narrowing requireClubFairStaff on
					   /clubfair/admin/* to admin, and on the day someone
					   does that, a read living at /admin/contacts takes
					   the staff screen down with it. This is the one
					   thing on that screen a staff account must keep.

					   ⚠ Also the one Club Fair list that is NOT public.
					   It is named people's phone numbers, gathered so a
					   volunteer can reach the prize desk — not so two
					   thousand students can. See the migration.
					*/
					r.Get("/staff/contacts", clubFairContentHandler.StaffContacts)
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

					// Staff contact writes. The read stays on
					// /clubfair/staff/contacts — there is one rota, and a
					// staff member calling a number the editor already
					// removed is what two reads would eventually produce.
					r.Post("/contacts", clubFairContentHandler.CreateStaffContact)
					r.Put("/contacts/{id}", clubFairContentHandler.UpdateStaffContact)
					r.Delete("/contacts/{id}", clubFairContentHandler.DeleteStaffContact)

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

					/*
					   One student's stamps, read and edited from the
					   dashboard's MFU333 screen.

					   ⚠ Admin-only, and enforced in
					   ClubFairAdminService rather than by an
					   r.With(requireClubFairAdmin) here — the same
					   arrangement the role change and the password
					   reset use, and for the same reason: the rule is
					   about the *edit*, not about the route, and
					   putting it in the service is what stops a second
					   caller reaching the repository around the
					   middleware.

					   The write is the reason for the gate. A stamp is
					   what a prize is measured in, so adding them hands
					   out an MFU333 point and removing one takes back an
					   entitlement the student can see in their own app.
					   That is the weight of a role change, not of a
					   booth assignment.

					   ⚠ POST here is the ONLY way into clubfair_checkin
					   that does not verify an HMAC payload from a booth
					   — see CLAUDE.md §6 and the repository. It exists
					   because an admin fixing a missed scan has no
					   payload to present, which is the whole point, and
					   it is why nothing below admin may call it.
					*/
					r.Get("/participants/{id}/checkins", clubFairAdminHandler.ParticipantCheckIns)
					r.Post("/participants/{id}/checkins", clubFairAdminHandler.AddParticipantCheckIn)
					r.Delete("/participants/{id}/checkins/{boothID}", clubFairAdminHandler.RemoveParticipantCheckIn)

					// The fair at a glance, for the console at
					// /clubfair/dashboard. Admin-only, so gated with
					// r.With rather than by its own r.Route — a second
					// r.Route("/admin") on this router is a startup panic,
					// and the rest of the block is staff-level.
					r.With(requireClubFairAdmin).
						Get("/dashboard", clubFairAdminHandler.Dashboard)
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
