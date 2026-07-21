package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"su-server/config"
	"su-server/internal/handler"
	appmw "su-server/internal/middleware"
	"su-server/internal/repository"
	"su-server/internal/service"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/joho/godotenv"
)

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

	eventRepository := repository.NewEventRepository(db)
	eventService := service.NewEventService(eventRepository)
	eventHandler := handler.NewEventHandler(eventService)

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
	// ใช้ connection pool ไม่ใช่ *pgx.Conn เดี่ยว — conn เดียวไม่ปลอดภัยเมื่อมี request พร้อมกัน
	pool, err := config.ConnectPool(context.Background())
	if err != nil {
		slog.Error("WBW pool connection failed", "err", err)
		os.Exit(1)
	}
	defer pool.Close()
	slog.Info("WBW POOL CONNECTED")

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

	// ต้องผ่าน RequireAuth ก่อนเสมอ แล้วจึงเช็ค role
	requireAuth := appmw.RequireAuth(wbwTokens)
	requireAdmin := appmw.RequireRole("admin")
	requireStaff := appmw.RequireRole("admin", "staff")

    r.Get("/", func(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"message": "SU Backend running"}`))
    })

	r.Route("/su-server", func(r chi.Router) {
		r.Route("/events", func(r chi.Router) {
			r.Get("/", eventHandler.GetAllEvents)
			r.Get("/{id}", eventHandler.GetOneEvents)
			r.Post("/", eventHandler.CreateOneEvent)
			r.Put("/{id}", eventHandler.UpdateOneEvent)
			r.Delete("/{id}", eventHandler.DeleteOneEvents)
		})
		r.Route("/users", func(r chi.Router) {
			r.Get("/{id}", userHandler.GetUserByID)
			r.Get("/email/{email}", userHandler.GetUserByEmail)
			r.Post("/insert", userHandler.InsertUser)
			r.Post("/upsert", userHandler.UpsertUser)
			r.Patch("/{id}", userHandler.UpdateUser)
			r.Delete("/{id}", eventHandler.DeleteOneEvents)
		})
		r.Route("/auth", func(r chi.Router) {
			r.Get("/google", oauthHandler.GoogleLogin)
			r.Get("/google/callback", oauthHandler.GoogleCallback)
			r.Post("/google/verify", oauthHandler.GoogleVerify)
		})
		r.Route("/steps", func(r chi.Router) {
			r.Get("/{userID}", stepHandler.GetStepsByUserID)
			r.Get("/{userID}/range?from=2026-06-01&to=2026-08-01", stepHandler.GetStepsByDateRange)
			r.Post("/sync", stepHandler.SyncSteps)
			r.Post("/sync/bulk", stepHandler.SyncManySteps)
		})
		r.Route("/leaderboard", func(r chi.Router) {
			r.Get("/", leaderboardHandler.GetLeaderboard)
			r.Get("/{userID}", leaderboardHandler.GetUserRank)
			r.Post("/update", leaderboardHandler.UpdateEntry)
			r.Post("/reset", leaderboardHandler.Reset)
		})
	})

	/* ============================================================
	   WBW routes — เว็บ web-next proxy /api/* มาที่นี่
	   next.config.ts ตัด /api ออก แล้วยิงไป ${API_UPSTREAM}/:path*
	   ตั้ง API_UPSTREAM=http://localhost:8080/wbw
	   ============================================================ */
	r.Route("/wbw", func(r chi.Router) {
		r.Route("/auth", func(r chi.Router) {
			r.Post("/register", wbwAuthHandler.Register)
			r.Post("/login", wbwAuthHandler.Login)
		})

		r.Route("/admin", func(r chi.Router) {
			// หน้าสมัครเรียกก่อนล็อกอิน — ต้องเปิดสาธารณะ (ตรงกับของเดิม)
			r.Get("/schools", wbwAdminHandler.ListSchools)

			r.Group(func(r chi.Router) {
				r.Use(requireAuth, requireAdmin)

				r.Get("/dashboard", wbwAdminHandler.Dashboard)
				r.Get("/logs", wbwAdminHandler.ListLogs)
				r.Get("/bases-overview", wbwAdminHandler.BasesOverview)

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
			})
		})

		r.Route("/groups", func(r chi.Router) {
			r.Use(requireAuth)
			r.Get("/", wbwAdminHandler.ListGroups)
		})

		r.Route("/notifications", func(r chi.Router) {
			r.Use(requireAuth)

			// ผู้เข้าร่วมอ่านประกาศของตัวเองได้
			r.Get("/", wbwNotiHandler.List)

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

	if err := http.ListenAndServe(":"+serverPort, r); err != nil {
		slog.Error("SERVER RUN FAILED", "err", err)
	}
}
