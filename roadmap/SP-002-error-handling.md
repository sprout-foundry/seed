# SP-002: Error Handling & Retry

**Status:** ✅ Complete — see [docs/extensibility.md](../docs/extensibility.md)

Typed error hierarchy (TransientError, RateLimitError, ContextOverflowError, AuthError, ClientError, ContentFilteredError, BlankResponseError), ClassifyError(), retry with exponential backoff, fail-fast on auth/context-overflow/client. ErrMaxIterations returned when max iterations exceeded, with EventTypeError event published.
