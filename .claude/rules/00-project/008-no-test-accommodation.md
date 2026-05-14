# No Test-Accommodation in Production
<!-- .claude/rules/00-project/008-no-test-accommodation.md -- forbid
     production gates whose only purpose is making tests easier -->

Production code MUST NOT carry gates, fallbacks, or nil-checks whose
only justification is "tests bypass the constructor." Fix the test
setup — never bend production around it.

## Anti-pattern (canonical, from upstream)

```go
// pkg/msgconv/from-viber.go (REMOVED)
cc := getClientContext(ctx)
if mc.DirectMedia && msg.MessageID != "" && cc.LoginID != "" {
    // cc.LoginID is always set by cdnCtx; the gate guards against test paths.
    ...
}

// pkg/msgconv/msgconv.go (REMOVED)
func getClientContext(ctx context.Context) *ClientContext {
    if cc, ok := ctx.Value(...).(*ClientContext); ok { return cc }
    return &ClientContext{} // empty fallback for tests
}
```

Two layers of subsidy: helper has a fallback, caller re-checks the
field. Both exist solely because tests construct `&MessageConverter{}`
and pass `context.Background()` instead of the proper context.

## Why it's bad

1. **Test-discipline subsidy.** Production carries a runtime cost for a
   state that cannot occur in production. Dead weight; hides intent.
2. **Compounds across layers.** One sloppy boundary creates N derived
   gates — every caller defensively re-checks fields the helper said
   could be empty.
3. **Hides real wiring bugs.** A production path that forgot the
   context silently degrades to "no slug / empty filename" instead of
   panicking. Per CC§3.1 errors should bubble; per CC§6.7 bad
   patterns multiply.

## The right shape

(a) **Pure sub-package** — extract testable logic into a package
   taking only primitives. The pattern lets tests exercise the pure
   transform without constructing the dependency-laden outer object.
(b) **Real fixtures** — tests exercising real paths set up a fake
   `Store` / synthetic context explicitly. Production accessors panic
   on missing context.

NEVER (c): leave the test shortcut in production code.

## What's allowed

These are NOT test-accommodation — the empty/nil case is reachable from
real production state:

- **Untrusted-wire defense** — multipart form parsing, JSON body
  decoding, anything that handles caller-shaped input.
- **Race-window guards** — e.g. `if s.db == nil` during a shutdown
  goroutine that races with the server stopping.
- **Cycle break** — corrupt persistence walking limits, infinite-loop
  defense in case of bad state.

Test for "real defense": can production reach the empty case from a
deployed binary? Yes → keep. No → test-accommodation.

## Enforcement

Greppable patterns flagged HIGH on review:

- `// guards against test` / `// fallback for test` / `// nil-safe in test`
- `// test path` / `// may be empty in test`
- Any `if x == nil` that exists in a code path that always constructs
  `x` non-nil in production

Real-defense exceptions get a `// race-window` / `// untrusted-wire`
justification on the line.

## Related

- CC§3.1 — errors must bubble, not silently degrade.
- CC§6.7 — bad patterns multiply; convenient nil-fallbacks invite more.
- 007-comment-history.md — same posture: code reflects current truth,
  not detours around testing.
