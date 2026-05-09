// Package sse hosts a tiny in-process event bus + HTTP handler
// that browsers consume via the EventSource API. Replaces the
// 30-second client polling loop on /problems and /anomalies for
// state-change events: a Problem opens / resolves, an anomaly
// fires / clears, and the page updates immediately instead of
// waiting up to 30s for the next poll.
//
// The bus is intentionally simple — one in-process broker, no
// Redis pub/sub, no message replay, no per-subscriber backlog.
// The trade-off: in a multi-replica deployment, an event
// produced on replica A is only seen by clients connected to
// replica A. That's acceptable because:
//
//  1. Each replica also still polls the database periodically;
//     SSE is for low-latency push, polling is the safety net.
//  2. The browser EventSource auto-reconnects on disconnect;
//     during reconnect the client re-runs whatever React Query
//     refetch its hook scheduled, so any missed events surface
//     as the regular cache refresh.
//  3. Adding Redis pub/sub later is a 1-file diff if cross-
//     replica fan-out becomes necessary.
//
// Wire format: standard SSE (text/event-stream). Each message
// is a JSON object { kind, payload } so the client can tell
// "problem opened" from "anomaly cleared" without one endpoint
// per event type.
package sse

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

// Event is the wire envelope. Kind is short ("problem.open",
// "problem.resolve", "anomaly.open", "anomaly.clear") so the
// client switch is one comparison. Payload is opaque JSON the
// receiver decodes if it cares about the details (e.g. the
// problem's service for badge counts).
type Event struct {
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Broker is the in-process pub/sub bus. Producers (evaluator,
// anomaly detector) call Publish; consumers (HTTP handler) call
// Subscribe to get a channel that receives every future event
// until the request context cancels.
//
// Channels are buffered (32) so a slow client doesn't block the
// producer. If the buffer fills (operator opens 100 tabs and
// pauses one) we drop events for that subscriber rather than
// stalling — the client's React Query polling will pick up the
// state on its next refetch.
type Broker struct {
	mu   sync.RWMutex
	subs map[chan<- Event]struct{}
}

func NewBroker() *Broker {
	return &Broker{subs: map[chan<- Event]struct{}{}}
}

// Subscribe registers a channel for events. Returns a function
// that removes the subscription — caller defers it.
func (b *Broker) Subscribe(ch chan<- Event) func() {
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
	}
}

// Publish fans the event out to every subscriber. Non-blocking;
// a slow consumer drops the event rather than back-pressuring
// the producer (which would block the entire alert evaluator
// tick).
func (b *Broker) Publish(kind string, payload any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[sse] marshal payload: %v", err)
		return
	}
	ev := Event{Kind: kind, Payload: raw}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs {
		select {
		case ch <- ev:
		default:
			// Subscriber buffer full — drop. The eventual
			// React Query refetch (10-30s) covers the gap.
		}
	}
}

// Handler returns an http.Handler that streams events to the
// client over text/event-stream. Sends a comment heartbeat
// every 15s so intermediate proxies (NGINX, Cloudflare) don't
// time out the connection — many default to 60s idle.
//
// Auth flow: the auth.Middleware in front of the mux already
// enforces JWT/cookie. By the time we hit Handler the user is
// authenticated; we don't need to re-check.
func Handler(b *Broker) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache, no-transform")
		w.Header().Set("X-Accel-Buffering", "no")  // disable NGINX buffering
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		ch := make(chan Event, 32)
		unsub := b.Subscribe(ch)
		defer unsub()

		// Initial comment so EventSource considers the
		// connection open and the client's onopen fires
		// immediately, even if no events have been published
		// yet. Without this the operator sees "connecting…"
		// until the first real event lands, which on a quiet
		// Sunday could be hours.
		fmt.Fprint(w, ": ok\n\n")
		flusher.Flush()

		heartbeat := time.NewTicker(15 * time.Second)
		defer heartbeat.Stop()

		ctx := r.Context()
		for {
			select {
			case <-ctx.Done():
				return
			case <-heartbeat.C:
				fmt.Fprint(w, ": ping\n\n")
				flusher.Flush()
			case ev := <-ch:
				if data, err := json.Marshal(ev); err == nil {
					fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Kind, data)
					flusher.Flush()
				}
			}
		}
	})
}
