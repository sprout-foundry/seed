# SP-002: Error Handling & Retry

**Status:** ⚠️ Partial — One remaining item  
**See also:** [docs/extensibility.md](../docs/extensibility.md)

## Remaining

- **`ErrMaxIterations` not returned** — When `maxIterations` is reached, `runLoop` logs a warning and force-finalizes silently. The sentinel error `ErrMaxIterations` is defined in `core/errors.go` but never returned. The caller receives the final content string with no error, losing the signal that the conversation was cut short by iteration limit.

## What Exists

Typed error hierarchy, `ClassifyError()`, retry with backoff, fail-fast on auth/context-overflow/client — all implemented and tested. See [docs/extensibility.md](../docs/extensibility.md).
