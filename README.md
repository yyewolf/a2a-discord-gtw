# a2a-discord-gtw

Talk to your [kagent](https://github.com/kagent-dev/kagent) agents from
Discord. Every agent gets its own channel in a category, every conversation is
a thread, and when an agent stops to ask you something — a question or a tool
approval — you get pinged and answer with a button. The agent resumes the same
second.

It is built for one owner (or a short allowlist) and one server, and it speaks
A2A v1, which is what kagent 1.0 serves.

## How it works

- **One channel per agent.** The bot creates a text channel for each
  configured agent inside the category, named after it and with the agent's
  description as its topic. A channel you rename is kept; one you delete, or
  move out of the category, is replaced right away. Channels outside the
  category are ignored. If a channel cannot be created (most often: the bot
  lacks Manage Channels on the category), the other agents keep working, the
  error says what is missing, and the bot retries with backoff and whenever
  something needs that channel.
- **Start a conversation** by writing in an agent's channel: the bot opens a
  thread on your message and the reply streams into it. Or use
  `/ask prompt:<text>` — inside an agent's channel it goes to that agent,
  anywhere else pick one with `agent:`.
- **Reply in the thread** to continue the conversation.
- **Status at a glance.** The conversation's top message shows ⏳ working,
  🙋 needs input, ✅ done, ❌ failed or ⛔ canceled — as a reaction on your
  message, or in the bot's own status line.
- **Questions and approvals.** A paused agent's questions are posted with one
  button per choice (a menu when several can be picked, and "Other…" for free
  text); tool approvals get Approve / Reject per tool. The message pings the
  owners. When there is a single question you can also just type the answer
  in the thread; anything else typed there brings the buttons back. However a
  request is answered — button, typed reply, or in kagent's own UI — its
  buttons are then removed, and a click on an outdated button is refused.
- **Tasks you did not start.** For agents with `watch: true` — for example
  ones run by a kagent ScheduledRun — the bot polls kagent and posts new tasks
  in the agent's channel as conversations of their own, with their reply,
  their final state and their questions. History from before the first start
  is not replayed.

- **Nothing falls through the cracks.** Every task the bot has seen is
  recorded, with how much of its reply was already posted. Each poll
  (`watch.interval`) follows up every agent's unsettled tasks, so a reply
  whose stream was cut — kagent restarting, the connection dropping, this
  process restarting — still lands in its thread, from where it stopped. A
  stream silent for 5 minutes is let go the same way, with a note in the
  thread. A thread whose first turn failed before kagent answered still
  works: the next reply starts the conversation afresh.

State (which channel and thread belong to what, what was posted for each
task, which messages carry a request's buttons) lives in a small SQLite
database. Records of settled tasks are pruned after 30 days; the
thread-to-conversation mapping is kept.

### Limits that come from Discord

- A menu holds 25 options: a question with more choices shows the first 25
  and says so; "Other…" takes any answer.
- A message holds five rows of components: approvals with up to four tool
  calls get a row per call, more than four only get Approve all / Reject
  all, and the message says why.
- Messages are split at 2000 characters, on paragraph, line or word
  boundaries, keeping code blocks intact.
- Discord gives three seconds to answer a button: modals open at once and
  the answer is checked against kagent on submit.

## Discord setup

1. Create an application in the [Developer Portal](https://discord.com/developers/applications),
   add a bot, and copy its token.
2. On the **Bot** page, turn **Public Bot** off and **Message Content Intent**
   on (the bot reads what you write in agent channels and threads).
3. Invite it to your server with the permissions it uses everywhere:
   View Channels, Send Messages, Send Messages in Threads, Create Public
   Threads, Read Message History and Add Reactions:

   ```
   https://discord.com/oauth2/authorize?client_id=<application id>&scope=bot+applications.commands&permissions=309237713984
   ```

4. Create a category for the agents and give the bot **Manage Channels** on
   that category only (category settings → Permissions), so it can create the
   agent channels there and nowhere else.
5. Copy the server, category and your user IDs (Developer Mode → right
   click → Copy ID) into the configuration.

Only users in `allowed_user_ids` are listened to; everyone else is ignored and
their button presses are refused.

## Configuration

The agent layout is a YAML file; see [`config.example.yaml`](config.example.yaml).
Unknown keys are an error.

| Key | Meaning |
|---|---|
| `discord.guild_id` | The server |
| `discord.category_id` | Category holding one channel per agent |
| `discord.allowed_user_ids` | Who can talk to agents and answer them |
| `discord.notify_user_ids` | Who is pinged when an agent needs input (default: the allowed users) |
| `kagent.url` | kagent's A2A gateway, e.g. `http://kagent-controller.kagent.svc.cluster.local:8083` |
| `kagent.user_id` | Sent as `X-User-Id`; empty uses kagent's default user |
| `watch.interval` | How often watched agents are polled (default `20s`, minimum `5s`) |
| `agents[].name` | Channel name and `/ask` choice (lowercase letters, digits, `-`, `_`) |
| `agents[].ref` | `namespace/name` of the agent in kagent |
| `agents[].watch` | Surface tasks started outside Discord |

Process settings come from the environment or flags:

| Variable | Flag | Default | Meaning |
|---|---|---|---|
| `ADG_DISCORD_TOKEN` | — | — | Bot token (required, or the file below) |
| `ADG_DISCORD_TOKEN_FILE` | — | — | File holding the bot token |
| `ADG_CONFIG` | `--config` | `/config/config.yaml` | The YAML configuration |
| `ADG_DATA_DIR` | `--data-dir` | `/data` | State database directory |
| `ADG_LISTEN` | `--listen` | `:8080` | Health endpoints |
| `ADG_LOG_LEVEL` | `--log-level` | `info` | `debug`, `info`, `warn`, `error` |

The token has no flag, so it never appears in a process list.

## Run it

```sh
docker run -d --name a2a-discord-gtw \
  --read-only --cap-drop=ALL --security-opt=no-new-privileges \
  -e ADG_DISCORD_TOKEN=... \
  -v ./config.yaml:/config/config.yaml:ro \
  -v a2a-discord-gtw-data:/data \
  ghcr.io/yyewolf/a2a-discord-gtw:latest
```

The image is `FROM scratch`, runs as UID/GID 65532 and only writes to
`/data`. `GET /healthz` answers while the process runs; `GET /readyz` also
checks the Discord connection, the database, that every agent answers in
kagent, and that every agent has its channel (naming the ones that do not,
and why). On SIGTERM the bot cancels the turns in flight (their tasks go on in
kagent and are followed up after the restart), waits up to ten seconds for
them to stop, then disconnects; a second signal exits at once.

In Kubernetes, run one replica (two would both answer every message) next to
kagent, mount the configuration from a ConfigMap and the token from a Secret,
and allow egress to kagent's controller and to Discord (`discord.com`,
`gateway.discord.gg` and its `*.discord.gg` resume hosts).

## Install with Helm

Each release publishes a chart at
`oci://ghcr.io/yyewolf/charts/a2a-discord-gtw`, signed like the image and
versioned with it: chart `0.3.0` deploys image `0.3.0`.

The chart never takes the token itself; put it in a Secret first:

```sh
kubectl create namespace a2a-discord-gtw
kubectl -n a2a-discord-gtw create secret generic a2a-discord-gtw \
  --from-literal=token=<bot token>
```

The minimal values are that Secret's name and the configuration under
`config`, with the same keys as `config.example.yaml`. Quote the Discord IDs:
unquoted, YAML reads them as numbers and the chart's schema refuses them.

```yaml
# values.yaml
discordToken:
  existingSecret: a2a-discord-gtw
config:
  discord:
    guild_id: "1027556538560282765"
    category_id: "1562536781361188945"
    allowed_user_ids: ["<your Discord user ID>"]
  kagent:
    url: http://kagent-controller.kagent.svc.cluster.local:8083
  agents:
    - name: claude-assistant
      ref: kagent/claude-assistant
```

```sh
helm install a2a-discord-gtw oci://ghcr.io/yyewolf/charts/a2a-discord-gtw \
  --namespace a2a-discord-gtw -f values.yaml
```

The chart runs exactly one replica with the `Recreate` strategy, and neither
is a setting. The bot holds one Discord gateway session, so a second pod would
answer every message again, and its state is a SQLite database on a
ReadWriteOnce volume (100Mi by default). A rolling update would briefly run
the old and the new pod side by side; `Recreate` stops the old one first, at
the cost of a few seconds offline per upgrade. Turning `persistence.enabled`
off puts the database in an `emptyDir`, and every restart then forgets which
thread belongs to which conversation.

Changing `config` rolls the pod. With Cilium, `networkPolicy.enabled: true`
adds a policy allowing only DNS, kagent's controller and Discord out and the
kubelet's probes in; if kagent's namespace has its own ingress policy, it must
admit the bot's namespace too. Everything else is in
[`values.yaml`](charts/a2a-discord-gtw/values.yaml).

To check the chart's signature:

```sh
cosign verify ghcr.io/yyewolf/charts/a2a-discord-gtw:<version> \
  --certificate-identity-regexp '^https://github.com/yyewolf/a2a-discord-gtw/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## kagent notes

- kagent 1.0 serves A2A v1 (`SendMessage`, `SendStreamingMessage`,
  `ListTasks`). Bridges built on the A2A 0.3 SDK (`message/send`) cannot talk
  to it.
- A paused task is `TASK_STATE_INPUT_REQUIRED`, with its request under the
  `https://kagent.dev/extensions/hitl/v1` key of the status message metadata —
  its text part may be empty. The answer is a message to the **same task** with
  that extension declared and the response in the same metadata key. Each
  ask-user answer is `{"answer": [...]}`; kagent accepts a bare `[[...]]` too,
  fails to decode it afterwards, and leaves the task stuck in WORKING.
- A plain message on a context whose task is waiting is refused (`native
  session is waiting for another task`); the bot then treats your message as
  the answer when it can, and asks you to use the buttons when it cannot.
- kagent's API trusts `X-User-Id` and is otherwise unauthenticated in the
  default install, so anyone who can reach it can drive every agent. Keep the
  controller off the internet; on Discord, the allowlist is the control.

## Development

```sh
make test lint
go run ./cmd/a2a-discord-gtw serve --config ./config.yaml --data-dir ./data --listen :8080
```

The Discord and kagent sides are interfaces (`bot.Discord`, `bot.AgentClient`),
so the gateway is tested end to end against in-memory fakes;
`internal/kagent/kagenttest` is a fake kagent JSON-RPC endpoint reproducing the
shapes kagent returns. To check the A2A layer against a real agent with an
ask-user tool (the Claude harness has one):

```sh
kubectl -n kagent port-forward deploy/kagent-controller 8083:8083 &
ADG_LIVE_AGENT_URL=http://127.0.0.1:8083/agents/kagent/claude-assistant \
  go test -tags live -run TestLive -v ./internal/kagent
```

Layout:

| Package | Role |
|---|---|
| `internal/kagent` | A2A v1 client per agent, kagent's HITL request/answer shapes, reply text |
| `internal/bot` | The gateway: channels, threads, relaying, questions, watching |
| `internal/discordbot` | DisGo client: connection, slash command, events → gateway |
| `internal/render` | Messages, components and their custom IDs, chunking |
| `internal/store` | SQLite state |
| `internal/config`, `internal/cli`, `internal/app` | Settings, commands, wiring |

## License

BSD 3-Clause.
