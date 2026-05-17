# SP-003: Streaming & Output

**Status:** ✅ Complete — see [docs/conversation-flow.md](../docs/conversation-flow.md)

AgentStreamHandler implements StreamHandler: OnContent writes to content buffer, OnReasoning to reasoning buffer, OnDone records tokens and syncs buffer, OnError publishes error. ProcessQueryStream calls provider.ChatStream() with the handler.
