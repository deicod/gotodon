// Package snowflake implements Mastodon-compatible Snowflake ID generation.
// Mastodon uses a modified Snowflake format with:
// - Epoch: 2018-01-01 00:00:00 UTC (1514764800000 ms)
// - 48 bits for timestamp (milliseconds since epoch)
// - 16 bits for sequence
package snowflake

import (
	"sync"
	"time"
)

const (
	// MastodonEpoch is 2018-01-01 00:00:00 UTC in milliseconds
	MastodonEpoch int64 = 1514764800000

	// TimestampBits is the number of bits for timestamp
	TimestampBits = 48

	// SequenceBits is the number of bits for sequence
	SequenceBits = 16

	// SequenceMask is the maximum sequence value
	SequenceMask int64 = (1 << SequenceBits) - 1
)

// Generator generates Mastodon-compatible Snowflake IDs.
type Generator struct {
	mu       sync.Mutex
	lastTime int64
	sequence int64
}

// NewGenerator creates a new Snowflake ID generator.
func NewGenerator() *Generator {
	return &Generator{}
}

// Generate creates a new Snowflake ID.
func (g *Generator) Generate() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()

	now := time.Now().UnixMilli()

	if now == g.lastTime {
		g.sequence = (g.sequence + 1) & SequenceMask
		if g.sequence == 0 {
			// Sequence overflow, wait for next millisecond
			for now <= g.lastTime {
				now = time.Now().UnixMilli()
			}
		}
	} else {
		g.sequence = 0
	}

	g.lastTime = now

	// Mastodon format: (timestamp - epoch) << 16 | sequence
	id := ((now - MastodonEpoch) << SequenceBits) | g.sequence
	return id
}

// GenerateAt creates a Snowflake ID for a specific timestamp.
// Useful for testing or importing historical data.
func (g *Generator) GenerateAt(t time.Time) int64 {
	g.mu.Lock()
	defer g.mu.Unlock()

	ms := t.UnixMilli()
	g.sequence = (g.sequence + 1) & SequenceMask

	return ((ms - MastodonEpoch) << SequenceBits) | g.sequence
}

// ExtractTime extracts the timestamp from a Snowflake ID.
func ExtractTime(id int64) time.Time {
	ms := (id >> SequenceBits) + MastodonEpoch
	return time.UnixMilli(ms)
}

// ExtractSequence extracts the sequence number from a Snowflake ID.
func ExtractSequence(id int64) int64 {
	return id & SequenceMask
}

// Default is the default global generator.
var Default = NewGenerator()

// Generate creates a new Snowflake ID using the default generator.
func Generate() int64 {
	return Default.Generate()
}
