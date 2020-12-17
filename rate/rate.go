// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package rate provides a rate limiter.
package rate

import (
	"context"
	"math"
	"time"
)

// InfDuration is the duration returned by Delay when a Reservation is not OK.
const InfDuration = time.Duration(1<<63 - 1)

// Limit defines the maximum frequency of some events.
// Limit is represented as number of events per second.
// A zero Limit allows no events.
type Limit float64

// Inf is the infinite rate limit; it allows all events (even if burst is zero).
const Inf = Limit(math.MaxFloat64)

// Every converts a minimum time interval between events to a Limit.
func Every(interval time.Duration) Limit {
	if interval <= 0 {
		return Inf
	}
	return 1 / Limit(interval.Seconds())
}

// durationFromTokens is a unit conversion function from the number of tokens to the duration
// of time it takes to accumulate them at a rate of limit tokens per second.
func (limit Limit) durationFromTokens(tokens float64) time.Duration {
	seconds := tokens / float64(limit)
	return time.Nanosecond * time.Duration(1e9*seconds)
}

// tokensFromDuration is a unit conversion function from a time duration to the number of tokens
// which could be accumulated during that duration at a rate of limit tokens per second.
func (limit Limit) tokensFromDuration(d time.Duration) float64 {
	// Split the integer and fractional parts ourself to minimize rounding errors.
	// See golang.org/issues/34861.
	sec := float64(d/time.Second) * float64(limit)
	nsec := float64(d%time.Second) * float64(limit)
	return sec + nsec/1e9
}

// NewLimiter is shorthand for NewTokenBucket(r,l)
func NewLimiter(r Limit, b int) *TokenBucket {
	return NewTokenBucket(r, b)
}

// A Limiter controls how frequently events are allowed to happen.
// Different algorithms will let events happen in different patterns.
// Informally, in any large enough time interval, the Limiter limits the
// rate to r tokens per second.
//
// Use NewLimiter to create a non-zero Limiter using the Token Bucket Algorithm.
//
// Limiter has three main methods, Allow, Reserve, and Wait.
// Most callers should use Wait.
//
// Each of the three methods consumes a single token.
// They differ in their behavior when no token is available.
// If no token is available, Allow returns false.
// If no token is available, Reserve returns a reservation for a future token
// and the amount of time the caller must wait before using it.
// If no token is available, Wait blocks until one can be obtained
// or its associated context.Context is canceled.
//
// The methods AllowN, ReserveN, and WaitN consume n tokens.
type Limiter interface {
	// Allow is shorthand for AllowN(time.Now(), 1)
	Allow() bool

	// AllowN reports whether n events may happen at time now.
	// Use this method if you intend to drop / skip events that exceed the rate limit.
	// Otherwise use Reserve or Wait.
	AllowN(now time.Time, n int) bool

	// Reserve is shorthand for ReserveN(time.Now(), 1)
	Reserve() Reservation

	// ReserveN returns a Reservation that indicates how long the caller must wait before n events happen.
	// The Limiter takes this Reservation into account when allowing future events.
	// The returned Reservation’s OK() method returns false if n exceeds the Limiter's burst size.
	// Usage example:
	//   r := lim.ReserveN(time.Now(), 1)
	//   if !r.OK() {
	//     // Not allowed to act! Did you remember to set lim.burst to be > 0 ?
	//     return
	//   }
	//   time.Sleep(r.Delay())
	//   Act()
	// Use this method if you wish to wait and slow down in accordance with the rate limit without dropping events.
	// If you need to respect a deadline or cancel the delay, use Wait instead.
	// To drop or skip events exceeding rate limit, use Allow instead.
	ReserveN(now time.Time, n int) Reservation

	// Wait is shorthand for WaitN(ctx, 1)
	Wait(ctx context.Context) error

	// WaitN blocks until lim permits n events to happen.
	// It returns an error if n exceeds the Limiter's burst size, the Context is
	// canceled, or the expected wait time exceeds the Context's Deadline.
	// The burst limit is ignored if the rate limit is Inf.
	WaitN(ctx context.Context, n int) error
}

// A Reservation holds information about events that are permitted by a Limiter to happen after a delay.
// A Reservation may be canceled, which may enable the Limiter to permit additional events.
type Reservation interface {
	// Cancel is shorthand for CancelAt(time.Now())
	Cancel()

	// CancelAt indicates that the reservation holder will not perform the reserved action
	// and reverses the effects of this Reservation on the rate limit as much as possible,
	// considering that other reservations may have already been made.
	CancelAt(now time.Time)

	// Delay is shorthand for DelayFrom(time.Now())
	Delay() time.Duration

	// DelayFrom returns the duration for which the reservation holder must wait
	// before taking the reserved action.  Zero duration means act immediately.
	// InfDuration means the limiter cannot grant the tokens requested in this
	// Reservation within the maximum wait time.
	DelayFrom(now time.Time) time.Duration

	// OK returns whether the limiter can provide the requested number of tokens
	// within the maximum wait time.  If OK is false, Delay returns InfDuration, and
	// Cancel does nothing.
	OK() bool
}
