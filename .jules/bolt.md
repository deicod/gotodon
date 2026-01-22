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
