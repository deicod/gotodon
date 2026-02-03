# Bolt's Journal

## 2024-05-22 - [Project Start]
**Learning:** Initial setup of Bolt's journal.
**Action:** Always check this journal for past learnings before starting.

## 2026-01-08 - [Regex Compilation in Hot Path]
**Learning:** Compiling regexes inside frequently called functions (like HTML formatting) is a major bottleneck.
**Action:** Always move 'regexp.MustCompile' to package-level variables for hot paths.

## 2026-01-24 - [Allocation Reduction in Hot Paths]
**Learning:** Functions that return concatenated strings in hot paths (like `linkifyText`) cause massive GC pressure.
**Action:** Refactor helper functions to accept `*strings.Builder` and write directly to it, avoiding intermediate string allocations. Pre-allocate builder capacity with `.Grow()`.

## 2025-06-25 - [IndexByte Overhead in Tight Loops]
**Learning:** Repeatedly calling `strings.IndexByte(text[i:], ':')` in a tight loop introduces significant slice header allocation overhead (~10-20ns per call). For loops parsing dense tokens (like shortcodes), a hybrid "speculative manual scan" for short distances avoids this overhead while preserving SIMD benefits for long gaps.
**Action:** In text parsing loops, use `strings.IndexByte` for the initial jump, but for subsequent checks, try a short manual scan (limit ~16 chars) before falling back to `strings.IndexByte`.

## 2026-01-22 - [HTML Parsing Fast Path]
**Learning:** `xhtml.Parse` and `Render` are expensive. For functions that only transform specific HTML elements (like `<a>`), guarding the parse with a `strings.Contains` check on the input string can avoid the entire overhead for the majority of inputs (e.g. plain text).
**Action:** Implement fast-path checks using `strings.Contains` for expensive transformation functions if the triggers are rare.

## 2026-02-12 - [Merged Text Formatting Pipeline]
**Learning:** Chaining multiple formatting functions (e.g., `linkify` -> `string` -> `formatParagraphs` -> `string`) creates unnecessary intermediate string allocations.
**Action:** Merge formatting steps into a single pass using a shared `strings.Builder`. Handle complex logic (like paragraph breaks) inline or via helper functions that accept the builder.

## 2026-02-18 - [IndexAny vs Byte Loop]
**Learning:** For scanning strings with multiple delimiter targets (e.g., HTML special chars + newlines), `strings.IndexAny` outperforms manual byte-by-byte loops in Go for typical inputs where delimiters are sparse. While `IndexAny` incurs a slice header allocation overhead, its assembly-optimized scanning provides a net benefit for clean text runs.
**Action:** Prefer `strings.IndexAny` over manual byte loops for multi-character search in hot paths, unless the text is known to be extremely dense with delimiters.

## 2026-02-27 - [Lazy Map Initialization in Serializers]
**Learning:** Serialization functions often pre-allocate multiple maps for potential related data (mentions, emojis, media), but in the majority of cases (e.g. plain text status), these maps remain empty. Pre-allocating them causes significant GC pressure.
**Action:** Use lazy initialization for maps in serialization logic. Wrap map allocation and population in `if len(data) > 0` checks. Ensure consuming code handles nil maps gracefully (which Go does naturally).

## 2026-03-05 - [Reusable Slice Buffer in Loops]
**Learning:** Allocating slice headers (e.g., `make([]T, 0)`) inside tight loops adds up to significant GC pressure, even if the backing array isn't allocated immediately. This is common in "extraction" functions called per item.
**Action:** Define a reusable buffer variable (e.g. `var buf []string`) outside the loop, reset it with `buf = buf[:0]` inside, and only allocate a final result slice if data is found.

## 2026-03-10 - [Flattening Slice Allocations]
**Learning:** Storing slices of data per item in batch processing (e.g., `map[int64][]string`) causes N heap allocations. Flattening this into a single large slice `[]string` and using a `map[int64]struct{start, end int}` to track ranges reduces GC pressure significantly.
**Action:** Replace maps of slices with flat buffers and range maps in hot batch processing paths.

## 2026-03-24 - [In-place Slice Filtering]
**Learning:** Using 'filtered := make(...)' to filter a slice allocates unnecessary memory when the source slice can be reused. In hot paths (like text entity extraction), filtering in-place using a write index eliminates this allocation.
**Action:** Use in-place filtering (write index pattern) for slice transformation functions where the input slice ownership allows modification.

## 2026-04-01 - [Slices for Small Sets]
**Learning:** Using `map[string]map[string]struct{}` for small nested sets (e.g., domains per username in mentions) creates excessive allocations (N inner maps). Since the set size is typically very small (1-2), using `map[string][]string` and linear scan is significantly faster and allocates less.
**Action:** Prefer slices over maps for small nested collections where N is small (<5).

## 2026-04-01 - [Efficient Regex Guards]
**Learning:** Guarding expensive regex matching with `strings.Contains` is critical. However, checking for a single character (e.g., `:`) can be too broad and still trigger the regex frequently. Using more specific substrings (e.g., `://`, `xmpp:`) or multiple specific checks is better.
**Action:** Use specific substrings in `strings.Contains` guards for regexes to filter out common false positives.
