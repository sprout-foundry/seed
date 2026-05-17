# SP-007: Response Validation

**Status:** ✅ Complete — see [docs/conversation-flow.md](../docs/conversation-flow.md)

ResponseValidator detects incomplete responses (trailing ellipsis, abrupt ending, short, unclosed code blocks) and tentative post-tool responses (planning prefixes under 40 words). Triggers continuation with transient message. Continuation budget limits to 3 consecutive attempts.
