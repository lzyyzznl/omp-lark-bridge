package core

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// richCardMinPushInterval bounds how often the background pusher calls the
// platform's streaming-text API. Feishu's CardKit accepts roughly 10 updates
// per second per card; 150ms (~6.7/s) leaves headroom for the final flush and
// for 429 backoff.
const richCardMinPushInterval = 150 * time.Millisecond

// richCardPusher decouples agent output from the network. Update only records
// the latest cumulative text and never blocks the event loop; one background
// goroutine pushes that snapshot at most once per interval.
//
// This is latest-value conflation + single-flight: a frame superseded before it
// is sent is simply dropped, because every frame carries the full accumulated
// text — dropping intermediate frames never loses content. Observable lag is
// bounded by one interval plus one request round-trip no matter how fast the
// agent produces tokens, and a slow API can never block the event loop.
type richCardPusher struct {
	streamer RichCardTextStreamer
	handle   any
	interval time.Duration

	ctx    context.Context
	wake   chan struct{} // new cumulative text available
	urgent chan struct{} // Final/Close: must interrupt a throttle wait
	done   chan struct{}

	mu       sync.Mutex
	latest   string
	sent     string
	lastPush time.Time
	final    bool
	closed   bool
}

func newRichCardPusher(ctx context.Context, streamer RichCardTextStreamer, handle any) *richCardPusher {
	return newRichCardPusherWithInterval(ctx, streamer, handle, richCardMinPushInterval)
}

// newRichCardPusherWithInterval is the test seam for a deterministic interval.
func newRichCardPusherWithInterval(ctx context.Context, streamer RichCardTextStreamer, handle any, interval time.Duration) *richCardPusher {
	p := &richCardPusher{
		streamer: streamer,
		handle:   handle,
		interval: interval,
		lastPush: time.Now(),
		ctx:      ctx,
		wake:     make(chan struct{}, 1),
		urgent:   make(chan struct{}, 1),
		done:     make(chan struct{}),
	}
	go p.run()
	return p
}

// Update records the latest full text without blocking. Intermediate snapshots
// may be coalesced away; the next flush always sends the newest text.
func (p *richCardPusher) Update(text string) {
	p.mu.Lock()
	if p.closed || text == p.latest {
		p.mu.Unlock()
		return
	}
	p.latest = text
	p.mu.Unlock()
	p.signal()
}

// Final pushes the final text (forced, ignoring the interval) and waits for the
// goroutine to stop. Call it before the terminal card Patch so a late in-flight
// frame cannot overwrite the finished card.
func (p *richCardPusher) Final(text string) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.latest = text
	p.final = true
	p.closed = true
	p.mu.Unlock()
	p.signalUrgent()
	<-p.done
}

// Close stops the pusher without sending another frame. Used on error and
// silent paths where the caller is about to replace the card.
func (p *richCardPusher) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	p.mu.Unlock()
	p.signalUrgent()
	<-p.done
}

func (p *richCardPusher) signal() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// signalUrgent wakes the goroutine when Final/Close must not wait out the
// throttle window. New text must NOT use this channel: otherwise a steady
// token stream keeps interrupting the wait and the pusher degenerates into
// one HTTP call per Update, which is exactly what the interval prevents.
func (p *richCardPusher) signalUrgent() {
	select {
	case p.urgent <- struct{}{}:
	default:
	}
}

func (p *richCardPusher) run() {
	defer close(p.done)
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-p.wake:
		case <-p.urgent:
		}

		p.mu.Lock()
		text := p.latest
		sent := p.sent
		final := p.final
		closed := p.closed
		p.mu.Unlock()

		if closed && !final {
			return
		}
		if text == sent {
			if final {
				return
			}
			continue
		}

		if !final {
			if !p.waitInterval() {
				return
			}
			// Re-read after the wait: updates may have arrived meanwhile, and
			// conflation means only the newest snapshot matters.
			p.mu.Lock()
			text = p.latest
			final = p.final
			closed = p.closed
			p.mu.Unlock()
			if closed && !final {
				return
			}
			if text == sent {
				continue
			}
		}

		if err := p.streamer.StreamRichCardText(p.ctx, p.handle, text); err != nil {
			slog.Debug("rich card pusher: streaming update failed", "error", err)
		} else {
			p.mu.Lock()
			p.sent = text
			p.lastPush = time.Now()
			p.mu.Unlock()
		}

		if final {
			return
		}
	}
}

// waitInterval blocks until the minimum interval since the last push elapsed.
// Only Final/Close cut it short; new text arrives on wake, which is deliberately
// not selected here.
// Returns false when the context is done.
func (p *richCardPusher) waitInterval() bool {
	p.mu.Lock()
	wait := p.interval - time.Since(p.lastPush)
	p.mu.Unlock()
	if wait <= 0 {
		return true
	}
	select {
	case <-p.ctx.Done():
		return false
	case <-p.urgent:
		return true
	case <-time.After(wait):
		return true
	}
}
