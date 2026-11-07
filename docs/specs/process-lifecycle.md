# Process lifecycle and shutdown

Step 35 covers local ownership and cleanup of work in the four application
commands and the optional operator replay utility. Each process owns a single database pool. SIGINT and SIGTERM also
interrupt writes to process-owned stdout/stderr, so a full logging pipe cannot
prevent exit. Uninterrupted startup and runtime failures exit nonzero.

| Process | Shutdown contract |
|---|---|
| HTTP server | Startup allows 15 seconds for PostgreSQL. Header reads have a 5-second deadline, reads and writes 30 seconds, and idle connections 60 seconds. On signal the listener closes and in-flight handlers get up to 10 seconds to finish. A successful drain exits zero after pool close. On expiry the server cancels request contexts, closes remaining sockets, and waits up to another 5 seconds for handler cleanup. An interrupted drain exits nonzero. If a handler still owns resources at hard stop, the executable exits directly and the OS reclaims its connections. |
| Bootstrap | The signal context bounds startup to 15 seconds and the complete provisioning job to one minute. Repository operations inherit this context. A provisioning failure or interrupted job returns through deferred pool cleanup and exits nonzero. |
| Admission and delivery workers | Generation cancellation ends admission fetches and joins partition readers. Delivery cancellation joins device submissions and lease renewal before closing the FCM client, Kafka producer and pool. Incomplete provider outcomes remain fenced by the persisted lease and attempt. |
| Scheduler | Cancellation joins the schedule and publication loops before closing the producer and pool. A producer drain error remains a failure even if cancellation occurred. |

Application mutation rollback has an independent one-second cleanup deadline
when request cancellation interrupts a transaction. It cannot extend the HTTP
grace period indefinitely. Configuration errors in legacy server/bootstrap
environment loaders exit before a database pool is created.

The Unix entrypoints adopt inherited blocking pipe/socket descriptors as
pollable Go files before starting work. They restore the inherited status flags
on exit. The operator replay command does the same for a partial stdin request.
A subprocess regression fills a blocking pipe, leaves it unread across exec
and checks that cancellation still exits and restores the original flags.

The focused HTTP tests use a real listener to exercise a successful in-flight
response, forced cancellation followed by handler cleanup, and a handler that
ignores its context through the hard deadline. The pool test stalls a PostgreSQL
handshake and checks that cancelling startup closes the accepted connection.
Existing worker and scheduler tests exercise joined polling, partition shutdown,
lease-renewal cancellation, producer-drain failure and blocked output pipes.
The disposable worker executable fixture sends SIGTERM and checks process exit.

The Kafka producer and the database pool must finish their library-owned cleanup.
Deployment supervisors must allow time for those drains and set their own final
termination deadline. These local checks do not measure a production drain under
broker outage, network partition or peak traffic.
