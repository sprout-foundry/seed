# SP-012: Library Integrability

**Status:** ✅ Complete  
**Location:** `core/`, `events/`, `internal/test/`  
**Date:** 2026-05-12

## Motivation

Make `seed` a first-class Go library that any project can `go get` without
importing sprout-specific concerns.

## Changes Made

### 1. Decouple `core` from `events`

- Added `EventPublisher` interface to `core/interfaces.go`
- Added 8 generic event type constants to `core/interfaces.go`
- Removed all `events` imports from production `core/*.go` files
- Renamed `Options.EventBus` → `Options.EventPublisher`
- Replaced `events.*` helper calls with inline `map[string]interface{}` construction
- `events.EventBus.Publish(string, any)` satisfies `EventPublisher` at compile time

### 2. Convenience types

- Added `core.NoopUI` — headless UI (discards output, never prompts)
- Added `core.NoopExecutor` — tool executor with no tools

### 3. Feature gates

- Added `Options.DisableFallbackParser` — skip the fallback parser
- Added `Options.DisableValidator` — skip response validation
- `Options.Optimizer` — already optional (nil = disabled)

### 4. Internalize test package

- Moved `test/` → `internal/test/` — prevents external import of test harness
- Updated `Makefile` to reference `internal/test/...`

### 5. Documentation

- Added package-level godoc to `core` (agent.go)
- Added `README.md` with quick start, interface table, feature list
- Fixed `NewOutputManager` return type to `OutputManager` (was `*defaultOutputManager`)
- Added `example/minimal/main.go` runnable example
- Lowered `go.mod` from 1.25.0 to 1.21

### 6. CI pipeline

- Added `.github/workflows/ci.yml` — tests on Go 1.21/1.22/1.23 matrix,
  vet, format check, build, test with race detection, example smoke test

## Result

```go
import "github.com/sprout-foundry/seed/core"

agent, err := core.NewAgent(core.Options{
    Provider: &myProvider{},
    Executor: core.NoopExecutor,
})
result, err := agent.Run(ctx, "Hello")
```

No dependency on `seed/events`, `seed/internal/test`, or any sprout code.
