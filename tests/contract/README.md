# Pulse SDK Contract Test Suite

This suite defines the **behavioural contract** every Pulse SDK must satisfy.

It runs against a live Pulse server (see `pulse-workspace/pulse` for setup) and validates
that each SDK correctly implements the protocol — regardless of language.

## What's covered

Each test is named by feature and run with a language-specific runner:

| Test                       | What it checks |
|----------------------------|----------------|
| `connect_login_logout`     | Hello/Welcome + token mint + graceful disconnect |
| `send_receive_text`        | 1-on-1 text message: send, ack, receive, history |
| `send_receive_group`       | Group message fan-out to N members |
| `mark_read_unread`         | Mark conversation as read; unread counter updates |
| `reaction_add_remove`      | Add/remove emoji reaction; aggregation |
| `presence_online_offline`  | Connect → online; disconnect → offline |
| `reconnect_after_drop`     | Auto-reconnect with exponential backoff |

## Running

```bash
# Start dependencies and pulse dev server:
docker compose up -d postgres nats rustfs
go run ./cmd/pulse dev --config configs/dev-localhost.yaml &

# Run the contract test suite:
PULSE_TEST_API=http://localhost:32011 \
PULSE_TEST_WS=ws://localhost:32001 \
go test -tags=contract ./tests/contract/...

# Or using Make:
make test-contract
```

## Porting to other SDKs

Each test file has a `// CONTRACT:` header describing the **observable
behaviour** that must hold. To port to a new language:

1. Read the `// CONTRACT:` spec.
2. Translate the setup/teardown into your language's idioms.
3. Assert against the spec's assertions.

Example contract (Go → TypeScript porting):

```ts
// CONTRACT: send_receive_text
// Given: two users alice + bob with valid tokens
// When:  alice sends "hello" to bob in convID
// Then:  alice receives FrameMessageAck with seq >= 1
//        bob receives FrameMessageReceived with text="hello"
//        bob can pull history containing the message
test("send_receive_text", async () => { ... });
```

## Layout

```
tests/contract/
├── README.md
├── connect_login_logout_test.go
├── send_receive_text_test.go
├── send_receive_group_test.go
├── mark_read_unread_test.go
├── reaction_add_remove_test.go
├── presence_online_offline_test.go
└── reconnect_after_drop_test.go
```

Each SDK repo is expected to mirror these tests in its own language. CI runs
all of them as part of the SDK's release pipeline.