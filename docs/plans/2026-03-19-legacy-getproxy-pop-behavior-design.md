# Legacy GetProxy Pop Behavior Design

**Date**: 2026-03-19
**Status**: Approved
**Author**: Design Session with User

## Executive Summary

Change legacy `GetProxy` so it no longer validates against `TargetUrl`. Instead, it should ensure the cached proxy pool exists for the requested target key, pop one matching proxy from that pool, and return it immediately. Keep `GetWorkingProxy` and `GetWorkingProxyList` as the explicit validated APIs, and update the README so library consumers can quickly choose the right function.

## Assumption

Because the user requested execution to continue without waiting on clarifications, this design assumes the current `TargetUrl` behavior remains as a cache and pool key for legacy bookkeeping, but `GetProxy` no longer performs target validation.

## Goals

1. Make `GetProxy` a non-validating pop/remove operation.
2. Preserve existing target-specific pool bookkeeping so repeated legacy calls continue consuming a target-scoped cached list.
3. Keep `GetWorkingProxy` and `GetWorkingProxyList` as the documented validated choices.
4. Update tests to reflect the new legacy semantics.
5. Improve README examples so common library usage is copy-pasteable and obvious.
6. Release the change with atomic commits, a pushed branch, an annotated tag, and a GitHub release.

## Non-Goals

- Removing or redesigning the working proxy system.
- Changing exported API signatures.
- Ignoring `TargetUrl` entirely in legacy bookkeeping.

## Approaches Considered

### Approach 1: Minimal legacy behavior swap

Keep the current target-keyed `targetUrlProxies` map and its lazy initialization. Replace the validation worker loop in `GetProxy` with a single `pickRandom` call, returning one proxy and removing it from the legacy pool.

**Pros**
- Smallest code change.
- Preserves current cache layout and backward-compatible call shape.
- Matches the requested semantics exactly.

**Cons**
- Legacy metrics become less representative of validation behavior.
- `TargetUrl` still affects pool partitioning even though it no longer validates.

### Approach 2: Share one legacy pool across all targets

Ignore `TargetUrl` in `GetProxy` and `GetProxyList`, using a single legacy pool key.

**Pros**
- Simpler mental model for legacy APIs.
- Less duplicated per-target bookkeeping.

**Cons**
- Behavior change is wider than requested.
- Breaks expectations for callers that currently rely on target-key isolation.

### Approach 3: Keep validation behind a separate internal option

Refactor legacy code so `GetProxy` can optionally validate, but default it off.

**Pros**
- Leaves room for future compatibility toggles.

**Cons**
- Adds complexity without current product value.
- Expands surface area for testing and maintenance.

## Recommendation

Use Approach 1. It is the narrowest safe change, preserves existing bookkeeping, and clearly separates legacy pop-based APIs from the validated working APIs.

## Design

### Legacy API behavior

- `GetProxy` will still call `ensureProxiesLoaded()`.
- It will still lazily initialize `targetUrlProxies[targetURL]` with indices into the shared proxy slice.
- It will then pop one matching proxy via `pickRandom(targetURL, categoryCode)` and return it.
- It will not call `validateProxyCtx` and will not create worker goroutines.
- If no matching proxy remains for that target/category pool, it returns an error.

### Legacy list behavior

- `GetProxyList` remains an unvalidated snapshot of the current target-specific legacy pool.
- Since `GetProxy` removes from `targetUrlProxies`, repeated calls to `GetProxyList` naturally reflect consumption.

### Working API behavior

- `GetWorkingProxy` and `GetWorkingProxyList` remain unchanged in semantics.
- README should explicitly steer users to these functions when they need a proxy already proven against a target.

### Testing

- Add or update tests around `pickRandom`, `GetProxy`, and `GetProxyList` to prove removal semantics and no-validation behavior.
- Prefer deterministic unit tests by seeding/replacing `defaultPool` state directly rather than depending on live validation.
- Keep existing working-proxy tests unless a legacy fallback expectation changes.

### Documentation

- Rewrite the main library usage section to show a quick decision guide:
  - `GetProxy`: fast pop/remove, no target test.
  - `GetProxyList`: cached unvalidated list.
  - `GetWorkingProxy`: validated for a target.
  - `GetWorkingProxyList`: validated list for a target.
- Add short, practical Go snippets for each common use case.
- Fix the malformed sample target URL typo.
- Update API reference wording so the HTTP endpoints align with the library semantics.

### Release workflow

1. Implement code and tests.
2. Run diagnostics on modified files.
3. Run focused tests, then broader `go test ./...` and relevant build commands.
4. Inspect `git status`, `git diff`, and recent commit style.
5. Create atomic commits: one for code/tests, one for docs if the diff is meaningfully separable.
6. Push the current branch to its remote.
7. Create an annotated tag using the repo's existing style if discoverable; otherwise use a sensible `vX.Y.Z`-style tag.
8. Create a GitHub release with a concise summary of the behavior change and README improvements.

## Success Criteria

- `GetProxy` no longer validates against `TargetUrl`.
- Legacy pool consumption is observable through repeated `GetProxy`/`GetProxyList` calls.
- Working proxy APIs remain the validated recommendation in docs.
- Modified files are diagnostics-clean.
- Tests and build pass.
- Commits, push, tag, and GitHub release complete successfully.
