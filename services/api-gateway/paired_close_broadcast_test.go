package main

import (
	"testing"
	"time"
)

// TestBroadcastRegistry_DeliversToSubscriber: a message sent for an account reaches every
// channel currently subscribed for that account.
func TestBroadcastRegistry_DeliversToSubscriber(t *testing.T) {
	s := &Server{}
	s.initBroadcastRegistry()

	ch, unsub := s.subscribeBroadcast("acc-1")
	defer unsub()

	s.broadcast("acc-1", "hello")

	select {
	case got := <-ch:
		if got != "hello" {
			t.Errorf("got %v, want %q", got, "hello")
		}
	default:
		t.Fatal("expected a message on the subscriber channel, got none")
	}
}

// TestBroadcastRegistry_DoesNotCrossAccounts: a message sent for one account must not
// reach a subscriber registered for a different account.
func TestBroadcastRegistry_DoesNotCrossAccounts(t *testing.T) {
	s := &Server{}
	s.initBroadcastRegistry()

	chA, unsubA := s.subscribeBroadcast("acc-a")
	defer unsubA()
	chB, unsubB := s.subscribeBroadcast("acc-b")
	defer unsubB()

	s.broadcast("acc-a", "for-a")

	select {
	case got := <-chA:
		if got != "for-a" {
			t.Errorf("chA got %v, want %q", got, "for-a")
		}
	default:
		t.Fatal("expected chA to receive the message")
	}
	select {
	case got := <-chB:
		t.Fatalf("chB must not receive account acc-a's message, got %v", got)
	default:
		// correct — nothing delivered
	}
}

// TestBroadcastRegistry_NonBlockingDropWhenFull: broadcasting to a subscriber whose
// channel is already full must not block the caller — the message is dropped for that
// slow subscriber rather than stalling every other subscriber/account.
func TestBroadcastRegistry_NonBlockingDropWhenFull(t *testing.T) {
	s := &Server{}
	s.initBroadcastRegistry()

	_, unsub := s.subscribeBroadcast("acc-slow")
	defer unsub()

	// Fill the channel to capacity (matches the buffer size subscribeBroadcast uses, 8 —
	// if that constant changes, this test's fill loop must match it) plus one extra send
	// that must be silently dropped rather than blocking. Nothing ever drains the
	// subscriber channel, so if broadcast() were blocking, this goroutine would hang
	// forever past the 8th send.
	//
	// NOTE: deliberately not using select{case <-done: ...; case <-ch: t.Fatal(...)} here.
	// Once the first message lands in ch's buffer, ch is immediately and permanently
	// ready to receive, while done only becomes ready after all 20 sends finish — so a
	// two-way select races two "ready" cases and Go picks between them pseudo-randomly,
	// making that form fail deterministically regardless of whether broadcast blocks.
	// A timeout is the correct way to prove absence of blocking.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 20; i++ {
			s.broadcast("acc-slow", i)
		}
		close(done)
	}()
	select {
	case <-done:
		// correct — broadcast never blocked even though nothing drained ch
	case <-time.After(2 * time.Second):
		t.Fatal("broadcast blocked despite full, undrained subscriber channel")
	}
}

// TestBroadcastRegistry_UnsubscribeRemovesChannel: after unsub(), further broadcasts for
// that account must not be sent to the now-removed channel.
func TestBroadcastRegistry_UnsubscribeRemovesChannel(t *testing.T) {
	s := &Server{}
	s.initBroadcastRegistry()

	ch, unsub := s.subscribeBroadcast("acc-2")
	unsub()

	s.broadcast("acc-2", "after-unsub")

	select {
	case got := <-ch:
		t.Fatalf("unsubscribed channel must not receive further broadcasts, got %v", got)
	default:
		// correct
	}
}
