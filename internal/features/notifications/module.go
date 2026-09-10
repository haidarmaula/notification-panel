package notifications

import (
	"hello/internal/audit"
	"hello/internal/config"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/kafka"
	"hello/internal/middleware"
)

// NotificationModule represents the notification feature module.
//
// It is the composition root of the notification feature: it owns the HTTP
// handler, holds the ordered chain of middlewares that gate the feature's
// endpoints, and is the unit that cmd/server constructs once at startup. It
// follows the same shape as the auth, mobile, and staff modules, with one
// structural difference — it wires a Kafka producer and an audit service in
// addition to the repositories, making it the most dependency-heavy module
// in the codebase.
//
// # What the module owns
//
// On construction, the module builds and retains:
//
//   - Seven repositories (notification, target, delivery, read, staff,
//     template, segment) built from the shared sqlc.Queries.
//   - An audit service built on a dedicated audit-log repository.
//   - A Kafka producer targeting cfg.SendTopic.
//   - A NotificationService composed from all of the above.
//   - A NotificationHandler wrapping the service.
//
// The module does not own the dependencies it receives — the sqlc.Queries,
// the config, and the middlewares belong to the caller — and it does not
// expose any of its internal repositories or the producer to the caller.
//
// # Producer lifetime
//
// The Kafka producer is created inside the constructor and is not exposed
// via an exported method. Because Producer.Close is never called by this
// module, the writer's background goroutine and its broker connections
// remain open until the process exits. This is acceptable for a
// long-running API server, which terminates as a whole on shutdown; it
// would become a leak in any context where modules are constructed and
// discarded repeatedly. If a future revision needs to close the producer
// gracefully, the module will need to expose a Close method that delegates
// to it.
//
// # Concurrency
//
// The module is safe for concurrent use. Its fields are set once in the
// constructor and never mutated; RegisterRoutes only reads them.
type NotificationModule struct {
	middlewares []middleware.Middleware
	handler     *NotificationHandler
}

// NewNotificationModule creates a new NotificationModule instance.
//
// # Wiring
//
// The constructor performs the full dependency-injection chain for the
// notification feature in a single call:
//
//  1. Builds the seven repositories the feature needs from the supplied
//     sqlc.Queries. This is the largest repository fan-out of any feature
//     module: the service needs read and write access across notifications,
//     their targets, their deliveries, per-user reads, the acting staff user
//     (for the authorisation check on create/update/delete/send), templates
//     (for template-existence validation), and segments (for
//     segment-existence validation).
//  2. Builds an audit-log repository and an audit.AuditService on top of
//     it. The audit service is a cross-feature dependency: the notification
//     service records staff actions through it, but the audit package has
//     no knowledge of the notification feature.
//  3. Builds a Kafka producer targeting cfg.SendTopic. The producer is used
//     by the service's Send method to publish a NotificationSendRequested
//     event; the worker consumes it asynchronously. Note that the service
//     receives a producer constructed for the *send* topic only — the
//     worker's delivery-update topic is not wired here, because the API
//     server does not publish delivery updates.
//  4. Builds a NotificationService from the repositories, the audit
//     service, and the producer.
//  5. Builds a NotificationHandler from the service.
//  6. Returns the module with the supplied middlewares and the handler.
//
// # Parameters
//
// queries must be non-nil and is expected to be the shared instance from
// cmd/server. cfg must be non-nil and must have KafkaBroker and SendTopic
// populated; the config loader guarantees both, and an empty value would
// surface as a broker-connection failure at the first publish rather than
// at construction.
//
// The middlewares parameter is variadic so that the caller can pass
// whichever cross-cutting concerns apply to the notification endpoints —
// in the current wiring the API-key, JWT, and audit middlewares — without
// the module hardcoding a chain. The order in which they are supplied is
// the order in which they are applied; see middleware.Chain for the
// composition rule.
//
// # Concurrency
//
// Intended to be called once during process startup, before the HTTP server
// begins listening.
func NewNotificationModule(queries *sqlc.Queries, cfg *config.ServerConfig, middlewares ...middleware.Middleware) *NotificationModule {
	notifRepo := repository.NewNotificationRepository(queries)
	targetRepo := repository.NewNotificationTargetRepository(queries)
	deliveryRepo := repository.NewNotificationDeliveryRepository(queries)
	readRepo := repository.NewNotificationReadRepository(queries)
	staffRepo := repository.NewStaffUserRepository(queries)
	templateRepo := repository.NewTemplateRepository(queries)
	segmentRepo := repository.NewSegmentRepository(queries)

	auditRepo := repository.NewAuditLogRepository(queries)
	auditService := audit.NewAuditService(auditRepo)

	producer := kafka.NewProducer(cfg.KafkaBroker, cfg.SendTopic)
	service := NewNotificationService(
		notifRepo, targetRepo, deliveryRepo, readRepo,
		staffRepo, templateRepo, segmentRepo, auditService, producer,
	)

	handler := NewNotificationHandler(service)

	return &NotificationModule{
		middlewares: middlewares,
		handler:     handler,
	}
}
