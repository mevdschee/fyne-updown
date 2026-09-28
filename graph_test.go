package main

import (
	"testing"
	"time"
)

func TestHistoryWraps(t *testing.T) {
	var h history
	start := time.Unix(0, 0)
	for i := 0; i < historySize+10; i++ {
		h.add(start.Add(time.Duration(i)*time.Second), float64(i), float64(-i))
	}
	if h.len() != historySize {
		t.Fatalf("len = %d, want %d", h.len(), historySize)
	}
	for i := 0; i < historySize; i++ {
		want := float64(historySize + 9 - i)
		if s := h.at(i); s.down != want || s.up != -want {
			t.Fatalf("at(%d) = %v, want %v", i, s.down, want)
		}
	}
	// beyond the oldest sample it counts back one second per sample
	if got, want := h.timeAt(historySize), start.Add(9*time.Second); !got.Equal(want) {
		t.Fatalf("timeAt(historySize) = %v, want %v", got, want)
	}
}

func TestHistoryDoesNotAllocateWhenFull(t *testing.T) {
	var h history
	now := time.Now()
	for i := 0; i < historySize; i++ {
		h.add(now, 1, 1)
	}
	if n := testing.AllocsPerRun(10000, func() { h.add(now, 1, 1) }); n != 0 {
		t.Fatalf("add allocates %v times when full", n)
	}
}
