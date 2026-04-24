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

// ─── applyBranchRunes ─────────────────────────────────────────────────────────

// TestApplyBranchRunes exercises the branch-projection rules that were the
// source of the "stray | far to the right" bugs.
//
// Key invariants under test:
//  1. '*' sets '|' at its column but must NOT stop processing; graph chars
//     that follow on the same commit line (e.g. "* | abc1234") must be
//     captured.
//  2. Processing stops at the first non-graph character (start of SHA or
//     message text), so '/' or '\' inside commit messages never produce
//     spurious '|' entries.
func TestApplyBranchRunes(t *testing.T) {
	run := func(input string) string {
		runes := []rune(input)
		out := make([]rune, len(runes)+4)
		for i := range out {
			out[i] = ' '
		}
		applyBranchRunes(runes, out)
		return strings.TrimRight(string(out), " ")
	}

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			"plain pipes",
			"| |",
			"| |",
		},
		{
			// '\' at pos 1 projects '|' one column to the right (pos 2).
			"backslash projects right",
			`|\ `,
			"| |",
		},
		{
			// '/' at pos 1 projects '|' one column to the left (pos 0),
			// which is already occupied — result is still just one pipe.
			"slash onto existing pipe",
			"|/ ",
			"|",
		},
		{
			// '/' at pos 1, nothing at pos 0 — creates a new pipe at pos 0.
			"slash opens new column",
			" / ",
			"|",
		},
		{
			// Critical regression case: '*' must NOT cause an early return.
			// The '|' at column 2 is a live parallel branch and must appear.
			"star with branch after it",
			"* |   abc1234 msg",
			"| |",
		},
		{
			// SHA follows '*' immediately (no extra graph chars) — one pipe only.
			"star only, sha follows immediately",
			"* abc1234 msg",
			"|",
		},
		{
			// '/' inside the commit message must NOT produce a stray '|'.
			// Processing stops at the SHA ('a' is non-graph), so the slash in
			// "user/repo" is never reached.
			"slash in message is ignored",
			"* abc1234 Merge PR from user/repo",
			"|",
		},
		{
			// '\' inside commit message must not project a pipe.
			"backslash in message is ignored",
			`* abc1234 Fix path C:\folder`,
			"|",
		},
		{
			// Two live branches follow '*' — both pipes must be captured.
			"star with two trailing branches",
			"* | |   abc1234 msg",
			"| | |",
		},
	}

	for _, tc := range cases {
		got := run(tc.input)
		if got != tc.want {
			t.Errorf("[%s] applyBranchRunes(%q) = %q, want %q",
				tc.name, tc.input, got, tc.want)
		}
	}
}

// ─── derivePaddingText ────────────────────────────────────────────────────────

// TestDerivePaddingText verifies that the padding line inserted after each
// commit correctly reflects all active branches and contains no spurious '|'
// characters caused by message content.
func TestDerivePaddingText(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "single-lane commit",
			raw:  "* abc1234 Commit",
			want: "|",
		},
		{
			// "* |   sha …": the '|' at col 2 is a live parallel branch.
			// It must appear in the padding even though it follows the '*'.
			name: "commit with live branch after star",
			raw:  "* |   abc1234 Commit\n| |",
			want: "| |",
		},
		{
			// A '/' in a PR title (e.g. "from user/repo") must not generate a
			// stray '|' far to the right of the graph columns.
			name: "no stray pipe from slash in PR title",
			raw:  "* |   abc1234 Merge PR from user/repo\n| |",
			want: "| |",
		},
		{
			// The next line reveals a branch opening ('\' → '|' one col right).
			name: "branch opens on line below commit",
			raw:  "* abc1234 Commit\n|\\ ",
			want: "| |",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines := parseLines(tc.raw)
			ci := -1
			for i, l := range lines {
				if l.commit {
					ci = i
					break
				}
			}
			if ci < 0 {
				t.Fatal("no commit line in input")
			}
			got := derivePaddingText(lines, ci)
			if got != tc.want {
				t.Errorf("derivePaddingText: got %q, want %q", got, tc.want)
			}
		})
	}
}

// ─── tryHit / wrong-lane guard ────────────────────────────────────────────────

// TestTryHit_WrongLaneNeverScores verifies that pressing a lane key when the
// active note is in a *different* lane does not mark the note as hit.
func TestTryHit_WrongLaneNeverScores(t *testing.T) {
	// Build a minimal model with one active note in lane 0.
	m := model{
		hitRow:    10,
		scrollPos: 10, // hitLine = scrollPos - hitRow = 0
		notes: []note{
			{lane: 0, lineIdx: 0, state: nsActive},
		},
	}

	// Press lane 1 (wrong lane).
	m2 := m.tryHit(1)
	if m2.notes[0].state != nsActive {
		t.Errorf("wrong-lane press changed note state to %v, want nsActive", m2.notes[0].state)
	}
	if m2.score != 0 {
		t.Errorf("wrong-lane press awarded %d points, want 0", m2.score)
	}
}

// TestTryHit_CorrectLaneScores verifies that pressing the correct lane key
// when a note is active and within the hit window does score it.
func TestTryHit_CorrectLaneScores(t *testing.T) {
	m := model{
		hitRow:    10,
		scrollPos: 10, // hitLine = 0; note at lineIdx 0 → diff = 0 → PERFECT
		notes: []note{
			{lane: 2, lineIdx: 0, state: nsActive},
		},
	}

	m2 := m.tryHit(2)
	if m2.notes[0].state != nsHit {
		t.Errorf("correct-lane press: note state = %v, want nsHit", m2.notes[0].state)
	}
	if m2.score == 0 {
		t.Error("correct-lane press awarded 0 points")
	}
}

// TestTryHit_WrongLaneBreaksStreak verifies that pressing a lane key when there
// is an active note in the hit window but in a different lane resets the streak
// to zero.
func TestTryHit_WrongLaneBreaksStreak(t *testing.T) {
	m := model{
		hitRow:    10,
		scrollPos: 10, // hitLine = 0; note at lineIdx 0 → diff = 0 → in window
		streak:    5,
		notes: []note{
			{lane: 0, lineIdx: 0, state: nsActive},
		},
	}

	// Press lane 1 (wrong lane) while a note is active in the window.
	m2 := m.tryHit(1)
	if m2.streak != 0 {
		t.Errorf("wrong-lane press did not break streak: got %d, want 0", m2.streak)
	}
	// Note must remain active (not penalised directly).
	if m2.notes[0].state != nsActive {
		t.Errorf("wrong-lane press changed note state to %v, want nsActive", m2.notes[0].state)
	}
}

// TestTryHit_OutsideWindowNotScored verifies that the correct lane key pressed
// when the note is too far from the hit line (> hitWindow) does not score.
func TestTryHit_OutsideWindowNotScored(t *testing.T) {
	m := model{
		hitRow:    10,
		scrollPos: 10, // hitLine = 0; note at lineIdx hitWindow+2 → outside window
		notes: []note{
			{lane: 0, lineIdx: hitWindow + 2, state: nsActive},
		},
	}

	m2 := m.tryHit(0)
	if m2.notes[0].state != nsActive {
		t.Errorf("out-of-window press changed note state to %v, want nsActive", m2.notes[0].state)
	}
	if m2.score != 0 {
		t.Errorf("out-of-window press awarded %d points, want 0", m2.score)
	}
}
