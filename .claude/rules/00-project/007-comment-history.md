# Comment History Discipline
<!-- .claude/rules/00-project/007-comment-history.md -- comment
     discipline, tombstone patterns to delete on sight -->

Code comments describe what the code IS and WHY. They do not narrate
what the code USED TO BE before the last refactor. History belongs in
git, not in comments — the comment doesn't move with the code, it rots,
and it sends the next reader chasing context that no longer exists.

## What this rule forbids

Patterns that have repeatedly leaked into codebases and need to be
deleted on sight:

- `Pre-<DATE>-2026, this was X` / `Pre-Q5 (May 7, 2026)` / `pre-v4`
- `was renamed to X` / `were renamed to X`
- `was previously called X` / `previously this was X`
- `gone post-refactor` / `post-refactor cleanup` / `post-Apr-30 X fix`
- `(A4 refactor)` / `(BA review iteration)` / `Tier A trivial wins`
- `extracted from <function>` / `moved from <package>` / `relocated to`
- `now lives at X` / `now an instance method` (when the prior shape is
  the only thing being described — pointing at code that exists is
  fine; narrating its movement is not)
- `Fixed: ` / `Fix: ` / `Fixes #123` / `after the BA fix`
- `- May 7, 2026` trailing date attribution on a comment that doesn't
  describe a still-load-bearing observation (date-tag the observation,
  not the change to the code)

These are all "version history written into the code." Every one of them
is a future grep target for "stupid dangling comments."

## What this rule allows

The code's WHY is load-bearing and stays:

- `// @NOTE: <hidden invariant>` — e.g. "DB writes must hold txn for the
  full slug-collision retry; nested SAVEPOINT would deadlock the WAL."
  The invariant is what the comment is for.
- `// @WARNING: <surprising constraint>` — e.g. lock-order docs, race
  conditions, "do not call this from goroutine X."
- `// @TODO confirm via X` — when there's a real open question. Not
  a marker for "the prior version was wrong."
- Date-tagged findings — `// JWKS endpoint changed shape 2026-05-01;
  see Zitadel CHANGELOG.md` is OK because it's a verification anchor,
  not a change log. `// - Apr 14, 2026` standalone with no observation
  is rot.
- References to docs / external artifacts — `docs/deployment.md`,
  `RFC 8628 §3.4`. These resolve to live documents.
- Citations of reference implementations — `mirrors pkg/x/foo.go::verify`,
  `same shape as <upstream>'s <Type>`. Forward-pointing.

## When you're tempted to write history

Three failure modes drive this:

1. **Refactoring from A to B**: leaving a `// pre-refactor this was A`
   ribbon. Don't. Either the new shape (B) is self-explanatory, in
   which case no comment, or there's a non-obvious WHY for choosing B
   over A — write THAT, without referencing A.
2. **Fixing a bug**: leaving a `// fix for <bug>` marker. Don't. The
   commit message + PR description are the bug-fix history. The comment
   should describe the invariant the fix preserves, not the bug.
3. **Discovering a wrong assumption**: leaving a `// previously
   misidentified as X` ribbon. Don't. The corrected shape is what the
   reader needs; mentioning the wrong shape introduces it as a
   plausible interpretation again.

## When a refactor moves comments

If a refactor genuinely moves load-bearing context (an invariant
attached to a struct that got extracted), MOVE the comment with the
code so it lives next to what it describes. Don't leave a "this used to
live here" stub behind. Don't write a "now lives in foo.go" pointer in
the old location — readers grep, they don't need a forwarding address.

## Enforcement

- Flag any new comment matching the forbidden patterns. Same severity
  as a forbidden import.
- When you spot a violation in code you're not editing, surface it as
  a `reflect:` note rather than ignoring (per CC§1.5). Comment rot
  compounds: every dangling-history comment is an invitation to write
  another one.

## Related rules

- CC top-level (system prompt): "Don't reference the current task, fix,
  or callers — those belong in the PR description and rot as the
  codebase evolves."
- CC§6.2: "when moving or rewriting code, don't leave orphaned comments
  behind — move them with the code they describe, or delete them if
  they no longer apply."
- CC§6.4: "When writing non-obvious code, proactively add `@NOTE:`
  comments explaining the *why*." This rule is the inverse: comments
  that are ABOUT WHY stay; the ones that are about CHANGE go.
