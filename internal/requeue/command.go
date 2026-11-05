// Package requeue adapts scoped replay services to a bounded, serial CLI protocol.
// It contains no permissive authority fallback or implicit production connection.
package requeue

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"hello/internal/dispatch"
)

// Session owns the replay services and their database/publisher resources.
// Close must stop further calls and drain producer-owned I/O before releasing
// source ownership. A local call timeout does not establish that drain.
type Session interface {
	Inspect(context.Context, dispatch.ReplayRequest, string, dispatch.ReplayPosition) (dispatch.ReplayAssessment, error)
	ReplayOne(context.Context, dispatch.ReplayRequest, string, dispatch.ReplayPosition) (dispatch.ReplayAttemptResult, error)
	Close() error
}

// OpenFunc supplies trusted deployment wiring after syntactic input validation.
// It must verify the credential, configured source/manifest and transport policy
// before enabling execution. On error it must clean up any resources it created.
// Neither caller-supplied input nor an ordinary staff role can replace these gates.
type OpenFunc func(context.Context, dispatch.ReplayRequest, string, bool) (Session, error)

type report struct {
	RequestID      string                     `json:"request_id"`
	Mode           string                     `json:"mode"`
	LogSourceID    string                     `json:"log_source_id"`
	Partition      int32                      `json:"partition"`
	Offset         int64                      `json:"offset"`
	Disposition    dispatch.ReplayDisposition `json:"disposition,omitempty"`
	NotificationID int64                      `json:"notification_id,omitempty"`
	Error          string                     `json:"error,omitempty"`
}

const help = `Usage: requeue [--execute]

Read one UTF-8 JSON object from stdin (maximum 64 KiB):
application_id, request_id, change_reference, credential, and positions.
Each position requires log_source_id, partition, and offset; select 1-100 unique positions.
Never place credentials in argv. Default mode is an authorized, advisory dry run.
--execute commits each position separately and stops on failure or uncertainty.
stdout contains one JSON result per processed position; diagnostics go to stderr.
Exit codes: 0 completed, 1 runtime/authority/uncertainty failure, 2 invalid input.
`

// Run validates input before opening a session, then processes positions in the
// supplied order without retries. It writes each safe result immediately, keeping
// earlier committed progress visible if a later operation fails. A failed output
// write stops subsequent operations; it cannot undo previously committed work.
// Callers own the input stream and must arrange interruptible reads on shutdown.
// The session always closes before Run returns, including after cancellation.
func Run(ctx context.Context, args []string, input io.Reader, output, diagnostics io.Writer, open OpenFunc) (code int) {
	flags := flag.NewFlagSet("requeue", flag.ContinueOnError)
	flags.SetOutput(io.Discard) // Flag errors can echo credential-bearing argv.
	execute := flags.Bool("execute", false, "execute the selected replay positions")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			if _, err := io.WriteString(output, help); err != nil {
				return 1
			}
			return 0
		}
		diagnostic(diagnostics, "invalid arguments; use --help")
		return 2
	}
	if flags.NArg() != 0 {
		diagnostic(diagnostics, "invalid arguments; use --help")
		return 2
	}
	if ctx.Err() != nil {
		diagnostic(diagnostics, "replay canceled")
		return 1
	}
	request, credential, err := decodeInput(input)
	if err != nil {
		diagnostic(diagnostics, "invalid replay input; use --help")
		return 2
	}
	if ctx.Err() != nil {
		diagnostic(diagnostics, "replay canceled")
		return 1
	}
	if open == nil {
		diagnostic(diagnostics, "replay configuration unavailable")
		return 1
	}
	session, err := open(ctx, request, credential, *execute)
	if err != nil {
		diagnostic(diagnostics, errorCode(err))
		return 1
	}
	if session == nil {
		diagnostic(diagnostics, "replay configuration unavailable")
		return 1
	}
	defer func() {
		if session.Close() != nil {
			diagnostic(diagnostics, "replay shutdown failed; reconcile before retry")
			code = 1
		}
	}()
	mode := "inspect"
	if *execute {
		mode = "execute"
	}
	encoder := json.NewEncoder(output)
	for _, position := range request.Positions {
		row := report{RequestID: request.ID.String(), Mode: mode, LogSourceID: position.LogSourceID.String(), Partition: position.Partition, Offset: position.Offset}
		var outcome dispatch.ReplayDisposition
		var notificationID int64
		err = ctx.Err()
		if err == nil {
			if *execute {
				var result dispatch.ReplayAttemptResult
				result, err = session.ReplayOne(ctx, request, credential, position)
				outcome = result.Disposition
				notificationID = result.NotificationID
			} else {
				var result dispatch.ReplayAssessment
				result, err = session.Inspect(ctx, request, credential, position)
				outcome = result.Disposition
				notificationID = result.NotificationID
			}
		}
		if err == nil && uncertain(outcome) {
			err = dispatch.ErrReplayPublicationUnconfirmed
		}
		if err == nil && (!validOutcome(outcome, *execute) || notificationID < 0) {
			err = errors.New("invalid replay service result")
		}
		if err != nil {
			row.Error = errorCode(err)
			if errors.Is(err, dispatch.ErrReplayPublicationUnconfirmed) && uncertain(outcome) {
				row.Disposition = outcome
			}
		} else {
			row.Disposition = outcome
			row.NotificationID = notificationID
		}
		if encoder.Encode(row) != nil {
			diagnostic(diagnostics, "replay report write failed; reconcile before retry")
			return 1
		}
		if err != nil {
			diagnostic(diagnostics, "replay stopped; retain this request identity for reconciliation")
			return 1
		}
	}
	return 0
}

func diagnostic(writer io.Writer, message string) { _, _ = fmt.Fprintln(writer, message) }

func uncertain(outcome dispatch.ReplayDisposition) bool {
	return outcome == dispatch.ReplayStarted || outcome == dispatch.ReplayPublishUnconfirmed || outcome == dispatch.ReplayAbandoned
}

func validOutcome(outcome dispatch.ReplayDisposition, execute bool) bool {
	switch outcome {
	case dispatch.ReplayEligible:
		return !execute
	case dispatch.ReplayPublished:
		return execute
	case dispatch.ReplayRejected, dispatch.ReplayPrepared, dispatch.ReplayTerminal, dispatch.ReplayExpired, dispatch.ReplayOnHold, dispatch.ReplayReconciliation, dispatch.ReplayResolved:
		return true
	default:
		return false
	}
}

func errorCode(err error) string {
	switch {
	case errors.Is(err, dispatch.ErrReplayCredential):
		return "credential_rejected"
	case errors.Is(err, dispatch.ErrReplayDenied):
		return "denied"
	case errors.Is(err, dispatch.ErrReplayHeld):
		return "held"
	case errors.Is(err, dispatch.ErrReplayConflict):
		return "request_conflict"
	case errors.Is(err, dispatch.ErrReplayPublicationUnconfirmed):
		return "publication_unconfirmed"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	default:
		return "operation_failed"
	}
}
