# Student Union Main Server
Backend server for Mae Fah Luang University Student Union services, built with Go and PostgreSQL.

> 🚧 This project is currently under active development.

## Tech Stack

- **Language:** Go
- **Database:** PostgreSQL 15
- **Containerization:** Docker & Docker Compose
- **Router:** Chi
- **Architecture:** Handler → Service → Repository pattern

## Project Structure

```
su-server/
├── cmd/
│   └── main.go                     # Entry point
├── config/
│   └── database.go                 # Database connection & config
├── db/
│   └── migrations/
│       ├── 000001_init.up.sql      # Events & lost and found schema
│       ├── 000001_init.down.sql
│       ├── 000002_user.up.sql      # Users schema
│       ├── 000002_user.down.sql
│       ├── 000003_steps.up.sql     # Steps schema
│       └── 000004_leaderboard.up.sql # Leaderboard schema
├── internal/
│   ├── handler/                    # HTTP handlers (request/response)
│   │   ├── event_handler.go
│   │   ├── leaderboard_handler.go
│   │   ├── oauth_handler.go
│   │   ├── step_handler.go
│   │   └── user_handler.go
│   ├── middleware/                 # HTTP middleware
│   ├── model/                      # Data models
│   │   ├── event_model.go
│   │   ├── event_image_model.go
│   │   ├── leaderboard_model.go
│   │   ├── step_model.go
│   │   └── user_model.go
│   ├── repository/                 # Database queries
│   │   ├── event_repository.go
│   │   ├── leaderboard_repository.go
│   │   ├── step_repository.go
│   │   └── user_repository.go
│   └── service/                    # Business logic
│       ├── event_service.go
│       ├── jwt_service.go
│       ├── leaderboard_service.go
│       ├── oauth_service.go
│       ├── step_service.go
│       └── user_service.go
├── docker-compose.yml
├── Dockerfile
└── Makefile
```

## API Routes

### Auth
| Method | Route | Description |
|--------|-------|-------------|
| `GET` | `/auth/google` | Redirect to Google OAuth2 login |
| `GET` | `/auth/google/callback` | Google OAuth2 callback |
| `POST` | `/auth/google/verify` | Verify Google ID token (Flutter mobile) |

### Events
| Method | Route | Description |
|--------|-------|-------------|
| `GET` | `/events` | List all events |
| `GET` | `/events/:id` | Get event by ID |
| `POST` | `/events` | Create a new event |
| `PUT` | `/events/:id` | Update an event |
| `DELETE` | `/events/:id` | Delete an event |

### Booths
| Method | Route | Description |
|--------|-------|-------------|
| `GET` | `/booths` | List all booths. `event_id` is nullable — a booth with no event assigned yet returns `event_id: null`. `secret` (the booth's check-in HMAC key) is deliberately never returned on this endpoint. |

### Users
| Method | Route | Description |
|--------|-------|-------------|
| `GET` | `/users/:id` | Get user by ID |
| `GET` | `/users/email/:email` | Get user by email |
| `POST` | `/users` | Create a new user |
| `PATCH` | `/users/:id` | Update user profile |

### Steps
| Method | Route | Description |
|--------|-------|-------------|
| `GET` | `/steps/:userID` | Get all steps for a user |
| `GET` | `/steps/:userID/range` | Get steps by date range (`?from=&to=`) |
| `POST` | `/steps/sync` | Sync a single day's steps |
| `POST` | `/steps/sync/bulk` | Bulk sync multiple days |

### Leaderboard
| Method | Route | Description |
|--------|-------|-------------|
| `GET` | `/leaderboard` | Get full ranked leaderboard |
| `GET` | `/leaderboard/:userID` | Get a user's current rank |
| `POST` | `/leaderboard/update` | Update a user's step count |
| `POST` | `/leaderboard/reset` | Reset the leaderboard |

## Getting Started

### Prerequisites

- [Go 1.21+](https://golang.org/dl/)
- [Docker](https://www.docker.com/) & Docker Compose
- [golang-migrate](https://github.com/golang-migrate/migrate)

### Setup

```bash
# Clone the repo
git clone https://github.com/yourname/su-server.git
cd su-server

# Copy env file and fill in values
cp .env.example .env

# Start PostgreSQL
docker-compose up -d

# Run migrations
make migrate-up

# Start dev server with hot reload
make dev
```

### Environment Variables

| Variable | Description | Example |
|----------|-------------|---------|
| `PORT` | Server port | `8080` |
| `BASE_URL` | Base URL | `http://localhost:8080` |
| `DB_HOST` | PostgreSQL host | `localhost` |
| `DB_PORT` | PostgreSQL port | `5432` |
| `DB_USER` | Database user | `admin` |
| `DB_PASSWORD` | Database password | `secret` |
| `DB_NAME` | Database name | `sudb` |
| `GOOGLE_CLIENT_ID` | Google OAuth2 client ID | |
| `GOOGLE_CLIENT_SECRET` | Google OAuth2 client secret | |
| `GOOGLE_REDIRECT_URL` | Google OAuth2 redirect URL | `http://localhost:8080/auth/google/callback` |
| `JWT_SECRET` | JWT signing secret | |
| `JWT_EXPIRY_HOURS` | JWT expiry in hours | `24` |

### Makefile Commands

```bash
make dev              # Start dev server with hot reload (air)
make migrate-up       # Run all pending migrations
make migrate-down     # Roll back last migration

# Events
make get-events
make get-event id=1
make create-event title="..." content="..." location="..." date="..." time="..." link="..."
make update-event id=1 title="..." content="..."
make delete-event id=1

# Users
make get-user id=1
make get-user-email email="640@lamduan.mfu.ac.th"
make create-user name="..." email="..." usertype="student" student_id="..." major="..." school="..." avatar_url="..." oauth_subject="..."
make update-user id=1 major="..." school="..." student_id="..."

# Steps
make sync-steps
make sync-steps-bulk
make get-steps userID=1
make get-steps-range userID=1 from=2026-06-16 to=2026-06-22

# Leaderboard
make get-leaderboard
make get-user-rank userID=1
make update-leaderboard userID=1 step_count=8432
make reset-leaderboard
```

## License

See [LICENSE](./LICENSE) for details.
