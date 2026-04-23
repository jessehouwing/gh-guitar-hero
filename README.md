# gh-guitar-hero

A [GitHub CLI](https://cli.github.com/) extension that turns your repository's
commit graph into a Guitar Hero–style rhythm game. 🎸

## Demo

```
* a1b2c3d Merge feature branch into main        ← commits scroll upward
|\
| * e4f5a6b Add login UI
| * 7c8d9e0 Fix auth token refresh
|/
* f1a2b3c Update dependencies
* 4d5e6f7 Add CI pipeline
══════════════════════════════════════  ← hit zone (press the matching key!)
  [A]      [S]      [D]      [F]      [G]
 Score: 1200  Streak: 4x  Best: 7x  Misses: 1
```

## Installation

```bash
gh extension install jessehouwing/gh-guitar-hero
```

## Usage

Run from inside any git repository:

```bash
gh guitar-hero
```

## How to play

The git commit graph scrolls **upward**. Each branch occupies one of five
colour-coded lanes:

| Lane | Colour | Key |
|------|--------|-----|
| 1 | 🔴 Red | **A** |
| 2 | 🟢 Green | **S** |
| 3 | 🟡 Yellow | **D** |
| 4 | 🔵 Blue | **F** |
| 5 | 🟣 Purple | **G** |

1. Watch a commit `●` approaching the `══` hit-zone line.
2. Press the matching key **when the commit reaches the hit-zone**.
3. **PERFECT** (within 1 line) → 300 pts 🎆 fireworks!
4. **GREAT** (within 3 lines) → 200 pts
5. **GOOD** (within 5 lines) → 100 pts
6. Miss → breaks your streak.

### Hold notes

When a branch has **3 or more consecutive commits**, a bar `┃` connects them.
**Keep holding** the key as all those commits scroll past the hit-zone for bonus
score (+15 pts every 4 ticks).

### Scoring multiplier

Your current streak boosts every hit:
`final pts = base pts × (1 + streak ÷ 5)`

### Grades

| Grade | Accuracy |
|-------|----------|
| ✦ S | ≥ 95 % |
| A | ≥ 85 % |
| B | ≥ 70 % |
| C | ≥ 55 % |
| D | ≥ 40 % |
| F | < 40 % |

## Controls

| Key | Action |
|-----|--------|
| `A` `S` `D` `F` `G` | Hit lane 1–5 |
| `Enter` / `Space` | Start game / Confirm |
| `R` | Restart (game-over screen) |
| `Q` / `Ctrl+C` | Quit |

## Requirements

- [GitHub CLI](https://cli.github.com/) ≥ 2.0
- Go 1.24+ (used automatically by `gh extension install`)
- A terminal with colour support (256-colour or true-colour)
- Must be run from inside a git repository
