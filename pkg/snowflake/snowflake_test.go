package snowflake

import (
	"testing"
	"time"
)

func TestGenerate(t *testing.T) {
	g := NewGenerator()

	id1 := g.Generate()
	id2 := g.Generate()

	if id1 == 0 {
		t.Error("Generated ID should not be zero")
	}

	if id2 <= id1 {
		t.Errorf("IDs should be monotonically increasing: %d <= %d", id2, id1)
	}
}

func TestGenerateUniqueness(t *testing.T) {
	g := NewGenerator()
	ids := make(map[int64]bool)
	count := 10000

	for i := 0; i < count; i++ {
		id := g.Generate()
		if ids[id] {
			t.Errorf("Duplicate ID generated: %d", id)
		}
		ids[id] = true
	}

	if len(ids) != count {
		t.Errorf("Expected %d unique IDs, got %d", count, len(ids))
	}
}

func TestExtractTime(t *testing.T) {
	g := NewGenerator()
	before := time.Now()
	id := g.Generate()
	after := time.Now()

	extracted := ExtractTime(id)

	if extracted.Before(before.Add(-time.Millisecond)) {
		t.Errorf("Extracted time %v is before generation start %v", extracted, before)
	}

	if extracted.After(after.Add(time.Millisecond)) {
		t.Errorf("Extracted time %v is after generation end %v", extracted, after)
	}
}

func TestGenerateAt(t *testing.T) {
	g := NewGenerator()
	target := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)

	id := g.GenerateAt(target)
	extracted := ExtractTime(id)

	// Should be within 1ms of target
	diff := extracted.Sub(target)
	if diff < -time.Millisecond || diff > time.Millisecond {
		t.Errorf("Extracted time %v differs from target %v by %v", extracted, target, diff)
	}
}

func TestMastodonEpoch(t *testing.T) {
	// Mastodon epoch is 2018-01-01 00:00:00 UTC
	expected := time.Date(2018, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()

	if MastodonEpoch != expected {
		t.Errorf("MastodonEpoch = %d, expected %d", MastodonEpoch, expected)
	}
}

func TestExtractSequence(t *testing.T) {
	g := NewGenerator()

	// Generate multiple IDs in same millisecond
	id1 := g.Generate()
	id2 := g.Generate()

	seq1 := ExtractSequence(id1)
	seq2 := ExtractSequence(id2)

	// Sequences should be different (unless we crossed a millisecond boundary)
	if seq1 == seq2 {
		// This is acceptable if we crossed millisecond boundary
		time1 := ExtractTime(id1)
		time2 := ExtractTime(id2)
		if time1.Equal(time2) {
			t.Errorf("Same timestamp but same sequence: %d", seq1)
		}
	}
}

func TestDefaultGenerator(t *testing.T) {
	id := Generate()

	if id == 0 {
		t.Error("Default generator should produce non-zero ID")
	}
}

func BenchmarkGenerate(b *testing.B) {
	g := NewGenerator()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		g.Generate()
	}
}

func BenchmarkGenerateParallel(b *testing.B) {
	g := NewGenerator()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			g.Generate()
		}
	})
}
