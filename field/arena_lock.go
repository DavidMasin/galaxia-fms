// The arena's lock: who may touch the arena's state, and when.
//
// The arena's state (the match and its clock, the stations, the display modes, the settings and the game in use) is
// read and changed on several goroutines: the arena loop, the web handlers, the driver station listeners, timers.
// One lock makes them take turns. The rule is:
//
//   - Everything outside the arena package takes the lock around whatever it reads or changes: Lock and Unlock, or
//     Do. The arena's own methods assume their caller holds it and never take it themselves, so they can call one
//     another freely.
//   - Update is the exception: it is the arena loop's turn, takes the lock itself, and so must be called without it.
//   - Goroutines the arena starts take the lock themselves around what they touch.
//   - Nothing waits on the network, or on a client, while holding the lock: every pass of the loop needs it, and the
//     loop is what keeps robots enabled and stops them. Code that must wait lets go with Unlocked.
//
// Parts with a lock of their own do not need this one: the live score (scoreMutex), the display registry, the
// scoring panel registry, the PLC, the access point, the device manager, the lighting engine, the event bus.
// Their locks are taken after this one, never before.

package field

import (
	"log"
	"time"
)

const (
	// Holding the arena's lock for longer than this is worth a line in the log: the loop was kept from several
	// passes in a row.
	lockHoldWarning = 50 * time.Millisecond
	// At most one such line is logged in this time, so that something slow on every pass does not flood the log.
	lockHoldWarningPeriod = 5 * time.Second
)

// Lock gives the caller the arena's state until Unlock.
func (arena *Arena) Lock() {
	arena.mutex.Lock()
	arena.lockHeld.Store(true)
	arena.lockedAt = time.Now()
	arena.lockHolder = ""
}

// LockAs is Lock for a caller that can say what it is, which is named in the log if it holds the lock for long.
func (arena *Arena) LockAs(holder string) {
	arena.Lock()
	arena.lockHolder = holder
}

// Unlock gives the arena's state up.
func (arena *Arena) Unlock() {
	// What other goroutines read without the lock is brought up to date first, so that it is current whenever the
	// lock is free.
	arena.refreshBusContext()
	if arena.sharedDatabase.Load() != arena.Database {
		arena.sharedDatabase.Store(arena.Database)
	}
	held, holder := time.Since(arena.lockedAt), arena.lockHolder
	warn := held > lockHoldWarning && time.Since(arena.lockWarnedAt) > lockHoldWarningPeriod
	if warn {
		arena.lockWarnedAt = time.Now()
	}
	arena.lockHeld.Store(false)
	arena.mutex.Unlock()
	if warn {
		if holder == "" {
			holder = "something that did not say what it was"
		}
		log.Printf("Warning: the arena was held for %d ms by %s; the arena loop waited", held.Milliseconds(), holder)
	}
}

// Do runs fn with the arena's lock held.
func (arena *Arena) Do(fn func()) {
	arena.Lock()
	defer arena.Unlock()
	fn()
}

// Unlocked runs fn, which waits on something slow (the network, a long computation), without holding the arena's
// lock: if the lock is held, as it is whenever the arena is used from outside, it is let go while fn runs and taken
// again afterwards. fn must not touch the arena, and the arena may have changed by the time it returns: the caller
// must check whatever it relied on.
func (arena *Arena) Unlocked(fn func()) {
	if arena.lockHeld.Load() {
		holder := arena.lockHolder
		arena.Unlock()
		defer arena.LockAs(holder)
	}
	fn()
}

// later runs fn after a delay, on a goroutine of its own and with the lock held.
func (arena *Arena) later(delaySec int, fn func()) {
	go func() {
		time.Sleep(time.Second * time.Duration(delaySec))
		arena.Do(fn)
	}()
}

// refreshBusContext notes which match events published now belong to. The bus asks from whatever goroutine
// publishes, including ones that do not hold the arena's lock, so it is given a copy rather than the arena's state.
func (arena *Arena) refreshBusContext() {
	match := ""
	if arena.CurrentMatch != nil && arena.MatchState != PreMatch {
		match = arena.CurrentMatch.ShortName
	}
	if previous, _ := arena.busMatch.Load().(string); previous != match {
		arena.busMatch.Store(match)
	}
}

// busContext supplies the match that events published on the bus belong to.
func (arena *Arena) busContext() (match, run string) {
	match, _ = arena.busMatch.Load().(string)
	return match, ""
}
