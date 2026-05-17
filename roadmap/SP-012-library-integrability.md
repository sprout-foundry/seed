# SP-012: Library Integrability

**Status:** ✅ Complete — see [docs/extensibility.md](../docs/extensibility.md)

core/ has no imports of events/ in production code — decoupled via EventPublisher interface. NoopUI and NoopExecutor convenience types provided. Feature gates: DisableFallbackParser, DisableValidator, DisableNormalizer. Test package in internal/test/. Runnable example in example/minimal/.
