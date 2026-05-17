# SP-005: Context Cancellation

**Status:** ✅ Complete  
**See also:** [docs/extensibility.md](../docs/extensibility.md)

## What's Implemented

- `ctx.Done()` checked in main loop — returns `ErrInterrupted` on cancellation
- `Interrupt()` method — public method on `Agent` to programmatically cancel the current conversation via independent `interruptCtx`
- `ResetInterrupt()` — auto-resets at start of each `Run()`/`RunStream()` call, mutex-protected
- `InjectInput()` — mid-conversation input injection
