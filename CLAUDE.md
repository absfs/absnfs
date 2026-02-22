## Code Review Discipline

This codebase has been through multiple rounds of review-driven patches where most
flagged "bugs" turned out to be false positives -- code that was correct but hard
to follow. The fixes added complexity that made the next review even more likely to
produce false positives. That cycle has been broken. Do not restart it.

### Before flagging something as a bug

1. **Trace the actual execution path.** Read the code that runs, not the code that
   looks suspicious from a type signature or function name. Many patterns that look
   wrong in isolation are correct in context.

2. **Check if it's tested.** If tests cover the behavior and pass, the code is
   probably doing what it intends. Read the test to understand the intent before
   concluding the production code is wrong.

3. **Check if it's off by default.** Features controlled by TuningOptions or
   PolicyOptions may be intentionally disabled. Something that looks unused may
   simply be optional.

### Distinguishing "I don't understand this" from "this is wrong"

When you encounter code that seems incorrect, work through these steps in order.
Stop as soon as one resolves the question.

1. **State the invariant you believe is violated.** Write it down concretely:
   "X should never be nil here" or "this lock must be held when Y is accessed."
   If you cannot state a specific invariant, you don't have a bug -- you have a
   question. Ask the question instead of filing a fix.

2. **Find a concrete input that triggers the violation.** Construct an actual
   call sequence, byte payload, or goroutine interleaving that would produce
   wrong behavior. If you can't construct one, the invariant may not actually
   be violated -- the code may have a guard you haven't found yet.

3. **Write a failing test.** Encode your concrete input as a test. If the test
   passes, re-examine your assumption -- the code handles the case. If the test
   fails, you've found a real bug and you have proof.

4. **Check whether the "fix" adds or removes complexity.** A real bug fix should
   make the code simpler or leave complexity unchanged. If your fix requires
   adding a new subsystem, a new configuration option, a new lock, or a new
   abstraction layer, reconsider whether the bug is real or whether you're
   compensating for a misunderstanding.

### What not to do

- **Do not add speculative infrastructure.** No new subsystems, caching layers,
  or optimization paths unless there is measured evidence of a problem. Previously
  removed speculative subsystems are documented in `shelved/` -- do not re-add them.

- **Do not add defensive nil checks for conditions that can't occur.** If a value
  cannot be nil at a given point, adding a nil check obscures the actual contract
  and makes readers think the nil case is possible.

- **Do not add tests that only verify "no panic" or "err == nil."** Every test
  should assert something about the returned value or observable side effect.

## Architecture

NFSv3 server adapter for absfs filesystems. The request path is:

```
TCP connection → RPC decode → HandleCall (policy lock + auth) →
  procedure handler (XDR decode → filesystem op → XDR encode) → RPC reply
```

Key subsystems:
- **server.go**: TCP lifecycle, connection handling via connIO interface
- **nfs_handlers.go**: RPC dispatch, drain-and-swap policy lock
- **nfs_proc_*.go**: NFS3 procedure handlers split by RFC 1813 category
- **operations.go**: Filesystem operations bridging NFS to absfs
- **cache.go**: AttrCache (LRU + TTL), DirCache
- **filehandle.go**: Handle allocation with path deduplication
- **auth.go**: IP filtering, UID/GID squash, TLS cert verification
- **options.go**: TuningOptions (atomic swap) vs PolicyOptions (drain-and-swap)

The TuningOptions/PolicyOptions split exists because stale tuning reads only
affect performance, while stale policy reads could violate security invariants.
This is intentional.

## Development

- Short single-line git commit messages only (enforced by hook).
- No heredoc-style file writes in Bash (enforced by hook). Use Write/Edit tools.
- Run `go test -short -race ./...` before committing.
- The `shelved/` directory contains specs for removed subsystems. Read before
  proposing new infrastructure.
