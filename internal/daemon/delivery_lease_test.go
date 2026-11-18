package daemon

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func TestDeliveryLeaseWaitsForActivationAndJoinsWorkOnLoss(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		checks := 0
		started := make(chan struct{})
		joined := make(chan struct{})
		done := make(chan error, 1)
		lease := deliveryLease{refresh: func(context.Context) (bool, error) {
			checks++
			return checks == 2, nil
		}}
		go func() {
			done <- lease.run(t.Context(), func(ctx context.Context) error {
				close(started)
				<-ctx.Done()
				close(joined)
				return context.Cause(ctx)
			})
		}()
		time.Sleep(deliveryLeaseRefresh + time.Millisecond)
		synctest.Wait()
		select {
		case <-started:
			t.Fatal("delivery ran while the generation was only prepared")
		default:
		}
		<-started
		if err := <-done; !errors.Is(err, errDeliveryLease) {
			t.Fatalf("expected revoked generation to stop delivery, got %v", err)
		}
		select {
		case <-joined:
		default:
			t.Fatal("delivery did not join before returning")
		}
	})
}

func TestDeliveryLeaseStopsBeforeActivationOnFailedRenewal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		lease := deliveryLease{refresh: func(context.Context) (bool, error) {
			return false, errDeliveryLease
		}}
		if err := lease.run(t.Context(), func(context.Context) error {
			t.Error("delivery started without an active lease")
			return nil
		}); !errors.Is(err, errDeliveryLease) {
			t.Fatalf("expected lease failure, got %v", err)
		}
	})
}
