# Bolt's Journal

## 2024-05-22 - [Project Start]
**Learning:** Initial setup of Bolt's journal.
**Action:** Always check this journal for past learnings before starting.

## 2026-01-08 - [Regex Compilation in Hot Path]
**Learning:** Compiling regexes inside frequently called functions (like HTML formatting) is a major bottleneck.
**Action:** Always move 'regexp.MustCompile' to package-level variables for hot paths.
