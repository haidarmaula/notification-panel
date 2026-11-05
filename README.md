# Notification Panel Backend

**Version:** 1.0  
**Last Updated:** 2026-09-10  
**Language:** Go 1.26.10

Engineering guidance: [Go standards](docs/go-standards.md) and
[pinned Go skills](docs/tooling/go-skills.md).
For current implementation status and release gates, use the
[production readiness plan](docs/analysis/production-readiness-plan.md).
The editable [target architecture diagram](docs/diagrams/README.md) shows the
multi-application design and distinguishes local foundations from planned work.
The restricted [operator replay runbook](docs/runbooks/operator-replay.md) covers
the new CLI and its deployment gates.
The architecture overview below describes the earlier pipeline; its modern
replacement is still under implementation and is not production-ready.

---

## Overview

Notification Panel is a comprehensive notification delivery system designed to manage, schedule, and track push notifications sent to mobile application users. The system features a REST API for staff administration, Kafka-based asynchronous processing, and integrated delivery tracking via OneSignal.

This backend powers:
- **Admin Portal:** Staff interface for creating and managing notification campaigns
- **Mobile Applications:** Device token registration and notification synchronization
- **Analytics:** Real-time delivery and engagement tracking

---

## High-Level Architecture

```text
┌─────────────────────────────────────────────────────────────┐
│                     Admin Web Portal                         │
│                  (External Frontend - React)                 │
└────────────────────┬────────────────────────────────────────┘
                     │
                     ▼
┌─────────────────────────────────────────────────────────────┐
│              Notification API Server                         │
│                  (Port 8080)                                 │
│  ┌─────────────┬─────────────┬─────────────────────────┐   │
│  │ Auth Module │ Staff CRUD  │ Notification Management │   │
│  │ Users CRUD  │ Segments    │ Profile Management      │   │
│  └─────────────┴─────────────┴─────────────────────────┘   │
└────────┬────────────────────────────┬─────────────────────┘
         │                            │
         ▼                            ▼
    PostgreSQL              Kafka Message Broker
    (Persistence)           (Event Queue)
         │                            │
         │                    ┌───────┴───────┐
         │                    ▼               ▼
         │            ┌─────────────┐  ┌──────────────┐
         │            │   Worker    │  │   Scheduler  │
         │            │(Notification)  │(Task Runner)│
         │            └──────┬──────┘  └──────┬───────┘
         │                   │               │
         │                   └───────┬───────┘
         │                           ▼
         └──────────────────► OneSignal API
                             (Push Provider)
                                    │
                                    ▼
                            Mobile Applications
                          (Android/iOS/Web)
```

### System Components

| Component | Purpose | Technology |
|-----------|---------|-----------|
| **API Server** | REST endpoints for notification management | Go + net/http |
| **PostgreSQL** | Persistent data storage | pgx/v5 |
| **Kafka** | Asynchronous event streaming | Kafka 4.3.1 |
| **Worker** | Processes notification delivery events | Go consumer |
| **Scheduler** | Polls and queues scheduled notifications | Go ticker |
| **OneSignal** | Third-party push notification provider | Firebase Cloud Messaging |

---

## Project Structure

```
notification-panel/
├── cmd/                           # Application entry points
│   ├── server/main.go            # API server (port 8080)
│   ├── worker/main.go            # Kafka consumer for delivery
│   ├── scheduler/main.go          # Scheduled notification processor
│   └── bootstrap/main.go          # Initial setup utility
│
├── internal/                       # Core application logic (not exported)
│   ├── config/                   # Configuration management
│   │   └── config.go             # Server/Worker/Scheduler config loading
│   │
│   ├── database/                 # Data layer
│   │   ├── db.go                 # PostgreSQL connection
│   │   ├── repository/           # Data access patterns
│   │   │   ├── users.go
│   │   │   ├── notifications.go
│   │   │   ├── segments.go
│   │   │   ├── staff_users.go
│   │   │   ├── device_tokens.go
│   │   │   └── ...               # 12+ repository files
│   │   └── sqlc/                 # Auto-generated type-safe SQL queries
│   │
│   ├── features/                 # Modular feature implementations
│   │   ├── auth/                 # Authentication
│   │   │   ├── module.go         # Dependency injection
│   │   │   ├── handler.go        # HTTP handlers
│   │   │   ├── service.go        # Business logic
│   │   │   ├── routes.go         # Route registration
│   │   │   └── dto.go            # Request/response DTOs
│   │   │
│   │   ├── notifications/        # Notification CRUD & sending
│   │   │   ├── module.go
│   │   │   ├── handler.go
│   │   │   ├── service.go
│   │   │   ├── processor.go      # Worker-side processing
│   │   │   ├── provider.go       # Push provider interface
│   │   │   ├── onesignal_provider.go
│   │   │   ├── fcm_provider.go
│   │   │   └── routes.go
│   │   │
│   │   ├── users/                # User management & device tokens
│   │   ├── staff/                # Staff administration
│   │   ├── segments/             # User segmentation & targeting
│   │   ├── profile/              # Staff profile management
│   │   └── mobile/               # Mobile app integration
│   │
│   ├── middleware/               # HTTP middleware
│   │   ├── middleware.go         # Middleware chain pattern
│   │   ├── api_key.go            # API Key validation
│   │   ├── jwt.go                # JWT authentication
│   │   ├── audit.go              # Audit logging
│   │   ├── super_admin.go        # SUPER_ADMIN role check
│   │   └── logger.go             # Request logging
│   │
│   ├── token/                    # JWT token management
│   │   └── token.go              # Token generation & parsing
│   │
│   ├── kafka/                    # Event-driven communication
│   │   ├── producer.go           # Kafka producer
│   │   └── events.go             # Event schemas
│   │
│   ├── audit/                    # Audit trail logging
│   │   └── audit.go              # Administrative action tracking
│   │
│   └── bootstrap/                # System initialization
│       └── bootstrap.go          # Create initial roles & admin
│
├── pkg/                           # Public utility packages
│   └── response/                 # Standardized HTTP responses
│       └── response.go           # JSON response helpers
│
├── db/                            # Database migrations & queries
│   ├── migrations/               # SQL migration files (numbered)
│   │   ├── 000001_init.up.sql
│   │   ├── 000002_add_audit.up.sql
│   │   └── ...
│   ├── queries/                  # sqlc query definitions
│   │   ├── users.sql
│   │   ├── notifications.sql
│   │   └── ...
│   └── sqlc.yaml                 # sqlc code generation config
│
├── docs/                          # Project documentation
│   ├── database.md               # Schema & design decisions
│   ├── development-guide.md      # Development environment setup
│   └── api/                      # API endpoint documentation
│       ├── auth-api.md
│       ├── notifications-api.md
│       ├── users-api.md
│       ├── staff-api.md
│       ├── segments-api.md
│       ├── profile-api.md
│       └── mobile-api.md
│
├── docker-compose.yml            # Local development services
├── Dockerfile.dev                # Development container with Air hot-reload
├── .air.server.toml              # Hot-reload config for server
├── .air.worker.toml              # Hot-reload config for worker
├── .air.scheduler.toml           # Hot-reload config for scheduler
├── Makefile                      # Build & development commands
├── .env.example                  # Environment variables template
└── go.mod / go.sum               # Go dependencies

```

### Module Pattern

Each feature module follows this consistent pattern:

```
internal/features/{module}/
├── module.go          # NewModule() - Dependency injection & wiring
├── handler.go         # HTTP request handlers
├── service.go         # Business logic (domain rules)
├── routes.go          # RegisterRoutes() - Route definitions
├── dto.go             # Request/Response data transfer objects
├── model.go           # Domain model types
└── errors.go          # Domain-specific errors
```

**Flow:** `routes.go` → `handler.go` → `service.go` → `repository` → `sqlc` → `PostgreSQL`

---

## Getting Started

### Prerequisites

- **Docker & Docker Compose** (for containerized development)
- **Go 1.26.10** (for local development)
- **Make** (for command automation)
- **PostgreSQL** 17 (via Docker)
- **Kafka** 4.3.1 (via Docker)
- **Git** (for version control)

### Quick Start (Docker)

**1. Clone the repository**
```bash
git clone https://github.com/haidarmaula/notification-panel.git
cd notification-panel
```

**2. Setup environment**
```bash
cp .env.example .env
# Edit .env with your configuration:
#   - Database credentials
#   - JWT secrets
#   - API keys
#   - OneSignal credentials
```

**3. Start services**
```bash
# Start all containers (PostgreSQL, Kafka, API Server, Worker, Scheduler)
make up

# View logs
make logs

# Stop services
make down
```

**4. Initialize database**
```bash
# Run migrations
make migrate-up

# Create initial admin user and roles
make bootstrap
```

**5. Verify installation**
```bash
# Test API server
curl -X POST http://localhost:8080/api/v1/auth/login \
  -H "X-API-Key: your-api-key" \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@example.com","password":"password"}'
```

### Development Workflow

```bash
# Terminal 1: Start services with hot reload
make up-fg

# Terminal 2: View logs
make logs

# Terminal 3: Make code changes
# → Server/Worker/Scheduler automatically restart via Air

# Run database commands
make migrate-create NAME=add_new_table
make sqlc                           # Regenerate queries
make bootstrap                      # Reset admin user

# Clean up
make down
make reset                          # Reset database & restart services
```

### Environment Variables

See `.env.example` for all required variables:

| Variable | Example | Purpose |
|----------|---------|---------|
| `DB_HOST` | localhost | PostgreSQL host |
| `DB_PORT` | 5432 | PostgreSQL port |
| `DB_USER` | admin | Database user |
| `DB_PASSWORD` | password | Database password |
| `DB_NAME` | notifications | Database name |
| `API_KEY` | your-secret-key | Server API Key |
| `ACCESS_SECRET` | secret-access | JWT access token secret |
| `REFRESH_SECRET` | secret-refresh | JWT refresh token secret |
| `MOBILE_JWT_SECRET` | secret-mobile | Mobile app JWT secret |
| `ONESIGNAL_APP_ID` | xxx | OneSignal app ID |
| `ONESIGNAL_API_KEY` | xxx | OneSignal API key |
| `KAFKA_BROKER` | kafka:9094 | Kafka broker address |
| `SCHEDULER_INTERVAL` | 30s | Check scheduled notifications every N seconds |

---

## API Documentation

All API endpoints are fully documented in `/docs/api/`:

| Module | File | Endpoints |
|--------|------|-----------|
| **Authentication** | `auth-api.md` | `POST /auth/login`, `POST /auth/refresh` |
| **Users** | `users-api.md` | List, Search, Get, Device tokens, Segments, History |
| **Staff** | `staff-api.md` | CRUD, Status, Password management (SUPER_ADMIN only) |
| **Notifications** | `notifications-api.md` | CRUD, Send, Delivery statistics, Lifecycle |
| **Segments** | `segments-api.md` | CRUD, Member management |
| **Profile** | `profile-api.md` | Get, Update profile & password |
| **Mobile** | `mobile-api.md` | Device sync, Token registration |

### Authentication

All API endpoints (except login/refresh) require:

```
X-API-Key: your-secret-api-key
Authorization: Bearer <access_token>
```

Obtain tokens via:
```bash
POST /api/v1/auth/login
{
  "email": "admin@example.com",
  "password": "password"
}
```

---

## Database Schema

Complete database documentation is in `/docs/database.md` including:

- **14 tables** with relationships and constraints
- **Notification lifecycle** (DRAFT → SCHEDULED → QUEUED → PROCESSING → COMPLETED)
- **Delivery tracking** per user/device
- **Audit logging** of all administrative actions
- **Design decisions** explaining normalization choices

Key entities:
- `users` — Mobile app users
- `device_tokens` — Push notification tokens per device
- `staff_users` — Admin portal users
- `notifications` — Notification campaigns
- `notification_targets` — Recipient targeting strategies
- `notification_deliveries` — Per-user delivery status
- `segments` — User audience groups
- `audit_logs` — Administrative action tracking

---

## Architecture Patterns

### 1. Modular Feature Design

Each feature is self-contained with its own:
- Routes & HTTP handlers
- Business logic (service layer)
- Data access (repository layer)
- Domain models & DTOs
- Error types

**Example: Creating a notification**
```
POST /api/v1/notifications
  ↓
NotificationHandler.Create()
  ↓
NotificationService.Create()
  ↓
NotificationRepository.Create() + TargetRepository.Create()
  ↓
sqlc generated queries
  ↓
PostgreSQL
```

### 2. Middleware Chain Pattern

Authentication and authorization use composable middleware:

```go
apiKeyMW := middleware.NewAPIKeyMiddleware(cfg.APIKey)
jwtMW := middleware.NewJWTMiddleware(tokenManager)
superAdminMW := middleware.NewSuperAdminMiddleware(cfg.SuperAdminRole)
auditMW := middleware.NewAuditMiddleware()

use := middleware.Chain(jwtMW.Use, superAdminMW.Use, auditMW.Use)
mux.HandleFunc("PATCH /staff/{id}", use(handler.Update))
```

### 3. Event-Driven Async Processing

**Server sends notifications:**
1. Staff clicks "Send" in admin portal
2. API marks notification as `QUEUED`
3. Publishes `notification.send.requested` event to Kafka

**Worker processes asynchronously:**
1. Consumes event from Kafka
2. Fetches notification targets from database
3. Calls OneSignal API for each device
4. Records delivery status in database
5. Publishes `notification.delivery.updated` event

**Scheduler processes scheduled notifications:**
1. Every N seconds, polls for notifications with `scheduled_at` in past
2. Updates status to `QUEUED`
3. Publishes to Kafka for worker processing

### 4. Type-Safe Database Access

Uses **sqlc** for compile-time SQL safety:
- Define queries in `/db/queries/*.sql`
- sqlc generates type-safe Go code in `/internal/database/sqlc/`
- No hand-written SQL queries → no runtime errors
- Migrations managed with `/db/migrations/`

### 5. Repository Pattern

Business logic is decoupled from database details:

```go
// Service doesn't know SQL
func (s *NotificationService) Create(ctx context.Context, params CreateParams) (*Notification, error) {
    return s.notifRepo.Create(ctx, params)
}

// Repository handles SQL via sqlc
func (r *NotificationRepository) Create(ctx context.Context, ...) (*sqlc.Notification, error) {
    return r.queries.CreateNotification(ctx, ...)
}
```

---

## Common Development Tasks

### Add a New API Endpoint

1. **Define SQL queries** in `/db/queries/`
   ```sql
   -- db/queries/users.sql
   -- name: GetUserByExternalID :one
   SELECT * FROM users WHERE external_id = $1;
   ```

2. **Generate code**
   ```bash
   make sqlc
   ```

3. **Create service method** in `internal/features/{module}/service.go`
   ```go
   func (s *UserService) GetByExternalID(ctx context.Context, externalID string) (*User, error) {
       return s.userRepo.FindByExternalID(ctx, externalID)
   }
   ```

4. **Create handler** in `internal/features/{module}/handler.go`
   ```go
   func (h *UserHandler) GetByExternalID(w http.ResponseWriter, r *http.Request) {
       externalID := r.URL.Query().Get("id")
       user, err := h.service.GetByExternalID(r.Context(), externalID)
       // ...
   }
   ```

5. **Register route** in `internal/features/{module}/routes.go`
   ```go
   mux.HandleFunc("GET "+prefix+"/users/by-external-id", use(m.handler.GetByExternalID))
   ```

6. **Document** in `/docs/api/{module}-api.md`

### Run Database Migrations

```bash
# Create new migration
make migrate-create NAME=add_new_column

# Run pending migrations
make migrate-up

# Rollback last migration
make migrate-down

# Check current version
make migrate-version

# Force to specific version
make migrate-force VERSION=3
```

### Debug Issues

```bash
# View all logs
make logs

# View specific service
make logs-server      # API server
make logs-worker      # Kafka worker
make logs-scheduler   # Scheduler

# Shell into container
make sh-server
make sh-worker

# Kafka UI
# Open http://localhost:8090 to view topics and messages
```

### Testing

Unit tests exist for authz, staff, segments, notifications, middleware and token.
Run them with `go test ./...`; regenerate committed mocks after interface
changes with `go tool mockery` (configured in `.mockery.yml`).

When adding tests:

1. Create `*_test.go` files alongside implementation
2. Use `testify` for assertions (already in go.mod)
3. Mock repository interfaces with the pinned `go tool mockery`
4. Run: `go test ./...`

The green unit suite does not establish PostgreSQL/Kafka/provider integration,
real-device delivery, load capacity or production readiness. See the
[production roadmap](docs/analysis/production-readiness-plan.md).

### Continuous integration

[Backend CI](.github/workflows/ci.yml) checks main pushes, optional pull requests,
manual runs and a weekly schedule. Run the same gates locally without `.env`:

```bash
bash scripts/ci.sh format
bash scripts/ci.sh modules
bash scripts/ci.sh build
bash scripts/ci.sh test
bash scripts/ci.sh vet
bash scripts/ci.sh vuln
```

Module verification expects committed `go.mod`/`go.sum`. Vulnerability scanning
installs a pinned govulncheck into a temporary directory and needs network access
to the Go module proxy and vulnerability database. Failures remain nonzero.
See [CI evidence and limits](docs/analysis/ci-readiness.md); hosted execution and
repository required-check settings still need verification after push.

### Dependency integration smoke

With Docker running, Go, Python 3, `migrate` and Postman CLI installed:

```bash
bash scripts/integration.sh
```

This provisions disposable PostgreSQL/Kafka fixtures, runs migrations up/down/up
and tagged dependency tests, then removes only its owned resources. It uses
fixed loopback ports and synthetic credentials, without loading development
`.env` or starting application workers. Firebase SDK calls go to a local Postman
simulation. See [prerequisites, evidence and limits](docs/analysis/integration-readiness.md).
This is dependency smoke; the production FCM adapter and delivery pipeline
remain separate implementation milestones.

---

## Deployment

### Production Checklist

- [ ] Review `.env` configuration (production secrets)
- [ ] Configure database backups
- [ ] Setup Kafka cluster (not single-node)
- [ ] Enable HTTPS/TLS for API
- [ ] Configure rate limiting
- [ ] Setup monitoring & alerting
- [ ] Create runbooks for common issues
- [ ] Load test notification throughput
- [ ] Plan disaster recovery procedures

### Docker Compose Override

For production, create `docker-compose.prod.yml`:
```bash
docker-compose -f docker-compose.yml -f docker-compose.prod.yml up -d
```

---

## Troubleshooting

| Issue | Cause | Solution |
|-------|-------|----------|
| `connection refused: postgres` | Database not ready | Wait 30s, then retry; check `make logs` |
| `Kafka broker not available` | Kafka startup delayed | Wait for healthcheck; view `make logs-worker` |
| `401 Unauthorized` | Invalid API Key or JWT | Verify `X-API-Key` header and token validity |
| `notification must be in DRAFT status` | Cannot modify sent notifications | Only draft notifications can be edited |
| `segment not found` | Invalid segment ID | Verify segment ID exists in database |
| `failed to queue notification` | Kafka producer error | Check Kafka broker connectivity in `make logs-server` |

---

## Performance Considerations

### Notification Sending at Scale

- **Batch processing:** Worker processes notifications in batches
- **Horizontal scaling:** Run multiple worker instances for throughput
- **Kafka partitioning:** Distribute load across partitions
- **Database indexing:** Ensure queries on `notification_id`, `user_id`, `status` are fast
- **Connection pooling:** pgx handles connection pooling automatically

### Query Optimization

- List endpoints use pagination (default 10, max 100 items)
- Search queries have indexes on `name`, `email`, `external_id`
- Segment member lookups are indexed on `(segment_id, user_id)`
- Notification delivery lookups indexed on `notification_id`, `user_id`

---

## Contributing Guidelines

1. **Follow existing patterns** — Use module structure and middleware chain patterns
2. **Document changes** — Update relevant API docs in `/docs/api/`
3. **Add error handling** — Return specific domain errors with HTTP status codes
4. **Write tests** — Coverage for business logic (not required yet, but appreciated)
5. **Use sqlc** — Don't write raw SQL; update `/db/queries/` instead

---

## Security

### Implemented Protections

- ✅ **JWT Authentication** — Access & refresh token pair
- ✅ **API Key Validation** — All endpoints require `X-API-Key` header
- ✅ **Role-Based Access Control** — SUPER_ADMIN, ADMIN, OPERATOR roles
- ✅ **Password Hashing** — bcrypt with default cost
- ✅ **Audit Logging** — All staff actions recorded
- ✅ **SQL Injection Prevention** — sqlc type-safe queries

### Not Yet Implemented

- ⚠️ **Rate Limiting** — Add rate limiter middleware
- ⚠️ **HTTPS** — Use TLS in production
- ⚠️ **CORS** — Configure for web frontend
- ⚠️ **Input Sanitization** — Add validation middleware for all inputs

---

## Monitoring & Observability

### Current State

- Basic logging via `log` package
- Request logging via `middleware.Logging()`
- Audit trails for administrative actions

### Recommended Additions

1. **Structured logging** — Use `github.com/rs/zerolog` (already in go.mod)
2. **Metrics** — Prometheus for request rates, latency, errors
3. **Distributed tracing** — OpenTelemetry for end-to-end visibility
4. **Alerting** — Configure alerts for delivery failures, worker lag

---

## Resources

### Documentation

- **Database Schema:** `/docs/database.md`
- **API Endpoints:** `/docs/api/*.md`
- **Development Setup:** `/docs/development-guide.md`
- **Go Modules:** `go.mod`

### External Tools

- **sqlc** — SQL code generation
- **Air** — Hot-reload for development
- **Kafka UI** — http://localhost:8090 (local dev)
- **migrate** — Database migration tool

### Key Files

- **Configuration:** `internal/config/config.go`
- **Database Connection:** `internal/database/db.go`
- **Middleware Chain:** `internal/middleware/middleware.go`
- **Kafka Events:** `internal/kafka/events.go`
- **Audit Service:** `internal/audit/audit.go`

---

## Changelog

| Version | Date | Description |
|---------|------|-------------|
| 1.0 | 2026-09-10 | Initial comprehensive README for developer onboarding |

---

## Questions?

For detailed API usage, refer to endpoint-specific documentation in `/docs/api/`.  
For database schema details, see `/docs/database.md`.  
For development environment setup, see `/docs/development-guide.md`.

**Happy coding! 🚀**
