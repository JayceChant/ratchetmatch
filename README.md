# ratchetmatch

[![CI](https://github.com/JayceChant/ratchetmatch/actions/workflows/ci.yml/badge.svg)](https://github.com/JayceChant/ratchetmatch/actions/workflows/ci.yml)
[![CodeQL](https://github.com/JayceChant/ratchetmatch/actions/workflows/codeql.yml/badge.svg)](https://github.com/JayceChant/ratchetmatch/actions/workflows/codeql.yml)
[![govulncheck](https://github.com/JayceChant/ratchetmatch/actions/workflows/vulncheck.yml/badge.svg)](https://github.com/JayceChant/ratchetmatch/actions/workflows/vulncheck.yml)
[![codecov](https://codecov.io/gh/JayceChant/ratchetmatch/graph/badge.svg)](https://codecov.io/gh/JayceChant/ratchetmatch)
[![SonarCloud](https://sonarcloud.io/api/project_badges/measure?project=JayceChant_ratchetmatch&metric=alert_status)](https://sonarcloud.io/summary/new_code?id=JayceChant_ratchetmatch)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/JayceChant/ratchetmatch/badge)](https://scorecard.dev/viewer/?uri=github.com/JayceChant/ratchetmatch)
[![go.dev reference](https://pkg.go.dev/badge/github.com/JayceChant/ratchetmatch.svg)](https://pkg.go.dev/github.com/JayceChant/ratchetmatch)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

**English** | [简体中文](README_CN.md)

An ACBM (Aho-Corasick + Boyer-Moore) multi-pattern matching library optimized for Chinese text: build the keyword automaton once, scan each long text in a single pass, and get every keyword hit. Zero third-party dependencies, Go standard library only.

## When to Use

**Suitable**: fixed keyword dictionary, varying target texts — build once with `New`, then query many long texts repeatedly (content filtering, auditing, risk control, and other streaming scan scenarios).

**Not suitable**: fixed target text, varying keywords — in that case, preprocess the target text once (e.g., an inverted keyword index) and query by keyword instead of scanning by text.

## Performance

```bash
go test -bench . -run '^$'
```

Bad-character skipping gives ~1.4x speedup on mixed Chinese/English text; `FindNext` first-hit-stop is ~10x on long texts. On pure Chinese text with dense keywords, skip opportunities are limited and gains level off — expected behavior.

Versus three semantics-equivalent baseline families (two 100-keyword dictionaries × ~100k-rune texts: a **sparse** one with no overlap/containment, and an **overlap** one full of prefix chains, containment, substrings and single-character keywords; equivalence with the official API is guarded by tests, see `bench_test.go`):

| Pure Chinese long text | Sparse | Speedup | Overlap | Speedup |
|---|---|---|---|---|
| ratchetmatch `FindAll` | 1.71 ms | — | 1.94 ms | — |
| Plain Trie restart scan (no fail chain, no skip) | 2.14 ms | ~1.2x | 2.19 ms | ~1.1x |
| Per-keyword Boyer-Moore bad-character search | 16.4 ms | ~9.6x | 21.4 ms | ~11.0x |
| Per-keyword strings.Index (std SIMD) | 19.1 ms | ~11.1x | 19.2 ms | ~9.9x |

| Mixed Chinese/English long text | Sparse | Speedup | Overlap | Speedup |
|---|---|---|---|---|
| ratchetmatch `FindAll` | 0.95 ms | — | 1.06 ms | — |
| Plain Trie restart scan (no fail chain, no skip) | 1.85 ms | ~1.9x | 1.36 ms | ~1.3x |
| Per-keyword Boyer-Moore bad-character search | 10.9 ms | ~11.5x | 14.1 ms | ~13.3x |
| Per-keyword strings.Index (std SIMD) | 9.2 ms | ~9.7x | 10.0 ms | ~9.4x |

| `FindNext` first hit, mixed text | Sparse | Speedup | Overlap | Speedup |
|---|---|---|---|---|
| ratchetmatch `FindNext` | 0.09 ms | — | 0.13 ms | — |
| Per-keyword strings.Index full-text sweep | 1.74 ms | ~19.7x | 1.67 ms | ~12.5x |

Notes: per-keyword baselines (BM / strings.Index) slow down linearly with dictionary size — one full-text scan per keyword, doubling from 50 to 100 keywords; the overlap dictionary explodes the raw occurrence count for per-keyword BM while the automaton slows down only marginally (amortized O(1) fail chains; outLens inheritance flattens containment). The plain-Trie scan is largely independent of dictionary size, but without skipping it forfeits the mixed-text gain (~1.9x); under the overlap dictionary noise characters often hit keyword first-characters (中, 人, 大…), so skip and first-hit gains fall back accordingly (~1.3x / ~12.5x) — expected. `strings.Index` is already the SIMD-accelerated standard-library search — a hand-rolled byte-by-byte loop only loses the vectorization (analysis, see `bench_test.go`). Figures are order-of-magnitude references that vary with hardware and data distribution.

## Design & Trade-offs

- **Rune-level automaton**: the Trie and transition tables are built on Unicode code points; Chinese characters transition as whole characters, never as fragmented UTF-8 bytes.
- **Sparse transition table + fail-pointer fallback** (instead of a full DFA transition table): each node stores only its own trie edges, flattened into a global sorted array; at query time, a segment miss falls back along the failure chain, amortized O(1). A full table bloats every node to the root's fan-out (~hundreds of keys) under the large Chinese character set — memory-prohibitive; measured and abandoned.
- **BM bad-character skip**: while the automaton is at the root state, a "dictionary first-character set + 256-bit byte filter" batch-skips text segments where no match can start, ~1.4x speedup on mixed Chinese/English text (the starting rune of any match must be a keyword's first character, so the skip criterion is provably safe — no missed hits).
- **Byte-based positions**: `Match.Start/End` are byte offsets, so `text[Start:End]` slices out the keyword directly. No rune-index API — `[]rune` pre-conversion would instantly add +300% memory for ASCII text and defeat the skip and first-hit optimizations.
- **Non-overlapping leftmost-longest semantics**: the smallest start wins; at the same start, the longest keyword occurring in full wins (proper containment always yields the longest match); results are deterministic regardless of how they are found.
- **Lock-free concurrency**: `Matcher` is read-only after construction, laid out as compact contiguous arrays (nearly pointer-free); queries are allocation-free, keeping Go GC marking cost near-constant — suitable for services that keep a dictionary resident in memory for months.
- **Robustness**: invalid UTF-8 text never panics and never skips subsequent content.

## Quick Start

Requires Go 1.27+; module path `github.com/JayceChant/ratchetmatch`.

```go
matcher, err := ratchetmatch.New([]string{"上海", "北京", "人工智能", "机器学习"})
if err != nil {
	panic(err)
}
for _, m := range matcher.FindAll(text) {
	fmt.Printf("%d-%d %s\n", m.Start, m.End, m.Keyword) // Start/End are byte offsets
}
```

For a complete runnable example (with output) see `example_test.go`; full copy-and-run programs live in `example/` (`go run ./basic/`, `./semantics/`, `./iterate/`, `./wordcount/` — matching semantics, on-demand iteration, and term-frequency counting, each a self-contained single file you can copy into your own project). Each Chinese character occupies 3 bytes and each ASCII character 1 byte; matching is exact — `Beijing` does not match `北京`. To get character ordinals, convert with `utf8.RuneCountInString(text[:m.Start])`.

## On-Demand Iteration: First-Hit-Early-Stop for Very Long Texts

`FindNext(text, offset)` returns the first hit from `offset` and stops scanning immediately; iterate by feeding the returned `Match.End` back as the next `offset` — the sequence is identical to `FindAll`. When you only need the first few hits, the rest of the long text is never scanned (~10x on long-text benchmarks).

## API

| Identifier | Description |
|---|---|
| `New(keywords []string) (*Matcher, error)` | Builds an immutable `Matcher`. Returns distinguishable errors for an empty dictionary, empty strings, invalid UTF-8, or U+FFFD bytes in keywords; duplicate keywords are deduplicated |
| `(*Matcher) FindAll(text string) []Match` | All hits, ascending by `Start`; returns `nil` when nothing matches |
| `(*Matcher) FindAllOverlapping(text string) []Match` | All occurrences (including mutually overlapping ones), ascending by `End`, longest first at equal `End`; suitable for term-frequency counting and index building, output-sensitive O(n+K) cost |
| `(*Matcher) FindNext(text string, offset int) (Match, bool)` | Returns the first hit from `offset`, stopping as soon as one is found. `offset<0` is treated as 0; `>=len(text)` or no hit returns `(Match{}, false)`; an offset landing inside a multi-byte character is aligned forward to a rune boundary |
| `Match{Start, End int; Keyword string}` | One hit; `text[Start:End] == Keyword` always holds |

## Matching Semantics (Non-Overlapping Leftmost-Longest)

Scan left to right: the smallest start wins; at the same start, the longest keyword occurring in full wins. Hits never overlap and leave no gaps; each text position belongs to at most one hit.

| Scenario | Dictionary / Text | Output |
|---|---|---|
| Prefix relation → longest | `{"中国", "中国人"}` / `"我是中国人"` | only `中国人` |
| Prefix not fully present → shorter word | `{"中", "中毒"}` / `"中x"` | only `中` |
| Overlap → leftmost start wins | `{"上海", "海口"}` / `"上海口"` | only `上海` |
| Non-overlapping → all, in order | `{"上海", "北京"}` / `"上海人北京"` | `上海`, `北京` |
| Proper containment → always longest | `{"国", "人", "中国人"}` / `"中国人"` | only `中国人` |

When you need **all occurrences** (including overlapping ones), use `FindAllOverlapping`: e.g., dictionary `{"国", "人", "中国人"}` on `"中国人"` returns 3 hits. This mode has no `FindNext` variant (overlapping semantics is incompatible with stateless on-demand iteration).

### Case Folding

Matching mode is decided once at `New` and cannot be changed afterwards. Case-sensitive exact matching is the default; pass `WithCaseFold()` for `strings.EqualFold`-style matching (per-rune SimpleFold orbits):

```go
fm, _ := ratchetmatch.New([]string{"hello", "世界"}, ratchetmatch.WithCaseFold())
fm.FindAll("Hello, WORLD! 世界") // hits Hello(0,5) and 世界(14,20)
fm.CaseFold()                    // true
```

Case variants in the dictionary are merged, so nothing is missed; `Match.Keyword` is the original text slice (e.g., `"Hello"`), while `Start`/`End` still slice the text directly. To use both modes on one dictionary, build two `Matcher` instances.

**Performance note**: the fold automaton pre-expands fold orbits into the transition tables at build time, so the query hot path performs zero per-rune fold comparisons. Measured overhead (`bench_fold_test.go`; the ASCII dictionary whose orbit-expanded keys are ~2x is the worst case, 100 keywords × ~100k-rune English text):

- **Query**: roughly at parity with exact mode (`FindAll` on the same dictionary and text: fold 3.05 ms vs exact 3.04 ms; Chinese-dictionary cases all within ±5% noise) — orbit-expanded keys live in the same CSR binary-search path, and match extraction's rune-level backoff (`runeStartBack`) only walks runes at hit positions.
- **Build**: slightly more expensive than exact (`New` 100 keywords: ~1.4x time, +20% memory; shrinks toward ~1.1x as the dictionary grows, since orbit expansion is amortized across dictionary dedup). Fold orbit resolution (SimpleFold rings) and expanded-key writing are one-time costs at `New`.
- **Semantics bonus**: on case-mixed text, fold finds all case variants that exact mode misses — in the ASCII benchmark fold reports ~1000 extra hits (uppercased keywords) at no measurable scan-time cost.

Rule of thumb: enable `WithCaseFold()` whenever case-insensitivity is desired — the query-side cost is negligible; only builds of very large dictionaries (>10k keywords) may notice the construction overhead.

For the authoritative description of algorithm principles, API contracts, and acceptance scenarios, see `spec/spec.md`.

## License

Released under the [MIT License](LICENSE).
