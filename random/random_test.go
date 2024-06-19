package random

import (
	"slices"
	"sort"
	"testing"
)

func TestRandomizeAnnounceList(t *testing.T) {
	announceList := [][]string{
		{"tier 1"},
		{"tier 2 A", "tier 2 B"},
		{"tier 3 A", "tier 3 B", "tier 3 C"},
	}

	result := RandomizeAnnounceList(announceList)

	if len(announceList) != len(result) {
		t.Errorf("got len=%d, want len=%d", len(result), len(announceList))
	}

	for pos := 0; pos < len(announceList); pos += 1 {
		sort.Slice(announceList[pos], func(i, j int) bool { return announceList[pos][i] < announceList[pos][j] })
		sort.Slice(result[pos], func(i, j int) bool { return result[pos][i] < result[pos][j] })

		if !slices.Equal(announceList[pos], result[pos]) {
			t.Errorf("got=%v (len=%d), want=%v (len=%d)", result[pos], len(result[pos]), announceList[pos], len(announceList[pos]))
		}
	}
}
