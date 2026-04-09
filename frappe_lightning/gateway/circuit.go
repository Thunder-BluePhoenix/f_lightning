package gateway

import (
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

// circuitState values stored in the atomic int32.
const (
	stateClosed   int32 = 0
	stateOpen     int32 = 1
	stateHalfOpen int32 = 2
)

// CircuitBreaker protects a single upstream from repeated failures.
// States:
//
//	Closed    — normal traffic flows through.
//	Open      — all requests fail immediately with 503.
//	Half-Open — one probe request is allowed through every probeInterval.
//	            If it succeeds the breaker closes; if it fails the open timer resets.
type CircuitBreaker struct {
	state     atomic.Int32
	failures  atomic.Int64
	threshold int64
	openDur   time.Duration
	probeInterval time.Duration

	openUntil atomic.Int64 // Unix nanoseconds when the breaker may move to half-open
	lastProbe atomic.Int64 // Unix nanoseconds of the last half-open probe attempt

	mu  sync.Mutex
	log *zap.Logger
}

// NewCircuitBreaker creates a CircuitBreaker.
// threshold — consecutive failures before opening.
// openDur   — how long to stay open before probing.
// probeInterval — minimum gap between half-open probes.
func NewCircuitBreaker(threshold int, openDur, probeInterval time.Duration, log *zap.Logger) *CircuitBreaker {
	return &CircuitBreaker{
		threshold:     int64(threshold),
		openDur:       openDur,
		probeInterval: probeInterval,
		log:           log,
	}
}

// Allow returns true if the request should be forwarded to the upstream.
func (cb *CircuitBreaker) Allow() bool {
	switch cb.state.Load() {
	case stateClosed:
		return true

	case stateOpen:
		// Check if the open window has expired and we can try a probe.
		if time.Now().UnixNano() >= cb.openUntil.Load() {
			cb.mu.Lock()
			defer cb.mu.Unlock()
			// Double-check under the lock to avoid a race.
			if cb.state.Load() == stateOpen && time.Now().UnixNano() >= cb.openUntil.Load() {
				cb.state.Store(stateHalfOpen)
				cb.log.Warn("circuit breaker: moving to half-open — sending probe")
			}
		}
		// Re-check state after potential transition.
		if cb.state.Load() != stateHalfOpen {
			return false
		}
		fallthrough

	case stateHalfOpen:
		// Allow one probe per probeInterval.
		now := time.Now().UnixNano()
		last := cb.lastProbe.Load()
		if now-last >= cb.probeInterval.Nanoseconds() {
			if cb.lastProbe.CompareAndSwap(last, now) {
				return true
			}
		}
		return false
	}
	return true
}

// RecordSuccess resets the failure counter and closes the breaker.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.failures.Store(0)
	if old := cb.state.Swap(stateClosed); old != stateClosed {
		cb.log.Info("circuit breaker: closed — upstream recovered")
	}
}

// RecordFailure increments the counter and opens the breaker when the threshold is reached.
func (cb *CircuitBreaker) RecordFailure() {
	count := cb.failures.Add(1)
	if count >= cb.threshold && cb.state.Load() == stateClosed {
		cb.mu.Lock()
		defer cb.mu.Unlock()
		if cb.state.Load() == stateClosed {
			cb.state.Store(stateOpen)
			cb.openUntil.Store(time.Now().Add(cb.openDur).UnixNano())
			cb.log.Error("circuit breaker: opened — upstream is unhealthy",
				zap.Int64("failures", count),
				zap.Duration("open_for", cb.openDur),
			)
		}
	}
	// If we were half-open and the probe failed, reset the open timer.
	if cb.state.Load() == stateHalfOpen {
		cb.mu.Lock()
		defer cb.mu.Unlock()
		if cb.state.Load() == stateHalfOpen {
			cb.state.Store(stateOpen)
			cb.openUntil.Store(time.Now().Add(cb.openDur).UnixNano())
			cb.log.Warn("circuit breaker: probe failed — re-opening")
		}
	}
}

// State returns a human-readable string for the current state.
func (cb *CircuitBreaker) State() string {
	switch cb.state.Load() {
	case stateClosed:
		return "closed"
	case stateOpen:
		return "open"
	case stateHalfOpen:
		return "half-open"
	}
	return "unknown"
}
