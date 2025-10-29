package wire

import (
	"math/rand"
	"time"
)

// shortHash safely returns up to the first 8 characters of a string (or the full string if shorter)
func shortHash(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[:8]
}

// randomDelay returns a random duration between min and max
func randomDelay(min, max time.Duration) time.Duration {
	if min >= max {
		return min
	}
	delta := max - min
	return min + time.Duration(rand.Int63n(int64(delta)))
}

// connectionIntervalDelay simulates the connection interval latency in BLE
func connectionIntervalDelay() time.Duration {
	return randomDelay(MinConnectionInterval, MaxConnectionInterval)
}
