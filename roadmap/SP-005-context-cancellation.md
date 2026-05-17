# SP-005: Context Cancellation

**Status:** ⚠️ Partial — Two remaining items  
**See also:** [docs/extensibility.md](../docs/extensibility.md)

## Remaining

- **`Interrupt()` method** — No public method on `Agent` to programmatically cancel the current conversation. The only way to stop is to cancel the `context.Context` passed to `Run()`.
- **`interruptCtx` / `interruptCancel`** — No internal interrupt context on `Agent`. Adding these would enable `Interrupt()` to work independently of the caller's context.

## What Exists

`ctx.Done()` checked in main loop, `ErrInterrupted` returned on cancellation, `InjectInput()` for mid-conversation input injection — all implemented. See [docs/extensibility.md](../docs/extensibility.md).
