# ratchetmatch

**English** | [简体中文](README_CN.md)

An ACBM (Aho-Corasick + Boyer-Moore) multi-pattern matching library optimized for Chinese text: build the keyword automaton once, scan each long text in a single pass, and get every keyword hit. Zero third-party dependencies, Go standard library only.

## When to Use

**Suitable**: fixed keyword dictionary, varying target texts — build once with `New`, then query many long texts repeatedly (content filtering, auditing, risk control, and other streaming scan scenarios).

**Not suitable**: fixed target text, varying keywords — in that case, preprocess the target text once (e.g., an inverted keyword index) and query by keyword instead of scanning by text.

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

For a complete runnable example (with output) see `example_test.go`. Each Chinese character occupies 3 bytes and each ASCII character 1 byte; matching is exact — `Beijing` does not match `北京`. To get character ordinals, convert with `utf8.RuneCountInString(text[:m.Start])`.

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

## Performance

```bash
go test -bench . -run '^$'
```

Bad-character skipping gives ~1.4x speedup on mixed Chinese/English text; `FindNext` first-hit-stop is ~10x on long texts. On pure Chinese text with dense keywords, skip opportunities are limited and gains level off — expected behavior.

For the authoritative description of algorithm principles, API contracts, and acceptance scenarios, see `spec/spec.md`.
