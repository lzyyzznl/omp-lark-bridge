package core

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// recordingStreamer records every fullText pushed by the pusher, with timing.
type recordingStreamer struct {
	mu    sync.Mutex
	texts []string
	at    []time.Time
}

func (s *recordingStreamer) StreamRichCardText(_ context.Context, _ any, fullText string) error {
	s.mu.Lock()
	s.texts = append(s.texts, fullText)
	s.at = append(s.at, time.Now())
	s.mu.Unlock()
	return nil
}

func (s *recordingStreamer) snapshot() ([]string, []time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.texts...), append([]time.Time(nil), s.at...)
}

func waitForCondition(t *testing.T, cond func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

// A burst of token updates must be coalesced: the pusher sends far fewer frames
// than updates and always ends on the final text. This is the regression guard
// for "agent finished but the card was still catching up one HTTP round-trip per
// chunk".
func TestRichCardPusher_CoalescesBurstUpdates(t *testing.T) {
	s := &recordingStreamer{}
	p := newRichCardPusherWithInterval(context.Background(), s, nil, 20*time.Millisecond)

	const updates = 200
	for i := range updates {
		p.Update(fmt.Sprintf("chunk-%d", i))
	}
	p.Final(fmt.Sprintf("chunk-%d", updates-1))

	texts, _ := s.snapshot()
	if len(texts) == 0 {
		t.Fatal("pusher sent nothing")
	}
	if len(texts) >= updates {
		t.Fatalf("pusher sent %d frames for %d updates, want coalescing", len(texts), updates)
	}
	if last := texts[len(texts)-1]; last != fmt.Sprintf("chunk-%d", updates-1) {
		t.Fatalf("last frame = %q, want the final text", last)
	}
}

// Final must force the last frame even when it arrives inside the throttle
// window, and must stop the goroutine so later updates cannot overwrite it.
func TestRichCardPusher_FinalForcesLastFrame(t *testing.T) {
	s := &recordingStreamer{}
	p := newRichCardPusherWithInterval(context.Background(), s, nil, time.Second)

	p.Update("early")
	p.Final("final")

	texts, _ := s.snapshot()
	if len(texts) == 0 || texts[len(texts)-1] != "final" {
		t.Fatalf("frames = %v, want final as the last frame", texts)
	}

	// Updates after Final are ignored.
	p.Update("after-final")
	sentAfter := len(texts)
	time.Sleep(20 * time.Millisecond)
	if again, _ := s.snapshot(); len(again) != sentAfter {
		t.Fatalf("pusher kept sending after Final: %v", again)
	}
}

// Close stops the pusher without flushing a pending frame, so a caller that is
// about to render an error/done card is not raced by a stale partial text.
func TestRichCardPusher_CloseDoesNotFlush(t *testing.T) {
	s := &recordingStreamer{}
	p := newRichCardPusherWithInterval(context.Background(), s, nil, time.Second)
	p.Update("never-sent")

	done := make(chan struct{})
	go func() {
		p.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close blocked")
	}

	time.Sleep(20 * time.Millisecond)
	if texts, _ := s.snapshot(); len(texts) != 0 {
		t.Fatalf("Close flushed %v, want no frame", texts)
	}
}

// Consecutive frames must respect the minimum interval so the pusher cannot
// trip the platform's per-card rate limit.
func TestRichCardPusher_RespectsMinInterval(t *testing.T) {
	const interval = 40 * time.Millisecond
	s := &recordingStreamer{}
	p := newRichCardPusherWithInterval(context.Background(), s, nil, interval)

	p.Update("first")
	waitForCondition(t, func() bool {
		texts, _ := s.snapshot()
		return len(texts) == 1
	}, time.Second)

	p.Update("second")
	waitForCondition(t, func() bool {
		texts, _ := s.snapshot()
		return len(texts) == 2
	}, time.Second)
	p.Close()

	_, at := s.snapshot()
	if len(at) != 2 {
		t.Fatalf("timestamps = %d, want 2", len(at))
	}
	if gap := at[1].Sub(at[0]); gap < interval/2 {
		t.Fatalf("frame gap = %v, want at least ~%v", gap, interval)
	}
}

// Context cancellation must stop the goroutine without Close or Final.
func TestRichCardPusher_ContextCancelStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &recordingStreamer{}
	p := newRichCardPusherWithInterval(ctx, s, nil, time.Second)
	p.Update("pending")
	cancel()

	select {
	case <-p.done:
	case <-time.After(time.Second):
		t.Fatal("pusher did not stop on context cancellation")
	}
	p.Close() // must return immediately
}
