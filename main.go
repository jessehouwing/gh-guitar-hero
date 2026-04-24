package main

// Git Guitar Hero — a gh CLI extension that turns your repo's commit graph
// into a Guitar Hero–style rhythm game.
//
// Usage:  gh guitar-hero
//
// Controls: A S D F G  — hit the note in that lane
//           Hold key   — for long runs on a single branch
//           Q / Ctrl+C — quit at any time

import (
	"bufio"
	"fmt"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ─── constants ────────────────────────────────────────────────────────────────

const (
	numLanes   = 5
	tickRate   = 50 * time.Millisecond  // 20 FPS
	hitWindow  = 5                      // ±lines tolerance around the hit zone
	holdGap    = 200 * time.Millisecond // key "held" if last press < this ago
	holdMinRun = 3                      // default: consecutive same-lane commits → hold note
	sparkLife  = 22                     // frames a firework particle lives
	maxCommits = 300                    // cap on git history depth
	minH, minW = 14, 50                // minimum terminal size
)

// ─── difficulty ───────────────────────────────────────────────────────────────

type difficultyProfile struct {
	label      string
	holdMinRun int // consecutive same-lane commits needed to form a hold note
}

var difficulties = []difficultyProfile{
	{"Easy",   2}, // pairs of same-lane commits become holds
	{"Normal", 3}, // triplets become holds
	{"Hard",   4}, // need 4 same-lane commits for a hold
	{"Expert", 5}, // need 5 same-lane commits for a hold
}

const defaultDiffIdx = 1 // Normal

// ─── speed ────────────────────────────────────────────────────────────────────

type speedProfile struct {
	label       string
	scrollEvery int // ticks between line advances
}

var speeds = []speedProfile{
	{"Slowest", 10}, // very relaxed scroll
	{"Slow",    30}, // gentle scroll
	{"Normal",   3}, // moderate scroll
	{"Fast",     2}, // quick scroll
	{"Fastest",  1}, // very fast scroll
}

const defaultSpeedIdx = 2 // Normal

// ─── lane colours & labels ────────────────────────────────────────────────────

var laneHex = [numLanes]string{
	"#FF6B6B", // red
	"#6BCB77", // green
	"#FFD93D", // yellow
	"#4D96FF", // blue
	"#C77DFF", // purple
}

var laneLabels = [numLanes]string{"A", "S", "D", "F", "G"}
var laneRunes = [numLanes]rune{'a', 's', 'd', 'f', 'g'}

// Pre-built cached styles (avoid allocating per frame)
var (
	styleLane  [numLanes]lipgloss.Style
	styleLaneB [numLanes]lipgloss.Style
	styleDim   = lipgloss.NewStyle().Faint(true)
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(lipgloss.Color("#7D56F4")).
			Padding(0, 2)
)

func init() {
	for i := range laneHex {
		styleLane[i] = lipgloss.NewStyle().Foreground(lipgloss.Color(laneHex[i]))
		styleLaneB[i] = lipgloss.NewStyle().Foreground(lipgloss.Color(laneHex[i])).Bold(true)
	}
}

// Rune pool for firework particles
var sparkChars = []rune{'★', '✦', '·', '˚', '✧', '*', '+', '°', '♦', '◆', '✸', '✺'}

// ─── data types ───────────────────────────────────────────────────────────────

// gLine is one line from `git log --graph`.
type gLine struct {
	text   string
	commit bool
	sha    string
	msg    string
	lane   int // 0..numLanes-1, -1 if not a commit line
}

type noteState int8

const (
	nsUpcoming noteState = iota
	nsActive             // within the hit window
	nsHit
	nsMissed
)

// note represents one scoreable event (a commit, or start of a hold run).
type note struct {
	lane      int
	lineIdx   int       // index in gLines where the commit marker lives
	sha       string
	msg       string
	state     noteState
	isHold    bool
	holdLines int  // how many gLines the hold spans (including branch decoration)
	activeAt  int  // tick when note entered the hit window
}

// spark is a single firework particle.
type spark struct {
	x, y, vx, vy float64
	ch            rune
	color         string
	life          int
}

type phase int

const (
	phMenu phase = iota
	phPlay
	phOver
)

type tickMsg time.Time

// ─── model ────────────────────────────────────────────────────────────────────

type model struct {
	ph     phase
	errMsg string

	lines    []gLine
	notes    []note
	notePtr  int // index of first unresolved (not yet hit/missed) note

	w, h      int
	hitRow    int // row index (from top) of the hit-zone separator
	scrollPos int // index of gLines shown at row 0

	diffIdx     int // index into difficulties slice
	speedIdx    int // index into speeds slice
	scrollEvery int // ticks between line advances (set from speed on start)

	tick  int
	sTick int // sub-tick counter for scrolling

	held      [numLanes]bool
	lastPress [numLanes]time.Time

	sparks []spark

	score  int
	streak int
	maxStr int
	misses int
	total  int

	fbMsg string
	fbEnd int // tick when feedback message expires
}

// ─── git parsing ──────────────────────────────────────────────────────────────

func newModel() model {
	m := model{ph: phMenu, w: 80, h: 24, diffIdx: defaultDiffIdx, speedIdx: defaultSpeedIdx}

	out, err := exec.Command("git", "log",
		"--graph", "--oneline", "--all", "--no-color",
		fmt.Sprintf("--max-count=%d", maxCommits),
	).Output()
	if err != nil {
		m.errMsg = "Not a git repository (or git is not installed)."
		return m
	}
	if len(strings.TrimSpace(string(out))) == 0 {
		m.errMsg = "No commits found in this repository."
		return m
	}

	m.lines = addPaddingLines(parseLines(string(out)))
	m.notes = buildNotes(m.lines, difficulties[m.diffIdx].holdMinRun)
	m.total = len(m.notes)
	return m
}

func parseLines(raw string) []gLine {
	var gl []gLine
	sc := bufio.NewScanner(strings.NewReader(raw))
	for sc.Scan() {
		t := sc.Text()
		l := gLine{text: t, lane: -1}
		if si := strings.Index(t, "*"); si >= 0 {
			l.commit = true
			l.lane = clamp(si/2, 0, numLanes-1)
			rest := strings.TrimSpace(t[si+1:])
			if len(rest) >= 7 {
				l.sha = rest[:7]
				if len(rest) > 8 {
					l.msg = truncate(stripRefs(rest[8:]), 48)
				}
			}
		}
		gl = append(gl, l)
	}
	return gl
}

// addPaddingLines inserts one branch-aware padding line after each commit line,
// adding visual breathing room between commits so they don't rush past the
// hit-zone. The padding line reflects all active branches at that point in the
// graph, not just the main-branch "|".
func addPaddingLines(lines []gLine) []gLine {
	result := make([]gLine, 0, len(lines)*2)
	for i, l := range lines {
		result = append(result, l)
		if l.commit {
			padText := derivePaddingText(lines, i)
			result = append(result, gLine{text: padText, commit: false, lane: -1})
		}
	}
	return result
}

// derivePaddingText returns a "branches only" text line for the padding row
// inserted after the commit at commitIdx. It combines information from the
// commit line itself (branches to the left of "*") and the immediately
// following line (which may reveal additional branches via "\" or "/").
func derivePaddingText(lines []gLine, commitIdx int) string {
	a := []rune(lines[commitIdx].text)
	// +2 so a trailing '\' can write one column past the end of the source text.
	n := len(a) + 2
	if commitIdx+1 < len(lines) {
		if m := len([]rune(lines[commitIdx+1].text)) + 2; m > n {
			n = m
		}
	}

	out := make([]rune, n)
	for j := range out {
		out[j] = ' '
	}

	applyBranchRunes(a, out)
	if commitIdx+1 < len(lines) {
		applyBranchRunes([]rune(lines[commitIdx+1].text), out)
	}

	result := strings.TrimRight(string(out), " ")
	if result == "" {
		return "|"
	}
	return result
}

// applyBranchRunes marks active branch positions in out from the given rune slice.
// Rules: '|' and '*' → '|' at that column; '\' → '|' one column to the right;
// '/' → '|' one column to the left. All other characters are ignored.
func applyBranchRunes(runes []rune, out []rune) {
	for i, r := range runes {
		if i >= len(out) {
			break // out is shorter than runes; no further columns can be written
		}
		switch r {
		case '|', '*':
			out[i] = '|'
		case '\\':
			if i+1 < len(out) {
				out[i+1] = '|'
			}
		case '/':
			if i > 0 {
				out[i-1] = '|'
			}
		}
	}
}

// buildNotes scans commit lines and groups consecutive same-lane commits into
// hold notes when the run is long enough.
func buildNotes(lines []gLine, holdMinRun int) []note {
	// Collect indices of commit lines in order.
	var ci []int
	for i, l := range lines {
		if l.commit {
			ci = append(ci, i)
		}
	}

	var notes []note
	i := 0
	for i < len(ci) {
		lane := lines[ci[i]].lane

		// Count consecutive commits on the same lane.
		j := i + 1
		for j < len(ci) && lines[ci[j]].lane == lane {
			j++
		}
		run := j - i

		holdLines := 0
		if run >= holdMinRun {
			// Span from first to last commit in the run (inclusive of decorating lines).
			holdLines = ci[j-1] - ci[i] + 1
		}

		n := note{
			lane:      lane,
			lineIdx:   ci[i],
			sha:       lines[ci[i]].sha,
			msg:       lines[ci[i]].msg,
			state:     nsUpcoming,
			isHold:    run >= holdMinRun,
			holdLines: holdLines,
		}
		notes = append(notes, n)

		if run >= holdMinRun {
			i = j // skip intermediate commits; they're part of the hold
		} else {
			i++
		}
	}
	return notes
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func iabs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "…"
}

// stripRefs removes Git ref decorations like "(HEAD -> main, origin/main)".
func stripRefs(s string) string {
	for {
		pi := strings.Index(s, "(")
		if pi < 0 {
			break
		}
		qi := strings.Index(s[pi:], ")")
		if qi < 0 {
			break
		}
		s = strings.TrimSpace(s[:pi] + s[pi+qi+1:])
	}
	return s
}

func gradeFor(pct int) string {
	switch {
	case pct >= 95:
		return "✦ S"
	case pct >= 85:
		return "  A"
	case pct >= 70:
		return "  B"
	case pct >= 55:
		return "  C"
	case pct >= 40:
		return "  D"
	default:
		return "  F"
	}
}

// ─── bubbletea wiring ─────────────────────────────────────────────────────────

func (m model) Init() tea.Cmd {
	return tickCmd()
}

func tickCmd() tea.Cmd {
	return tea.Tick(tickRate, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.hitRow = m.h - 5
		if m.hitRow < 3 {
			m.hitRow = 3
		}
	case tea.KeyMsg:
		return m.handleKey(msg)
	case tickMsg:
		return m.handleTick()
	}
	return m, tickCmd()
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := strings.ToLower(msg.String())

	switch m.ph {
	case phMenu:
		switch key {
		case "left", "h":
			if m.diffIdx > 0 {
				m.diffIdx--
				m.notes = buildNotes(m.lines, difficulties[m.diffIdx].holdMinRun)
				m.total = len(m.notes)
			}
		case "right", "l":
			if m.diffIdx < len(difficulties)-1 {
				m.diffIdx++
				m.notes = buildNotes(m.lines, difficulties[m.diffIdx].holdMinRun)
				m.total = len(m.notes)
			}
		case "up", "k":
			if m.speedIdx > 0 {
				m.speedIdx--
			}
		case "down", "j":
			if m.speedIdx < len(speeds)-1 {
				m.speedIdx++
			}
		case "enter", " ":
			if m.errMsg == "" {
				m.ph = phPlay
				m.hitRow = m.h - 5
				if m.hitRow < 3 {
					m.hitRow = 3
				}
				// Notes fall from the top; scrollPos=0 puts the latest commit at row 0.
				m.scrollPos = 0
				m.scrollEvery = speeds[m.speedIdx].scrollEvery
				m.tick = 0
				m.sTick = 0
				// Rebuild notes with the holdMinRun for the chosen difficulty.
				m.notes = buildNotes(m.lines, difficulties[m.diffIdx].holdMinRun)
				m.total = len(m.notes)
				m.notePtr = 0
				m.score = 0
				m.streak = 0
				m.maxStr = 0
				m.misses = 0
			}
		case "q", "ctrl+c":
			return m, tea.Quit
		}

	case phPlay:
		switch key {
		case "q", "ctrl+c":
			return m, tea.Quit
		default:
			for i, r := range laneRunes {
				if key == string(r) {
					m.lastPress[i] = time.Now()
					m.held[i] = true
					m = m.tryHit(i) // always attempt; tryHit only scores nsActive notes
				}
			}
		}

	case phOver:
		switch key {
		case "enter", " ", "r":
			m2 := newModel()
			m2.w, m2.h = m.w, m.h
			m2.hitRow = m.hitRow
			m2.diffIdx = m.diffIdx
			m2.speedIdx = m.speedIdx
			// Rebuild notes for the previously chosen difficulty.
			m2.notes = buildNotes(m2.lines, difficulties[m2.diffIdx].holdMinRun)
			m2.total = len(m2.notes)
			return m2, tickCmd()
		case "q", "ctrl+c":
			return m, tea.Quit
		}
	}

	return m, nil
}

// tryHit checks whether the pressed lane corresponds to an active note.
func (m model) tryHit(lane int) model {
	hitLine := m.scrollPos - m.hitRow
	for i := m.notePtr; i < len(m.notes); i++ {
		n := &m.notes[i]
		if n.state != nsActive {
			continue
		}
		if n.lane != lane {
			continue
		}
		diff := iabs(n.lineIdx - hitLine)
		pts, fb := 0, ""
		switch {
		case diff <= 1:
			pts, fb = 300, "PERFECT!"
			m.spawnFireworks(lane)
		case diff <= 3:
			pts, fb = 200, "GREAT!  "
		case diff <= hitWindow:
			pts, fb = 100, "GOOD!   "
		}
		if pts > 0 {
			n.state = nsHit
			multiplier := 1 + m.streak/5
			m.score += pts * multiplier
			m.streak++
			if m.streak > m.maxStr {
				m.maxStr = m.streak
			}
			m.fbMsg = fb
			m.fbEnd = m.tick + 28
		}
		break
	}
	return m
}

func (m *model) spawnFireworks(lane int) {
	laneW := m.w / numLanes
	cx := float64(lane*laneW + laneW/2)
	cy := float64(m.hitRow)

	// Scale particle count and speed with current streak.
	// Base: 24 particles. Add 12 more for every 5 streak, up to 72 total.
	count := 24 + clamp(m.streak/5, 0, 4)*12
	spdScale := 1.0 + float64(m.streak)/25.0
	if spdScale > 2.5 {
		spdScale = 2.5
	}

	for k := 0; k < count; k++ {
		angle := rand.Float64() * 2 * math.Pi
		spd := (0.5 + rand.Float64()*2.5) * spdScale
		m.sparks = append(m.sparks, spark{
			x: cx, y: cy,
			vx:    math.Cos(angle) * spd,
			vy:    math.Sin(angle) * spd * 0.45,
			ch:    sparkChars[rand.Intn(len(sparkChars))],
			color: laneHex[lane],
			life:  sparkLife,
		})
	}
}

func (m model) handleTick() (tea.Model, tea.Cmd) {
	if m.ph != phPlay {
		return m, tickCmd()
	}

	m.tick++

	// Update held state: a key is "held" if it received an event recently
	// (terminal auto-repeat fires at ~30 ms intervals when a key is depressed).
	for i := range m.held {
		if m.held[i] && time.Since(m.lastPress[i]) > holdGap {
			m.held[i] = false
		}
	}

	// Advance scroll
	m.sTick++
	if m.sTick >= m.scrollEvery {
		m.sTick = 0
		m.scrollPos++
	}

	hitLine := m.scrollPos - m.hitRow

	// Update note states
	for i := range m.notes {
		n := &m.notes[i]
		switch n.state {
		case nsUpcoming:
			if iabs(n.lineIdx-hitLine) <= hitWindow {
				n.state = nsActive
				n.activeAt = m.tick
			}
		case nsActive:
			// Missed if the window has scrolled past without a hit
			if n.lineIdx < hitLine-hitWindow {
				n.state = nsMissed
				m.misses++
				m.streak = 0
				m.fbMsg = "MISS    "
				m.fbEnd = m.tick + 20
			}
			// Award incremental hold-note score while the key is held
			if n.isHold && m.held[n.lane] && m.tick%4 == 0 {
				m.score += 15
			}
		case nsHit:
			// Continue awarding hold score while key is held and hold window is live
			if n.isHold && m.held[n.lane] && m.tick%4 == 0 {
				holdEnd := n.lineIdx + n.holdLines
				if hitLine <= holdEnd+hitWindow {
					m.score += 15
				}
			}
		}
	}

	// Advance notePtr past fully resolved notes
	for m.notePtr < len(m.notes) {
		if s := m.notes[m.notePtr].state; s == nsHit || s == nsMissed {
			m.notePtr++
		} else {
			break
		}
	}

	// Check end condition: wait until all notes are resolved AND the commit
	// history has completely scrolled off screen.
	allDone := true
	for _, n := range m.notes {
		if n.state == nsUpcoming || n.state == nsActive {
			allDone = false
			break
		}
	}
	if allDone && m.scrollPos >= len(m.lines)+m.hitRow {
		m.ph = phOver
	}

	// Advance spark particles
	alive := m.sparks[:0]
	for i := range m.sparks {
		s := &m.sparks[i]
		s.x += s.vx
		s.y += s.vy
		s.vy += 0.12 // gravity
		s.life--
		if s.life > 0 && s.x >= 0 && s.x < float64(m.w) && s.y >= 0 && s.y < float64(m.h) {
			alive = append(alive, *s)
		}
	}
	m.sparks = alive

	return m, tickCmd()
}

// ─── view ─────────────────────────────────────────────────────────────────────

func (m model) View() string {
	switch m.ph {
	case phMenu:
		return m.viewMenu()
	case phPlay:
		return m.viewGame()
	case phOver:
		return m.viewOver()
	}
	return ""
}

func (m model) viewMenu() string {
	if m.errMsg != "" {
		return "\n  ❌  " + m.errMsg + "\n\n  Press q to quit.\n"
	}

	laneList := ""
	for i := 0; i < numLanes; i++ {
		laneList += fmt.Sprintf("    Lane %d (%s) → press  %s\n",
			i+1,
			styleLaneB[i].Render(strings.ToLower(laneHex[i][:7])),
			styleLaneB[i].Render(laneLabels[i]),
		)
	}

	// Build difficulty selector display
	diffSelector := "  "
	for i, d := range difficulties {
		if i == m.diffIdx {
			diffSelector += styleLaneB[2].Render("[ " + d.label + " ]")
		} else {
			diffSelector += styleDim.Render("  " + d.label + "  ")
		}
	}

	// Build speed selector display
	speedSelector := "  "
	for i, s := range speeds {
		if i == m.speedIdx {
			speedSelector += styleLaneB[3].Render("[ " + s.label + " ]")
		} else {
			speedSelector += styleDim.Render("  " + s.label + "  ")
		}
	}

	return fmt.Sprintf(`
%s

  Loaded %d commits  (%d playable notes)

  HOW TO PLAY  ─────────────────────────────────────────────────
  The git commit graph scrolls downward.
  When a commit (●) reaches the ══ hit-zone line, press the
  matching key for its column:

%s
  For long runs on a single branch, HOLD the key!
  Hit at exactly the right moment for fireworks 🎆

  PERFECT = ×3 pts   GREAT = ×2 pts   GOOD = ×1 pt
  Your streak multiplies your score!
  ─────────────────────────────────────────────────────────────

  DIFFICULTY:  ← %s →    (← → to change)
  SPEED:       ↑ %s ↓    (↑ ↓ to change)

  ENTER / SPACE = Start    Q = Quit

`,
		titleStyle.Render("  🎸  Git Guitar Hero  "),
		m.total, m.total,
		laneList,
		diffSelector,
		speedSelector,
	)
}

func (m model) viewOver() string {
	hits := m.total - m.misses
	pct := 0
	if m.total > 0 {
		pct = hits * 100 / m.total
	}
	grade := gradeFor(pct)
	return fmt.Sprintf(`
%s

  Score      %d
  Grade      %s  (%d%% accuracy)
  Max Combo  %dx
  Hits       %d / %d
  Misses     %d

  ENTER / R = Play Again    Q = Quit

`,
		titleStyle.Render("  🎸  Git Guitar Hero – Game Over  "),
		m.score, grade, pct,
		m.maxStr,
		hits, m.total,
		m.misses,
	)
}

// noteMarker holds the visual override for a git graph line.
type noteMarker struct {
	r     rune
	color string
}

// color2lane maps a hex colour string back to its lane index.
func color2lane(color string) int {
	for i, h := range laneHex {
		if h == color {
			return i
		}
	}
	return 0
}

// viewGame renders the full game screen.
func (m model) viewGame() string {
	if m.w < minW || m.h < minH {
		return fmt.Sprintf("Terminal too small (%dx%d). Minimum %dx%d.\n",
			m.w, m.h, minW, minH)
	}

	// Build spark lookup: [col, row] → spark
	sparkAt := make(map[[2]int]spark, len(m.sparks))
	for _, s := range m.sparks {
		x, y := int(math.Round(s.x)), int(math.Round(s.y))
		if x >= 0 && x < m.w && y >= 0 && y < m.h {
			sparkAt[[2]int{x, y}] = s
		}
	}

	// Build note-marker lookup: gLine index → noteMarker
	markers := make(map[int]noteMarker)
	for _, n := range m.notes {
		row := m.scrollPos - n.lineIdx
		if row < 0 || row >= m.h {
			continue
		}
		switch n.state {
		case nsActive:
			markers[n.lineIdx] = noteMarker{'●', laneHex[n.lane]}
		case nsHit:
			markers[n.lineIdx] = noteMarker{'✓', laneHex[n.lane]}
		case nsMissed:
			markers[n.lineIdx] = noteMarker{'✗', "#555555"}
		}
		// For hold notes, also mark intermediate lines with a bar
		if (n.state == nsActive || n.state == nsHit) && n.isHold {
			for k := 1; k < n.holdLines; k++ {
				li := n.lineIdx + k
				innerRow := m.scrollPos - li
				if innerRow >= 0 && innerRow < m.h {
					if _, exists := markers[li]; !exists {
						markers[li] = noteMarker{'┃', laneHex[n.lane]}
					}
				}
			}
		}
	}

	gameRows := m.hitRow + 1 // rows for git graph + hit zone line
	var sb strings.Builder

	for row := 0; row < gameRows; row++ {
		if row == m.hitRow {
			sb.WriteString(renderHitZone(m.w))
			sb.WriteByte('\n')
			continue
		}

		lineIdx := m.scrollPos - row

		// Rows outside git history: only sparks
		if lineIdx < 0 || lineIdx >= len(m.lines) {
			sb.WriteString(renderSparkRow(row, m.w, sparkAt))
			sb.WriteByte('\n')
			continue
		}

		gl := m.lines[lineIdx]
		mk, hasMk := markers[lineIdx]
		sb.WriteString(renderGraphLine(gl, mk, hasMk, sparkAt, row, m.w))
		sb.WriteByte('\n')
	}

	// Key display row
	sb.WriteString(renderKeyRow(m.w, m.held))
	sb.WriteByte('\n')

	// Status / feedback row
	fb := ""
	if m.tick < m.fbEnd && m.fbMsg != "" {
		fb = "  " + feedbackStyle(m.fbMsg).Render(m.fbMsg)
	}
	status := fmt.Sprintf(" Score:%-7d Streak:%-3dx Best:%-3dx Misses:%-3d%s",
		m.score, m.streak, m.maxStr, m.misses, fb)
	sb.WriteString(styleDim.Render(status))

	return sb.String()
}

// renderHitZone draws the coloured hit-zone separator line.
func renderHitZone(w int) string {
	segW := w / numLanes
	var sb strings.Builder
	for i := 0; i < numLanes; i++ {
		start := i * segW
		end := start + segW
		if i == numLanes-1 {
			end = w
		}
		seg := strings.Repeat("═", end-start)
		sb.WriteString(styleLaneB[i].Render(seg))
	}
	return sb.String()
}

// renderKeyRow draws the lane key indicators at the bottom of the game area.
func renderKeyRow(w int, held [numLanes]bool) string {
	segW := w / numLanes
	var sb strings.Builder
	for i := 0; i < numLanes; i++ {
		// Centre the label within its segment
		lbl := ""
		if held[i] {
			lbl = fmt.Sprintf(" (%-1s) ", laneLabels[i])
		} else {
			lbl = fmt.Sprintf(" [%-1s] ", laneLabels[i])
		}
		padTotal := segW - len(lbl)
		left := padTotal / 2
		right := padTotal - left
		if left < 0 {
			left = 0
		}
		if right < 0 {
			right = 0
		}
		cell := strings.Repeat(" ", left) + lbl + strings.Repeat(" ", right)
		if len(cell) > segW {
			cell = cell[:segW]
		}
		if held[i] {
			sb.WriteString(styleLaneB[i].Render(cell))
		} else {
			sb.WriteString(styleDim.Render(cell))
		}
	}
	return sb.String()
}

// renderSparkRow renders a row that has no git content — only spark particles.
func renderSparkRow(row, w int, sparkAt map[[2]int]spark) string {
	var sb strings.Builder
	for col := 0; col < w; col++ {
		if s, ok := sparkAt[[2]int{col, row}]; ok {
			lane := clamp(int(s.x)*numLanes/w, 0, numLanes-1)
			sb.WriteString(styleLane[lane].Render(string(s.ch)))
		} else {
			sb.WriteByte(' ')
		}
	}
	return sb.String()
}

// renderGraphLine renders a single git-log --graph line with ANSI colours
// and overlays note markers and spark particles.
// Git graph characters use 1 column each. The commit star is followed by a
// space and then the 7-char SHA + message.
func renderGraphLine(
	gl gLine,
	mk noteMarker,
	hasMk bool,
	sparkAt map[[2]int]spark,
	row, width int,
) string {
	var sb strings.Builder

	runes := []rune(gl.text)

	// Find the byte offset of '*' in gl.text and convert to rune index.
	starRuneIdx := -1
	if gl.commit {
		byteOff := strings.Index(gl.text, "*")
		if byteOff >= 0 {
			// Count runes up to byteOff
			starRuneIdx = len([]rune(gl.text[:byteOff]))
		}
	}

	col := 0
	for ci, ch := range runes {
		if col >= width {
			break
		}

		// Spark overlay takes priority
		if s, ok := sparkAt[[2]int{col, row}]; ok {
			lane := clamp(int(s.x)*numLanes/width, 0, numLanes-1)
			sb.WriteString(styleLane[lane].Render(string(s.ch)))
			col++
			continue
		}

		lane := clamp(col/2, 0, numLanes-1)

		// Commit star position: replace with marker or coloured star
		if gl.commit && ci == starRuneIdx {
			if hasMk {
				sb.WriteString(styleLaneB[color2lane(mk.color)].Render(string(mk.r)))
			} else {
				sb.WriteString(styleLaneB[gl.lane].Render("*"))
			}
			col++
			continue
		}

		// Everything after the star (space + SHA + message) is rendered dim
		if gl.commit && starRuneIdx >= 0 && ci > starRuneIdx {
			sb.WriteString(styleDim.Render(string(ch)))
			col++
			continue
		}

		// Graph decoration characters.
		// '\' and '/' are swapped on render because the game scrolls commits
		// newest-first toward the hit-zone (bottom), which is the reverse of the
		// git-log order. Swapping restores the conventional top-to-bottom
		// branch appearance (branches open with '\' going right and close with '/').
		switch ch {
		case '|':
			sb.WriteString(styleLane[lane].Render("|"))
		case '\\':
			sb.WriteString(styleLane[lane].Render("/"))
		case '/':
			sb.WriteString(styleLane[lane].Render("\\"))
		case '_':
			sb.WriteString(styleLane[lane].Render("_"))
		case ' ':
			sb.WriteByte(' ')
		default:
			sb.WriteString(styleDim.Render(string(ch)))
		}
		col++
	}

	// Pad to full width (fills in sparks too)
	for col < width {
		if s, ok := sparkAt[[2]int{col, row}]; ok {
			lane := clamp(int(s.x)*numLanes/width, 0, numLanes-1)
			sb.WriteString(styleLane[lane].Render(string(s.ch)))
		} else {
			sb.WriteByte(' ')
		}
		col++
	}

	return sb.String()
}

func feedbackStyle(msg string) lipgloss.Style {
	switch {
	case strings.Contains(msg, "PERFECT"):
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFD700"))
	case strings.Contains(msg, "GREAT"):
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#6BCB77"))
	case strings.Contains(msg, "GOOD"):
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#AAAAFF"))
	default: // MISS
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#FF4444"))
	}
}

// ─── entry point ──────────────────────────────────────────────────────────────

func main() {
	m := newModel()
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
