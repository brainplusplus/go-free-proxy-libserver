# Legacy GetProxy Pop Behavior Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Change legacy `GetProxy` to return one cached proxy without validating against `TargetUrl`, update tests to prove the new pop/remove semantics, improve README guidance for validated vs non-validated APIs, and then verify, commit, push, tag, and publish a GitHub release.

**Architecture:** Keep the existing target-scoped legacy pool (`targetUrlProxies`) and reuse `pickRandom` as the pop/remove primitive. Remove validation work from `GetProxy`, keep working-proxy APIs unchanged, and document the distinction clearly so consumers can choose between speed/pop semantics and target-validated semantics.

**Tech Stack:** Go, standard library concurrency primitives, Go test, Markdown docs, git, GitHub CLI (`gh`).

---

### Task 1: Lock in legacy non-validating behavior with tests

**Files:**
- Modify: `proxy_test.go`
- Test: `proxy_test.go`

**Step 1: Write the failing tests**

Add unit tests that seed `defaultPool` with a tiny in-memory proxy slice and verify:

```go
proxy, err := GetProxy(FreeProxyParameter{TargetUrl: "http://example.com", CategoryCode: "US"})
if err != nil {
    t.Fatalf("GetProxy failed: %v", err)
}

remaining, err := GetProxyList(FreeProxyParameter{TargetUrl: "http://example.com", CategoryCode: "US"})
if err != nil {
    t.Fatalf("GetProxyList failed: %v", err)
}

if len(remaining) != 1 {
    t.Fatalf("expected 1 remaining proxy, got %d", len(remaining))
}
```

Also add a test that uses a malformed or unreachable `TargetUrl` and still returns a proxy, proving no validation occurs.

**Step 2: Run the targeted tests to verify failure**

Run: `go test ./... -run "TestGetProxy|TestPickRandomWithIndices"`

Expected: at least one new test fails because `GetProxy` still validates.

**Step 3: Keep fixtures deterministic**

Use a helper or inline setup that:
- swaps `defaultPool` with a test pool,
- sets `expiry` in the future so `ensureProxiesLoaded()` does not scrape,
- restores the previous `defaultPool` with `defer`.

**Step 4: Re-run the targeted tests**

Run: `go test ./... -run "TestGetProxy|TestPickRandomWithIndices"`

Expected: still failing only on the unimplemented legacy behavior change.

**Step 5: Commit after implementation is green**

```bash
git add proxy.go proxy_test.go
git commit -m "fix: make legacy getproxy pop cached proxies"
```

### Task 2: Replace legacy validation path with pop/remove logic

**Files:**
- Modify: `proxy.go`
- Test: `proxy_test.go`

**Step 1: Simplify `GetProxy`**

Replace the worker, context, winner channel, and `validateProxyCtx` flow with:

```go
key := param.getTargetURL()
ensure targetUrlProxies[key] exists

proxy, ok := defaultPool.pickRandom(key, param.CategoryCode)
if !ok {
    return nil, fmt.Errorf("no proxy available")
}

globalMetrics.LegacyHits.Add(1)
return proxy, nil
```

Keep latency metrics and pool initialization intact.

**Step 2: Remove now-unused imports and comments**

Delete `context` from `proxy.go` if unused and rewrite comments/docstrings so they describe pop/remove behavior instead of validation.

**Step 3: Run targeted tests**

Run: `go test ./... -run "TestGetProxy|TestPickRandomWithIndices"`

Expected: PASS.

**Step 4: Read the diff for behavior wording**

Check `proxy.go` comments and errors so they match the new semantics.

**Step 5: Commit**

```bash
git add proxy.go proxy_test.go
git commit -m "fix: make legacy getproxy pop cached proxies"
```

### Task 3: Refresh README guidance and examples

**Files:**
- Modify: `README.md`

**Step 1: Rewrite library usage into a decision-oriented section**

Add a short chooser near the top of usage:
- `GetProxy` / `GetProxyList`: cached, unvalidated, destructive pop for `GetProxy`
- `GetWorkingProxy` / `GetWorkingProxyList`: validated for a target, use when you need a tested proxy

**Step 2: Add practical Go snippets**

Include copy-paste examples for:
- one quick popped proxy,
- one working proxy validated against a target,
- list of working proxies,
- optional TTL setup.

Use valid sample URLs such as `http://httpbin.org/get`.

**Step 3: Update API reference wording**

Adjust endpoint descriptions and example request comments so `/api/v1/proxy/get` is described as legacy pop/remove, while working endpoints are described as pre-validated.

**Step 4: Re-read README for consumer clarity**

Make sure the distinction is obvious without reading source code.

**Step 5: Commit**

```bash
git add README.md
git commit -m "docs: clarify working proxy library usage"
```

### Task 4: Verify diagnostics, tests, and build

**Files:**
- Check: `proxy.go`
- Check: `proxy_test.go`
- Check: `README.md`

**Step 1: Run diagnostics on modified Go files**

Use LSP diagnostics for every changed Go file.

Expected: no errors.

**Step 2: Run focused tests**

Run: `go test ./... -run "TestGetProxy|TestPickRandomWithIndices|TestGetWorkingProxy|TestGetWorkingProxyList"`

Expected: PASS.

**Step 3: Run full test suite**

Run: `go test ./...`

Expected: PASS.

**Step 4: Run build for confidence**

Run: `go build ./...`

Expected: PASS.

**Step 5: Review git state before release operations**

Run:
- `git status --short`
- `git diff --stat`
- `git log --oneline -10`

Expected: only intended changes remain.

### Task 5: Push, tag, and release safely

**Files:**
- No source changes expected

**Step 1: Confirm branch and remote state**

Run:
- `git branch --show-current`
- `git remote -v`
- `git status`

Expected: branch and push target are clear.

**Step 2: Push the branch**

Run: `git push` or `git push -u <remote> <branch>` if upstream is missing.

Expected: push succeeds without force.

**Step 3: Create annotated tag**

If repo convention is visible, match it; otherwise use a sensible release tag such as:

```bash
git tag -a v0.0.0 -m "Release v0.0.0"
git push <remote> v0.0.0
```

Replace `v0.0.0` with the chosen real tag.

**Step 4: Create GitHub release with `gh`**

Run `gh release create` with a concise title and body summarizing:
- legacy `GetProxy` now pops cached proxies without validation,
- README now explains when to use working proxy APIs.

**Step 5: Capture final release metadata**

Record:
- commit hashes,
- branch name,
- pushed remote,
- tag name,
- GitHub release URL.
