package main

import (
	"strings"
	"testing"
)

// sampleLog is a typical git log --graph --oneline output used across tests.
const sampleLog = `* a1b2c3d Merge feature into main
|\  
| * e4f5a6b Add login UI
| * 7c8d9e0 Fix auth token refresh
| * c1d2e3f Add user model
|/  
* f1a2b3c Update dependencies
* 4d5e6f7 Add CI pipeline
* 9a8b7c6 Initial commit`

// TestParseLines verifies lane assignment from git graph column positions.
func TestParseLines(t *testing.T) {
	lines := parseLines(sampleLog)

	commitLines := 0
	for _, l := range lines {
		if !l.commit {
			continue
		}
		commitLines++
		// Lane must be in bounds.
		if l.lane < 0 || l.lane >= numLanes {
			t.Errorf("lane out of bounds: %d for sha %s", l.lane, l.sha)
		}
		// Lane must match column position of '*'.
		col := strings.Index(l.text, "*")
		want := clamp(col/2, 0, numLanes-1)
		if l.lane != want {
			t.Errorf("lane mismatch for %s: got %d, want %d (col=%d)", l.sha, l.lane, want, col)
		}
	}
	if commitLines != 7 {
		t.Errorf("expected 7 commit lines, got %d", commitLines)
	}
}

// TestBuildNotes_HoldDetection checks that runs of ≥ holdMinRun same-lane commits
// become hold notes.
//
// sampleLog contains two such runs:
//   - lane 1 (feature branch): e4f5, 7c8d, c1d2  → 3 consecutive
//   - lane 0 (main tail): f1a2, 4d5e, 9a8b        → 3 consecutive
func TestBuildNotes_HoldDetection(t *testing.T) {
	// Override holdRandIntn so probability checks always pass (return 0 < any threshold).
	orig := holdRandIntn
	holdRandIntn = func(int) int { return 0 }
	defer func() { holdRandIntn = orig }()

	lines := parseLines(sampleLog)
	notes := buildNotes(lines, holdMinRun)

	holdCount := 0
	for _, n := range notes {
		if n.isHold {
			holdCount++
			if n.holdLines < holdMinRun {
				t.Errorf("hold note span too short: %d (want >= %d)", n.holdLines, holdMinRun)
			}
		}
	}
	// Expect 2 hold notes (one per consecutive run described above).
	if holdCount != 2 {
		t.Errorf("expected 2 hold notes, got %d", holdCount)
	}
}

// TestBuildNotes_NoHoldForShortRun verifies that 2 consecutive commits (below
// holdMinRun = 3) do NOT produce a hold note.
func TestBuildNotes_NoHoldForShortRun(t *testing.T) {
	input := "* abc1234 Commit A\n* def5678 Commit B\n"
	lines := parseLines(input)
	notes := buildNotes(lines, holdMinRun)
	for _, n := range notes {
		if n.isHold {
			t.Errorf("unexpected hold note for 2 consecutive commits (holdMinRun=%d)", holdMinRun)
		}
	}
}

// TestBuildNotes_HoldForExactMinRun verifies that exactly holdMinRun consecutive
// commits produce a hold note.
func TestBuildNotes_HoldForExactMinRun(t *testing.T) {
	// Override holdRandIntn so probability checks always pass.
	orig := holdRandIntn
	holdRandIntn = func(int) int { return 0 }
	defer func() { holdRandIntn = orig }()

	parts := make([]string, holdMinRun)
	for i := range parts {
		parts[i] = "* abc1234 Commit"
	}
	lines := parseLines(strings.Join(parts, "\n"))
	notes := buildNotes(lines, holdMinRun)

	found := false
	for _, n := range notes {
		if n.isHold {
			found = true
		}
	}
	if !found {
		t.Errorf("expected hold note for %d consecutive commits, found none", holdMinRun)
	}
}

// TestBuildNotes_ProbabilisticInterruption verifies that a long run is split
// into shorter segments when all probability rolls fail (holdRandIntn returns
// a value ≥ every threshold).
func TestBuildNotes_ProbabilisticInterruption(t *testing.T) {
	// Always fail: return 100 ≥ any probability threshold (max is 90).
	orig := holdRandIntn
	holdRandIntn = func(int) int { return 100 }
	defer func() { holdRandIntn = orig }()

	// Build a run of 6 same-lane commits.
	parts := make([]string, 6)
	for i := range parts {
		parts[i] = "* abc1234 Commit"
	}
	lines := parseLines(strings.Join(parts, "\n"))
	notes := buildNotes(lines, holdMinRun)

	// With all rolls failing at holdMinRun, every group of holdMinRun commits is
	// emitted as individual notes and no hold is formed.
	for _, n := range notes {
		if n.isHold {
			t.Errorf("expected no hold notes when probability always fails, got one")
		}
	}
	// All 6 commits should be individual notes.
	if len(notes) != 6 {
		t.Errorf("expected 6 individual notes, got %d", len(notes))
	}
}

// TestStripRefs verifies that Git ref decorations are stripped from commit messages.
func TestStripRefs(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Normal message", "Normal message"},
		{"(HEAD -> main) Normal message", "Normal message"},
		{"(HEAD -> main, origin/main) Fix", "Fix"},
	}
	for _, c := range cases {
		got := stripRefs(c.in)
		if got != c.want {
			t.Errorf("stripRefs(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestGradeFor verifies the grade boundaries.
func TestGradeFor(t *testing.T) {
	cases := []struct {
		pct   int
		grade string
	}{
		{100, "✦ S"},
		{95, "✦ S"},
		{94, "  A"},
		{85, "  A"},
		{84, "  B"},
		{70, "  B"},
		{69, "  C"},
		{55, "  C"},
		{54, "  D"},
		{40, "  D"},
		{39, "  F"},
		{0, "  F"},
	}
	for _, c := range cases {
		got := gradeFor(c.pct)
		if got != c.grade {
			t.Errorf("gradeFor(%d) = %q, want %q", c.pct, got, c.grade)
		}
	}
}

// TestClamp verifies the clamp helper.
func TestClamp(t *testing.T) {
	if clamp(-1, 0, 4) != 0 {
		t.Error("clamp below lo")
	}
	if clamp(10, 0, 4) != 4 {
		t.Error("clamp above hi")
	}
	if clamp(3, 0, 4) != 3 {
		t.Error("clamp within range")
	}
}

// TestTruncate verifies that long strings are truncated with an ellipsis.
func TestTruncate(t *testing.T) {
	short := "hello"
	if truncate(short, 10) != short {
		t.Error("short string should not be truncated")
	}
	long := strings.Repeat("a", 50)
	got := truncate(long, 10)
	if len([]rune(got)) != 10 {
		t.Errorf("expected 10 runes, got %d", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Error("truncated string should end with ellipsis")
	}
}
