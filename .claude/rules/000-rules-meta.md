# Rules Structure
<!-- .claude/rules/000-rules-meta.md -- rule conventions, naming, sizing -->

All `.md` files under `.claude/rules/` are automatically loaded into
every Claude Code session in this repo. Rules are the operating manual.

## Naming Convention

`.claude/rules/NNN-name.md` where `NNN` is a 3-digit globally unique
number. The first digit is a namespace; add new namespace prefixes to
the table below before creating files in that range.

| Prefix | Section | Contents |
|--------|---------|----------|
| `000` | `(root)` | Meta, architecture, dev stack |
| `0xx` | `00-project/` | Project-wide style + discipline rules. Mostly external references (Beeper Go guidelines, log levels) plus universal Go anti-patterns ported from sibling projects. External-source rules carry a `source:` URL in frontmatter; refresh by re-fetching. |
| `01x` | Reserved | Future operational rules |

Each rule file starts with an H1 title and an HTML comment of the
form `<!-- path -- keywords -->`.

## When to Create a Rule

Rules are for domain-specific operational content that agents need
during relevant work. Detailed rationale and reference material
belong in `docs/`, not rules. Rules should be directives (the "what"),
with pointers to docs for the "why".

## Constraints

- Each rule file stays under 100 lines. If it grows beyond that,
  split it.
- When rules reference code, point to the file (e.g., "read
  `cmd/server/auth.go`"), never to line numbers -- they go stale
  silently.
- Reference docs live in `docs/`. `docs/deployment.md` is the
  operator-facing recipe (threat model, Docker, Caddy, fail2ban,
  smoke test). Rules summarise the procedural slice agents need on
  every session; code is the authoritative behaviour spec, with
  handler comments documenting each contract at the call site.
