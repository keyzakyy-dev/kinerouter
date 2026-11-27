package auth

import (
	"testing"
	"time"
)

// The reference login limiter locks after every five failures and advances the
// step only then; a single failure after the first lock must not jump from 30 s
// to 2 min, and the one-hour window follows the latest failure rather than the
// bucket's creation.
func TestLimiterMatchesReferenceProgression(t *testing.T) {
	l := NewLimiter()
	for i := 0; i < 4; i++ {
		l.Fail("ip")
	}
	if wait, ok := l.Check("ip"); !ok || wait != 0 {
		t.Fatalf("bucket locked before 5 failures: %v %v", wait, ok)
	}
	l.Fail("ip")
	wait, ok := l.Check("ip")
	if ok || wait <= 0 || wait > 30*time.Second {
		t.Fatalf("first lock = %v (allowed %v), want about 30s", wait, ok)
	}

	// Let the first lock expire, then confirm one failure is still harmless.
	b := l.state["ip"]
	b.lockedTill = time.Now().Add(-time.Second)
	if _, ok := l.Check("ip"); !ok {
		t.Fatal("bucket still locked after expiry")
	}
	l.Fail("ip")
	wait, ok = l.Check("ip")
	if ok || wait != 0 {
		t.Fatalf("one failure after lock escalated immediately: %v %v", wait, ok)
	}

	for i := 0; i < 4; i++ {
		l.Fail("ip")
	}
	wait, ok = l.Check("ip")
	if ok || wait <= 30*time.Second || wait > 2*time.Minute {
		t.Fatalf("second lock = %v (allowed %v), want about 2m", wait, ok)
	}
}

// An idle bucket is dropped even when its lock already expired, so a stale
// lockLevel cannot punish a client that returns after more than an hour.
func TestLimiterIdleWindowResetsState(t *testing.T) {
	l := NewLimiter()
	for i := 0; i < 5; i++ {
		l.Fail("ip")
	}
	b := l.state["ip"]
	b.lastFailAt = time.Now().Add(-2 * time.Hour)
	b.lockedTill = time.Now().Add(-time.Hour)

	if _, ok := l.Check("ip"); !ok {
		t.Fatal("stale bucket still locked")
	}
	for i := 0; i < 5; i++ {
		l.Fail("ip")
	}
	wait, ok := l.Check("ip")
	if ok || wait <= 0 || wait > 30*time.Second {
		t.Fatalf("stale lock level carried over: %v %v", wait, ok)
	}
}
