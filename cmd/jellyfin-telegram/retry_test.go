package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetryTriesAgainAfterAFailureAndStopsWhenItWorks(t *testing.T) {
	calls := 0
	err := retry(context.Background(), 5, time.Millisecond, nil, "step", func() error {
		calls++
		if calls < 3 {
			return errors.New("CONNECTION_ERROR")
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("calls %d err %v", calls, err)
	}
}

func TestRetryGivesUpWithTheLastError(t *testing.T) {
	calls := 0
	err := retry(context.Background(), 3, time.Millisecond, nil, "step", func() error {
		calls++
		return errors.New("still down")
	})
	if err == nil || calls != 3 {
		t.Fatalf("calls %d err %v", calls, err)
	}
}

func TestRetryStopsWhenTheContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	_ = retry(ctx, 5, time.Hour, nil, "step", func() error { calls++; return errors.New("x") })
	if calls != 1 {
		t.Fatalf("calls %d after the program was asked to stop", calls)
	}
}
