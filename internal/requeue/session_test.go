package requeue

import (
	"context"
	"errors"
	"strings"
	"testing"

	"hello/internal/dispatch"
)

func TestOpenRejectsCredentialBeforeNetworkWork(t *testing.T) {
	clearPostgresEnvironment(t)
	cfg, err := LoadConfiguration(strings.NewReader(configurationJSON(t, validConfiguration(t))))
	if err != nil {
		t.Fatal(err)
	}
	request, credential, err := decodeInput(strings.NewReader(validInput))
	if err != nil {
		t.Fatal(err)
	}
	session, err := cfg.Open(context.Background(), request, credential, true)
	if session != nil || !errors.Is(err, dispatch.ErrReplayCredential) {
		t.Fatalf("unverified credential reached startup: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	session, err = cfg.Open(ctx, request, credential, false)
	if session != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("ignored canceled startup")
	}
}
func TestSessionInspectionCannotEnableExecution(t *testing.T) {
	s := &replaySession{}
	result, err := s.ReplayOne(context.Background(), dispatch.ReplayRequest{}, "", dispatch.ReplayPosition{})
	if !errors.Is(err, dispatch.ErrReplayDenied) || result.Claimed {
		t.Fatal("inspection session enabled execution")
	}
	if s.Close() != nil || s.Close() != nil {
		t.Fatal("empty close failed")
	}
}
func TestStartupErrorsAreSanitized(t *testing.T) {
	private := errors.New("postgres://user:secret@host private broker payload")
	if got := safeStartupError(private); got != errSession || strings.Contains(got.Error(), "secret") {
		t.Fatal("startup details leaked")
	}
	if safeStartupError(errors.Join(private, dispatch.ErrReplayDenied)) != dispatch.ErrReplayDenied {
		t.Fatal("lost denial classification")
	}
}
