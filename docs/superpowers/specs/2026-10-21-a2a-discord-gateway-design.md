# a2a-discord-gtw — design

A Discord bot that is the phone-side front end for a personal set of
[kagent](https://github.com/kagent-dev/kagent) agents. One Discord category
holds a text channel per agent; each conversation is a thread in its agent's
channel.

## Why

The owner runs agents in kagent (a Kubernetes agent platform speaking A2A v1).
Some run on a schedule — notably `windmill-maintainer`, which rebases a fork
onto new upstream releases and must stop to ask a human before force-pushing.
The kagent UI is two-way but sends no notifications; answering hours later is
not acceptable. This bot makes every agent reachable from Discord, with
notifications, and lets a paused agent be answered in seconds.

Existing bridges were tried and rejected:

- `lekkerelou/kagent-a2a-discord`: A2A 0.3 (`message/send`); kagent 1.0.0-alpha17
  only speaks A2A v1 (`SendMessage`). One agent, no threads, no questions.
- `eliasecchig/a2a-gateway`: A2A v1, worked for chat, but one agent per
  instance, never creates threads, and cannot answer a paused task (it sends
  plain text with a contextId only; see "Questions" for why that fails).

## Goals

1. When an agent produces something — including tasks the bot did not start,
   such as kagent ScheduledRuns — it shows up in that agent's channel.
2. Replying in a thread continues that conversation in kagent.
3. Agent questions (ask-user) and tool approvals can be answered from Discord,
   with buttons where possible, and the agent resumes immediately.
4. Several agents live side by side, one channel each, in one category.
5. The owner is pinged (a real Discord notification) when an agent needs input.

## Non-goals

- Multi-user / multi-tenant. One owner (or a short allowlist), one guild.
- DMs. Ignored.
- Managing agents or schedules (that stays in kagent).
- Agents posting arbitrary messages through a tool (an MCP endpoint on the
  bot). Possible later; a task's own output is the message for now.

## Anti-goals

- Anyone outside the allowlist driving an agent. kagent's API is
  unauthenticated in this deployment (it trusts `X-User-Id`), so the allowlist
  is the access control.
- Losing the thread↔conversation mapping on restart, so a reply after a pod
  restart goes nowhere or to a new conversation.
- Flooding the channel with history on first start.

## Decisions (made by the owner)

- Go 1.27, module `github.com/yyewolf/a2a-discord-gtw`.
- Discord library: DisGo (`github.com/disgoorg/disgo`).
- Repository shape, CI, release, lint, Renovate, Dockerfile modelled on
  `yyewolf/SSArchiver` (goreleaser, cosign, attestations, `FROM scratch`,
  conventional commits with auto-tagging).
- Very small commits, one logical change each, conventional commit subjects.

## Decisions (builder's calls, recorded here)

- A2A client: `github.com/a2aproject/a2a-go/v2`, the version kagent pins
  (v2.6.0 at kagent 1.0.0-alpha17), JSON-RPC binding, `A2A-Version: 1.0`.
- Storage: SQLite through a pure-Go driver (no cgo; the image is `FROM
  scratch`), in a data directory.
- CLI with cobra like SSArchiver: `serve`, `version`, `healthcheck`.
- Config: a YAML file for structure (agents), env/flags for scalars and
  secrets. Env prefix `ADG_`. The bot token only ever comes from env or a file.

## How it works

### Configuration

```yaml
discord:
  guild_id: "1027556538560282765"
  category_id: "1562536781361188945"  # one text channel per agent inside it
  allowed_user_ids: ["<owner id>"]     # everyone else is ignored
  notify_user_ids: ["<owner id>"]      # pinged when input is needed (defaults to allowed)
kagent:
  url: http://kagent-controller.kagent.svc.cluster.local:8083
  user_id: ""                          # optional X-User-Id; empty = kagent's default user
agents:
  - name: claude-assistant             # shown in Discord, used in /ask
    ref: kagent/claude-assistant       # namespace/name -> {url}/agents/{ns}/{name}
  - name: windmill-maintainer
    ref: kagent/windmill-maintainer
    watch: true                        # surface tasks the bot did not start
```

Env: `ADG_DISCORD_TOKEN` (required), `ADG_CONFIG` (path), `ADG_DATA_DIR`,
`ADG_LISTEN` (health endpoint), `ADG_LOG_LEVEL`.

### Agent channels

Every configured agent has its own text channel inside `category_id`, named
after the agent (`windmill-maintainer`). The bot creates it when missing, with
the agent's description from its agent card as the channel topic. The
agent → channel ID mapping is persisted, so a channel renamed by hand is reused
rather than duplicated; if the stored channel was deleted, it is recreated.
Channels outside the category are ignored. The bot needs Manage Channels on the
category.

### Threads

An agent's channel holds one top-level message per conversation; the thread on
it is the conversation. A thread maps to exactly one `(agent, contextId)`, stored
in SQLite. Every task in that context belongs to the thread.

The top-level message is the conversation's status line, edited as state
changes: agent name, a one-line title (the prompt, or the scheduled run's
prompt, truncated), and a state marker — working, needs input, done, failed,
canceled.

### Starting a conversation from Discord

- A plain message in an agent's channel (not in a thread) starts a
  conversation with that agent, threaded on the user's own message.
- `/ask agent:<choice> prompt:<text>` — choices are the configured agents.
  Inside an agent's channel `agent` is optional and defaults to that channel's
  agent; elsewhere it is required. The bot posts the status message in the
  agent's channel, opens a thread on it, and sends the prompt.

### Replies in a thread

A message in a mapped thread is sent to that thread's agent with its
`contextId`. If the context has a task waiting for input, the message answers
it (see Questions) instead of being sent as a new turn — kagent rejects new
turns on a context with a pending question
(`native session is waiting for another task`).

Responses stream (`SendStreamingMessage`): the bot posts one message and edits
it as text arrives, throttled to Discord's rate limits, splitting at 2000
characters on paragraph or code-fence boundaries. The typing indicator runs
while the task is working.

### Tasks the bot did not start (watch)

For each agent with `watch: true`, the bot polls kagent's A2A `ListTasks`
every 15–30 seconds for tasks updated since its watermark. A task in a context
the bot does not know gets a new status message and thread in the agent's
channel, then is treated like any other. State changes post into the thread: the final text when it
completes, the error when it fails, the question when it needs input.

The watermark is persisted. On first start it is "now", so history is not
replayed.

### Questions

kagent's human-in-the-loop extension (`https://kagent.dev/extensions/hitl/v1`)
is a structured exchange on the same task. Verified against kagent
1.0.0-alpha17 with the Claude harness:

- The paused task is `TASK_STATE_INPUT_REQUIRED`. Its status message metadata
  holds, under the extension URI, either
  `{"type":"ask_user_request","id":…,"questions":[{"question":…,"choices":[…],"multiple":bool}]}`
  or a tool-approval request
  `{"type":"tool_approval_request","hint":…,"tools":[{"id","call_id","name","args"}]}`.
  The status message's text part may be empty — render from the metadata.
- The answer is a message to the same `taskId` and `contextId`, with
  `extensions: [URI]`, the `A2A-Extensions: URI` header, and metadata under the
  URI:
  - `{"type":"ask_user_response","id":<request id>,"answers":[{"answer":["Blue"]}]}`
    — one entry per question, each `{"answer": [strings]}`. **Not**
    `[["Blue"]]`: kagent's own doc shows that shape, and kagent accepts it,
    fails to decode it, and leaves the task stuck in WORKING.
  - `{"type":"tool_approval_response","approvals":[{"id":…,"approved":bool,"rejection_reason":…}]}`
- Plain text with only a `contextId` is rejected.

Rendering:

- ask-user, single choice: one button per choice, plus "Other…" opening a modal
  for free text. multiple: a select menu with min 1, max all, plus "Other…".
- several questions: one message per question; the answer is sent once all
  are answered.
- a typed reply in the thread answers the pending question when there is one
  single-choice question (matching a choice case-insensitively, else free text).
- tool approval: Approve / Reject per tool (Reject opens a modal for an
  optional reason), and "Approve all" when there are several.
- The question message pings `notify_user_ids`. After answering, its buttons
  are disabled and it shows what was chosen and by whom.

Only `allowed_user_ids` may press buttons or send messages that reach an agent;
others get an ephemeral refusal for interactions and are ignored otherwise.

### Health and operation

`/healthz` (process up) and `/readyz` (Discord gateway connected, kagent
reachable) on `ADG_LISTEN` (default `:8080`). Structured logs (`log/slog`).
Graceful shutdown on SIGTERM. Image `FROM scratch`, UID 65532, writes only to
the data directory.

## Testing

- Unit tests for the A2A layer against an `httptest` server that speaks the
  JSON-RPC shapes above (including the input-required → answer → completed
  sequence and the stuck-task shape as a regression test).
- Unit tests for rendering (question → components, chunking).
- Store tests on a temp SQLite file.
- No live Discord in CI.

## Left to the builder

Package layout, exact poll interval and throttle values, how stream events are
coalesced, and message wording.
