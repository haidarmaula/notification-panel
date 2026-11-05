package requeue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"hello/internal/dispatch"
)

const validInput = `{"application_id":"00000000-0000-4000-8000-000000000001","request_id":"00000000-0000-4000-8000-000000000002","change_reference":"INC-42","credential":"secret.assertion.value","positions":[{"log_source_id":"00000000-0000-4000-8000-000000000003","partition":0,"offset":7},{"log_source_id":"00000000-0000-4000-8000-000000000003","partition":0,"offset":8}]}`

type fakeSession struct {
	inspections, executions, closed int
	positions                       []int64
	run                             func(int) (dispatch.ReplayDisposition, error)
}

func (s *fakeSession) Inspect(_ context.Context, _ dispatch.ReplayRequest, _ string, position dispatch.ReplayPosition) (dispatch.ReplayAssessment, error) {
	s.positions = append(s.positions, position.Offset)
	s.inspections++
	d, err := s.run(s.inspections)
	return dispatch.ReplayAssessment{Disposition: d, NotificationID: 42}, err
}
func (s *fakeSession) ReplayOne(_ context.Context, _ dispatch.ReplayRequest, _ string, position dispatch.ReplayPosition) (dispatch.ReplayAttemptResult, error) {
	s.positions = append(s.positions, position.Offset)
	s.executions++
	d, err := s.run(s.executions)
	return dispatch.ReplayAttemptResult{Disposition: d, NotificationID: 42}, err
}
func (s *fakeSession) Close() error { s.closed++; return nil }

func TestRunDefaultDryRun(t *testing.T) {
	session := &fakeSession{run: func(int) (dispatch.ReplayDisposition, error) { return dispatch.ReplayEligible, nil }}
	var out, diagnostics bytes.Buffer
	code := Run(context.Background(), nil, strings.NewReader(validInput), &out, &diagnostics, func(_ context.Context, r dispatch.ReplayRequest, credential string, execute bool) (Session, error) {
		if execute || len(r.Positions) != 2 || credential != "secret.assertion.value" {
			t.Fatal("incorrect session input")
		}
		return session, nil
	})
	if code != 0 || session.inspections != 2 || session.executions != 0 || session.closed != 1 || diagnostics.Len() != 0 {
		t.Fatalf("code=%d session=%+v diagnostics=%s", code, session, diagnostics.String())
	}
	rows := decodeReports(t, out.Bytes())
	if len(rows) != 2 || rows[0].Mode != "inspect" || rows[0].Disposition != "eligible" || rows[1].Offset != 8 {
		t.Fatalf("reports: %+v", rows)
	}
	if strings.Contains(out.String(), "secret") || strings.Contains(out.String(), "INC-42") {
		t.Fatal("sensitive input leaked")
	}
}

func TestRunExecuteStopsOnFailure(t *testing.T) {
	tests := []struct {
		name        string
		failure     error
		outcome     dispatch.ReplayDisposition
		wantOutcome string
	}{
		{"uncertain result", dispatch.ErrReplayPublicationUnconfirmed, dispatch.ReplayPublishUnconfirmed, "publish_unconfirmed"},
		{"uncertain prior claim", dispatch.ErrReplayPublicationUnconfirmed, dispatch.ReplayStarted, "started"},
		{"database failure", errors.New("secret database payload"), "", ""},
		{"authority denied", dispatch.ErrReplayDenied, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := &fakeSession{run: func(n int) (dispatch.ReplayDisposition, error) {
				if n == 1 {
					return tt.outcome, tt.failure
				}
				t.Fatal("processed later position")
				return "", nil
			}}
			var out, diagnostics bytes.Buffer
			code := Run(context.Background(), []string{"--execute"}, strings.NewReader(validInput), &out, &diagnostics, func(context.Context, dispatch.ReplayRequest, string, bool) (Session, error) { return session, nil })
			if code != 1 || session.executions != 1 || session.inspections != 0 || session.closed != 1 {
				t.Fatalf("code=%d session=%+v", code, session)
			}
			rows := decodeReports(t, out.Bytes())
			if len(rows) != 1 || string(rows[0].Disposition) != tt.wantOutcome || rows[0].Error == "" {
				t.Fatalf("reports: %+v", rows)
			}
			if rows[0].NotificationID != 0 {
				t.Fatal("failed operation exposed unverified campaign")
			}
			if strings.Contains(out.String()+diagnostics.String(), "secret") {
				t.Fatal("error leaked private detail")
			}
		})
	}
}

func TestRunRetainsEarlierProgress(t *testing.T) {
	session := &fakeSession{run: func(n int) (dispatch.ReplayDisposition, error) {
		if n == 1 {
			return dispatch.ReplayPublished, nil
		}
		return "", errors.New("private detail")
	}}
	var out, diagnostics bytes.Buffer
	if code := Run(context.Background(), []string{"--execute"}, strings.NewReader(validInput), &out, &diagnostics, func(context.Context, dispatch.ReplayRequest, string, bool) (Session, error) { return session, nil }); code != 1 {
		t.Fatal(code)
	}
	rows := decodeReports(t, out.Bytes())
	if len(rows) != 2 || rows[0].Disposition != dispatch.ReplayPublished || rows[1].Error != "operation_failed" {
		t.Fatalf("reports: %+v", rows)
	}
}

func TestRunContinuesLogicalOutcomes(t *testing.T) {
	for _, outcome := range []dispatch.ReplayDisposition{dispatch.ReplayRejected, dispatch.ReplayPrepared, dispatch.ReplayTerminal, dispatch.ReplayExpired, dispatch.ReplayOnHold, dispatch.ReplayReconciliation, dispatch.ReplayResolved} {
		t.Run(string(outcome), func(t *testing.T) {
			session := &fakeSession{run: func(int) (dispatch.ReplayDisposition, error) { return outcome, nil }}
			var out, diagnostics bytes.Buffer
			if code := Run(context.Background(), []string{"--execute"}, strings.NewReader(validInput), &out, &diagnostics, func(context.Context, dispatch.ReplayRequest, string, bool) (Session, error) { return session, nil }); code != 0 || session.executions != 2 {
				t.Fatalf("code=%d calls=%d", code, session.executions)
			}
		})
	}
}

func TestRunRejectsInputBeforeOpeningSession(t *testing.T) {
	tests := []struct {
		name, input string
		args        []string
	}{
		{name: "unknown flag", input: validInput, args: []string{"--credential=secret"}},
		{name: "positional argument", input: validInput, args: []string{"secret"}},
		{name: "duplicate top field", input: strings.Replace(validInput, `"application_id":`, `"credential":"other","application_id":`, 1)},
		{name: "escaped duplicate", input: strings.Replace(validInput, `"application_id":`, `"cred\u0065ntial":"other","application_id":`, 1)},
		{name: "case alias", input: strings.Replace(validInput, `"credential":`, `"Credential":`, 1)},
		{name: "unknown field", input: strings.Replace(validInput, `"credential":`, `"actor":1,"credential":`, 1)},
		{name: "duplicate position key", input: strings.Replace(validInput, `"partition":0`, `"partition":0,"partition":1`, 1)},
		{name: "null partition", input: strings.Replace(validInput, `"partition":0`, `"partition":null`, 1)},
		{name: "missing offset", input: strings.Replace(validInput, `,"offset":7`, "", 1)},
		{name: "duplicate positions", input: strings.Replace(validInput, `"offset":8`, `"offset":7`, 1)},
		{name: "overflow", input: strings.Replace(validInput, `"partition":0`, `"partition":2147483648`, 1)},
		{name: "negative", input: strings.Replace(validInput, `"offset":7`, `"offset":-1`, 1)},
		{name: "trailing value", input: validInput + ` {}`},
		{name: "null selection", input: strings.Replace(validInput, `"positions":[`, `"positions":null,"ignored":[`, 1)},
		{name: "noncanonical UUID", input: strings.Replace(validInput, `00000000-0000-4000-8000-000000000001`, `00000000000040008000000000000001`, 1)},
		{name: "zero UUID", input: strings.Replace(validInput, `00000000-0000-4000-8000-000000000001`, `00000000-0000-0000-0000-000000000000`, 1)},
		{name: "empty credential", input: strings.Replace(validInput, "secret.assertion.value", "", 1)},
		{name: "oversized credential", input: strings.Replace(validInput, "secret.assertion.value", strings.Repeat("s", 8193), 1)},
		{name: "oversized input", input: strings.Repeat(" ", 64*1024) + validInput},
		{name: "control in reference", input: strings.Replace(validInput, "INC-42", `INC\n42`, 1)},
		{name: "invalid UTF8", input: strings.Replace(validInput, "INC-42", string([]byte{0xff}), 1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, diagnostics bytes.Buffer
			code := Run(context.Background(), tt.args, strings.NewReader(tt.input), &out, &diagnostics, func(context.Context, dispatch.ReplayRequest, string, bool) (Session, error) {
				t.Fatal("opened session for invalid input")
				return nil, nil
			})
			if code != 2 || out.Len() != 0 || diagnostics.Len() == 0 {
				t.Fatalf("code=%d out=%q diagnostics=%q", code, out.String(), diagnostics.String())
			}
			if strings.Contains(diagnostics.String(), "secret") || strings.Contains(diagnostics.String(), "INC") {
				t.Fatal("input leaked")
			}
		})
	}
}

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) { return 0, errors.New("sensitive write failure") }
func TestRunStopsWhenReportCannotBeWritten(t *testing.T) {
	session := &fakeSession{run: func(int) (dispatch.ReplayDisposition, error) { return dispatch.ReplayPublished, nil }}
	var diagnostics bytes.Buffer
	code := Run(context.Background(), []string{"--execute"}, strings.NewReader(validInput), failedWriter{}, &diagnostics, func(context.Context, dispatch.ReplayRequest, string, bool) (Session, error) { return session, nil })
	if code != 1 || session.executions != 1 || session.closed != 1 {
		t.Fatalf("code=%d session=%+v", code, session)
	}
	if strings.Contains(diagnostics.String(), "sensitive") {
		t.Fatal("write error leaked")
	}
}
func TestRunCancellationAndHelp(t *testing.T) {
	t.Run("canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		var out, diag bytes.Buffer
		if code := Run(ctx, nil, strings.NewReader(validInput), &out, &diag, func(context.Context, dispatch.ReplayRequest, string, bool) (Session, error) {
			t.Fatal("opened on cancellation")
			return nil, nil
		}); code != 1 {
			t.Fatal(code)
		}
	})
	t.Run("help", func(t *testing.T) {
		var out, diag bytes.Buffer
		if code := Run(context.Background(), []string{"--help"}, nil, &out, &diag, nil); code != 0 || !strings.Contains(out.String(), "stdin") || diag.Len() != 0 {
			t.Fatalf("code=%d out=%q diag=%q", code, out.String(), diag.String())
		}
	})
}
func decodeReports(t *testing.T, data []byte) []report {
	t.Helper()
	d := json.NewDecoder(bytes.NewReader(data))
	var rows []report
	for {
		var row report
		err := d.Decode(&row)
		if errors.Is(err, io.EOF) {
			return rows
		}
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
}

type closeFailureSession struct{ *fakeSession }

func (s closeFailureSession) Close() error { s.closed++; return errors.New("private broker address") }

func TestRunReportsShutdownFailure(t *testing.T) {
	s := &fakeSession{run: func(int) (dispatch.ReplayDisposition, error) { return dispatch.ReplayPublished, nil }}
	var out, diag bytes.Buffer
	code := Run(context.Background(), []string{"--execute"}, strings.NewReader(validInput), &out, &diag, func(context.Context, dispatch.ReplayRequest, string, bool) (Session, error) {
		return closeFailureSession{s}, nil
	})
	if code != 1 || s.closed != 1 || s.executions != 2 || len(decodeReports(t, out.Bytes())) != 2 {
		t.Fatalf("code=%d session=%+v", code, s)
	}
	if !strings.Contains(diag.String(), "shutdown failed") || strings.Contains(diag.String(), "private") {
		t.Fatal(diag.String())
	}
}

func TestRunRejectsUnexpectedServiceResults(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		outcome dispatch.ReplayDisposition
	}{
		{"private outcome", nil, "secret database message"},
		{"dry run publication", nil, dispatch.ReplayPublished},
		{"execute eligible", []string{"--execute"}, dispatch.ReplayEligible},
		{"uncertain without error", []string{"--execute"}, dispatch.ReplayPublishUnconfirmed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &fakeSession{run: func(int) (dispatch.ReplayDisposition, error) { return tt.outcome, nil }}
			var out, diag bytes.Buffer
			code := Run(context.Background(), tt.args, strings.NewReader(validInput), &out, &diag, func(context.Context, dispatch.ReplayRequest, string, bool) (Session, error) { return s, nil })
			if code != 1 || s.inspections+s.executions != 1 || s.closed != 1 {
				t.Fatalf("code=%d session=%+v", code, s)
			}
			rows := decodeReports(t, out.Bytes())
			if len(rows) != 1 || rows[0].Error == "" || rows[0].NotificationID != 0 {
				t.Fatalf("reports: %+v", rows)
			}
			if strings.Contains(out.String()+diag.String(), "secret") {
				t.Fatal("service result leaked")
			}
		})
	}
}

func TestRunCancellationBetweenPositions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &fakeSession{run: func(int) (dispatch.ReplayDisposition, error) { cancel(); return dispatch.ReplayPublished, nil }}
	var out, diag bytes.Buffer
	code := Run(ctx, []string{"--execute"}, strings.NewReader(validInput), &out, &diag, func(context.Context, dispatch.ReplayRequest, string, bool) (Session, error) { return s, nil })
	rows := decodeReports(t, out.Bytes())
	if code != 1 || s.executions != 1 || s.closed != 1 || len(rows) != 2 || rows[0].Disposition != dispatch.ReplayPublished || rows[1].Error != "canceled" {
		t.Fatalf("code=%d session=%+v rows=%+v", code, s, rows)
	}
}

func TestRunRedactsSessionStartupErrors(t *testing.T) {
	var out, diag bytes.Buffer
	code := Run(context.Background(), nil, strings.NewReader(validInput), &out, &diag, func(context.Context, dispatch.ReplayRequest, string, bool) (Session, error) {
		return nil, errors.New("postgres://user:secret@host/db")
	})
	if code != 1 || out.Len() != 0 || strings.Contains(diag.String(), "secret") {
		t.Fatalf("code=%d out=%q diag=%q", code, out.String(), diag.String())
	}
}

func TestRunPreservesSuppliedPositionOrder(t *testing.T) {
	session := &fakeSession{run: func(int) (dispatch.ReplayDisposition, error) { return dispatch.ReplayEligible, nil }}
	input := strings.Replace(validInput, `"offset":7`, `"offset":9`, 1)
	var out, diag bytes.Buffer
	code := Run(context.Background(), nil, strings.NewReader(input), &out, &diag, func(context.Context, dispatch.ReplayRequest, string, bool) (Session, error) { return session, nil })
	if code != 0 || len(session.positions) != 2 || session.positions[0] != 9 || session.positions[1] != 8 {
		t.Fatalf("code=%d positions=%v", code, session.positions)
	}
	rows := decodeReports(t, out.Bytes())
	if rows[0].Offset != 9 || rows[1].Offset != 8 {
		t.Fatalf("reports: %+v", rows)
	}
}
