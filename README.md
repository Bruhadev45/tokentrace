# tokentrace

**Know what every prompt costs.** One binary, zero config, works with any
OpenAI- or Anthropic-compatible endpoint.

tokentrace is a local reverse proxy that sits between your app and an LLM
API. Point your client's base URL at it and every request keeps working
exactly as before — while tokentrace records each request's token usage,
latency, and cache hits to a local JSONL log and shows you spend summaries
in the terminal.

- **Single static binary**, stdlib only, no daemons, no accounts.
- **Any client, any language** — you change one base URL, nothing else.
- **Streaming and non-streaming** responses both parsed for usage.
- **Cache-aware**: cached reads and cache writes tracked and priced.
- **Private by design**: only usage metadata is ever written to disk.

## Install

### Prebuilt binary (no Go toolchain needed)

Every `v*` tag publishes archives for macOS, Linux and Windows on both
amd64 and arm64, plus a `checksums.txt` (SHA-256), to the
[releases page](https://github.com/Bruhadev45/tokentrace/releases).

```sh
V=0.2.0 OS=darwin ARCH=arm64          # adjust to your platform
base=https://github.com/Bruhadev45/tokentrace/releases/download/v$V
curl -sSLO $base/tokentrace_${V}_${OS}_${ARCH}.tar.gz
curl -sSLO $base/checksums.txt
shasum -a 256 --ignore-missing -c checksums.txt
tar xzf tokentrace_${V}_${OS}_${ARCH}.tar.gz tokentrace
sudo mv tokentrace /usr/local/bin/
```

Windows archives are `.zip` with the same naming.

### From source

Requires Go 1.22+.

```sh
go install github.com/Bruhadev45/tokentrace/cmd/tokentrace@latest
# or from a checkout:
make build          # produces ./tokentrace
```

## Quickstart

1. Start the proxy in front of your provider:

   ```sh
   # Anthropic
   tokentrace serve anthropic --project my-app

   # OpenAI (in another terminal / for another project, pick another port)
   tokentrace serve openai --port 4142 --project my-app
   ```

   `anthropic` and `openai` are built-in aliases for their API base URLs;
   any other endpoint can be given as a full URL
   (`tokentrace serve https://gateway.example.com`) or named in the
   [config file](#config-file).

2. Point your client at it by changing **only the base URL**. Auth headers
   pass through untouched, so keep using your normal API key:

   ```sh
   # Anthropic SDKs
   export ANTHROPIC_BASE_URL=http://127.0.0.1:4141

   # OpenAI SDKs (note the /v1 — OpenAI base URLs include it)
   export OPENAI_BASE_URL=http://127.0.0.1:4142/v1
   ```

3. Run your app, then look at the numbers:

   ```sh
   tokentrace stats                 # spend by model
   tokentrace stats --by project    # spend by project label
   tokentrace stats --by day --since 7d
   tokentrace stats --since 2026-10-01 --until 2026-11-01   # one month
   tokentrace stats --json          # same numbers, machine-readable
   tokentrace tail                  # live view: one line per request
   tokentrace tail --stats          # same, plus a req/min · p50 · cost footer
   ```

## Commands

| Command | What it does |
| --- | --- |
| `serve [ALIAS\|URL] [--upstream ALIAS\|URL] [--port 4141] [--project NAME]` | Run the recording proxy on 127.0.0.1. The upstream is an alias (`anthropic`, `openai`, or one from the config file) or a base URL, given once — as the argument or with `--upstream`. `--project` labels every record. |
| `stats [--project NAME] [--since 24h\|7d\|2026-08-01] [--until ...] [--by model\|project\|day] [--json\|--csv] [--out FILE]` | Aggregated report: requests, tokens, est. cost, avg/p95 latency, cache-hit rate. `--since` is inclusive, `--until` exclusive and takes the same forms, so `--since 2026-10-01 --until 2026-10-02` is exactly one day. Table by default; `--json`/`--csv` for machines, `--out` to write a file. |
| `tail [--project NAME] [--stats]` | Follow the log live; prints one formatted line per new record (polls every 500ms). `--stats` adds a footer pinned to the last line: requests/min and p50 latency over the last 60s, plus requests and cost since `tail` started. |
| `prices` | Print the built-in price table. |
| `version` | Print the build version. |

Records are appended to `~/.tokentrace/records.jsonl` — one JSON object per
line. Set `TOKENTRACE_DIR` to use a different directory (handy for keeping
projects or tests isolated).

## Config file

Optional. tokentrace reads `config.toml` from the same directory as the
records (`~/.tokentrace/config.toml`, or `$TOKENTRACE_DIR/config.toml`);
without one it behaves exactly as if the file were empty.

```toml
# ~/.tokentrace/config.toml
[upstreams]                       # aliases for `tokentrace serve NAME`
gateway = "https://llm-gateway.internal.example.com"
local   = "http://127.0.0.1:11434"
openai  = "https://eu.api.openai.com"   # overrides the built-in alias

[defaults]                        # used when the flag is not given
upstream = "gateway"              # so a bare `tokentrace serve` works
port     = 4141
project  = "my-app"
```

Flags always win over `[defaults]`. Only this subset of TOML is accepted —
bare keys, `"basic"` / `'literal'` strings, integers, `#` comments and the
two tables above. Anything else (arrays, dotted keys, booleans, unknown
keys or tables) is an error that names the key or line, rather than being
silently ignored: a typo like `prot = 4142` fails loudly instead of
leaving you on the default port. The parser is stdlib-only and fuzz-tested
(`go test ./internal/config -fuzz=FuzzParseTOML`).

## Example output

Real output, generated by `make demo` (seeds a throwaway log with sample
records, then runs the actual commands; for `tail --stats` it appends six
records while `tail` runs and shows the final screen):

```
$ tokentrace stats --by model
MODEL                       REQS  INPUT    OUTPUT  CACHED   COST     AVG MS  P95 MS  HIT %
claude-sonnet-4-5-20250929  18    24,777   7,529   147,851  $0.3396  2,547   2,983   85.6
gpt-4.1-2025-04-14          9     28,486   6,738   0        $0.1109  3,414   3,815   0.0
claude-haiku-4-5-20251001   27    19,135   3,906   121,500  $0.0703  907     1,074   86.4
gpt-4o-mini-2024-07-18      24    49,242   6,532   21,669   $0.0134  1,216   1,471   30.6
-----
TOTAL                       78    121,640  24,705  291,020  $0.5342  1,670   3,630   70.5

$ tokentrace stats --by project --since 7d
PROJECT       REQS  INPUT    OUTPUT  CACHED   COST     AVG MS  P95 MS  HIT %
checkout-bot  45    43,912   11,435  269,351  $0.4099  1,563   2,882   86.0
docs-search   33    77,728   13,270  21,669   $0.1243  1,816   3,734   21.8
-----
TOTAL         78    121,640  24,705  291,020  $0.5342  1,670   3,630   70.5

$ tokentrace stats --by day
DAY         REQS  INPUT    OUTPUT  CACHED   COST     AVG MS  P95 MS  HIT %
2026-10-11  26    40,547   8,396   95,829   $0.1787  1,561   3,291   70.3
2026-10-12  26    41,967   8,197   96,450   $0.1819  1,704   3,734   69.7
2026-10-13  26    39,126   8,112   98,741   $0.1736  1,744   3,380   71.6
-----
TOTAL       78    121,640  24,705  291,020  $0.5342  1,670   3,630   70.5

$ tokentrace tail --stats
19:37:22  anthropic 200 1,972ms  checkout-bot                   claude-sonnet-4-5-20250929         in=1,281 out=397 cached=9,151  $0.0125
19:37:22  anthropic 200 1,000ms  checkout-bot                   claude-haiku-4-5-20251001          in=581 out=182 cached=4,318  $0.0019
19:37:22  openai    200 1,397ms  docs-search                    gpt-4o-mini-2024-07-18             in=2,468 out=205 cached=1,058  $0.0006
19:37:22  openai    200 2,688ms  docs-search                    gpt-4.1-2025-04-14                 in=4,192 out=603 cached=0  $0.0132
19:37:22  anthropic 200 2,188ms  checkout-bot                   claude-sonnet-4-5-20250929         in=1,706 out=329 cached=10,097  $0.0131
19:37:22  anthropic 200 1,031ms  checkout-bot                   claude-haiku-4-5-20251001          in=872 out=174 cached=5,181  $0.0023
last 60s: 6 req/min, p50 1,397ms | since start: 6 req, $0.0436
```

The last line is the `--stats` footer. It is redrawn in place — a plain
`\r` + erase-line ANSI rewrite, no curses — on every new record and once a
second, so the 60-second numbers decay when traffic stops. It is written to
stderr, so `tokentrace tail --stats | grep …` still gets clean record lines
on stdout; stderr is expected to be the terminal (redirecting it captures
every raw redraw). Running cost marks unpriced requests with `*`, as
`stats` does.

`HIT %` is the cache-hit rate `cached / (input + cached)`. Token counts are
normalized so this is comparable across providers: `INPUT` is always the
*non-cached* input tokens (OpenAI's `prompt_tokens` includes cached tokens,
so tokentrace subtracts them out; Anthropic's `input_tokens` already
excludes cache reads/writes).

## Machine-readable output

`stats` renders the terminal table by default. Add `--json` or `--csv` for
anything that isn't a human — a CI budget check, a spreadsheet, a dashboard
— and `--out FILE` to write straight to a file instead of stdout (the
confirmation line goes to stderr, so the file is the only output).

```sh
tokentrace stats --by project --json
tokentrace stats --by day --csv --out spend.csv
tokentrace stats --json | jq '.total.cost_usd'
```

### Field names

These names are the interface: they are identical in both formats and will
not be renamed or reordered without a major-version bump. Every count is a
plain integer, not the comma-formatted string the table shows.

| Field | Meaning |
| --- | --- |
| `key` | The group — model name, project label or `YYYY-MM-DD` day, per `--by`. |
| `requests` | Requests in the group. |
| `input_tokens` | Non-cached input tokens (normalized across providers, as in the table). |
| `output_tokens` | Output tokens, including Responses-API reasoning tokens. |
| `cached_tokens` | Cache-read tokens. |
| `cache_creation_tokens` | Cache-write tokens (Anthropic; `0` elsewhere). |
| `cost_usd` | Estimated cost, rounded to 6 decimals. |
| `unpriced_requests` | Requests whose model had no price-table entry (the table's `*`). |
| `unknown_requests` | Requests with no parseable usage at all. |
| `avg_latency_ms` / `p95_latency_ms` | Latency over the group's requests. |
| `cache_hit_rate` | `cached / (input + cached)` as a 0–1 fraction, or `null` (JSON) / empty (CSV) when the group has no input tokens — never a misleading `0`. |

JSON wraps the rows in `{"by": ..., "rows": [...], "total": {...}}`, where
`total` is the same shape aggregated over every row. CSV emits a header
plus one row per group and **no** total line, so the file sums correctly
when loaded into a dataframe; read the grand total from the JSON form.

Both formats always produce a well-formed document — an empty `rows` array
or a bare header row — when nothing matches the filters, so scripts never
have to special-case the empty state.

```
$ tokentrace stats --by project --json
{
  "by": "project",
  "rows": [
    {
      "key": "checkout-bot",
      "requests": 45,
      "input_tokens": 43912,
      "output_tokens": 11435,
      "cached_tokens": 269351,
      "cache_creation_tokens": 44400,
      "cost_usd": 0.409936,
      "unpriced_requests": 0,
      "unknown_requests": 0,
      "avg_latency_ms": 1563,
      "p95_latency_ms": 2882,
      "cache_hit_rate": 0.8598
    },
    {
      "key": "docs-search",
      "requests": 33,
      "input_tokens": 77728,
      "output_tokens": 13270,
      "cached_tokens": 21669,
      "cache_creation_tokens": 3072,
      "cost_usd": 0.124267,
      "unpriced_requests": 0,
      "unknown_requests": 0,
      "avg_latency_ms": 1816,
      "p95_latency_ms": 3734,
      "cache_hit_rate": 0.218
    }
  ],
  "total": {
    "key": "TOTAL",
    "requests": 78,
    "input_tokens": 121640,
    "output_tokens": 24705,
    "cached_tokens": 291020,
    "cache_creation_tokens": 47472,
    "cost_usd": 0.534204,
    "unpriced_requests": 0,
    "unknown_requests": 0,
    "avg_latency_ms": 1670,
    "p95_latency_ms": 3630,
    "cache_hit_rate": 0.7052
  }
}

$ tokentrace stats --by model --csv
key,requests,input_tokens,output_tokens,cached_tokens,cache_creation_tokens,cost_usd,unpriced_requests,unknown_requests,avg_latency_ms,p95_latency_ms,cache_hit_rate
claude-sonnet-4-5-20250929,18,24777,7529,147851,28800,0.339621,0,0,2547,2983,0.8565
gpt-4.1-2025-04-14,9,28486,6738,0,0,0.110876,0,0,3414,3815,0.0000
claude-haiku-4-5-20251001,27,19135,3906,121500,15600,0.070315,0,0,907,1074,0.8639
gpt-4o-mini-2024-07-18,24,49242,6532,21669,3072,0.013391,0,0,1216,1471,0.3056
```

Same seeded log as the table examples above, so the numbers line up
row-for-row with the `--by model` and `--by project` tables.

## Provider matrix

| Endpoint | Non-streaming | Streaming | Usage needs a client option? |
| --- | --- | --- | --- |
| Anthropic `/v1/messages` | yes | yes — `message_start` + `message_delta` | no |
| OpenAI `/v1/responses` | yes | yes — `response.completed` (also `.incomplete` / `.failed`) | no |
| OpenAI `/v1/chat/completions` | yes | final extra chunk, opt-in | **yes** — `stream_options.include_usage` |
| Anything else | request recorded, usage unknown | request recorded, usage unknown | — |

Cache accounting follows the same split everywhere: Anthropic reports
`cache_read_input_tokens` / `cache_creation_input_tokens` alongside an
already-exclusive `input_tokens`, while both OpenAI dialects fold the
cached share into their input count (`prompt_tokens_details.cached_tokens`
for chat completions, `input_tokens_details.cached_tokens` for Responses),
so tokentrace subtracts it back out. Responses API reasoning tokens are
already part of `output_tokens` and are priced as output.

## How streaming usage works

tokentrace parses SSE streams incrementally as bytes flow to your client —
nothing is buffered, tokens are not delayed (the proxy flushes every write).

**Anthropic** (`/v1/messages` with `"stream": true`): the `message_start`
event carries the model, `input_tokens`, `cache_read_input_tokens`, and
`cache_creation_input_tokens`; the final `message_delta` event carries the
cumulative `output_tokens`. Usage is always present — Anthropic streams are
fully accounted with no client changes.

**OpenAI Responses** (`/v1/responses` with `"stream": true`): every
`response.*` event carries the response object, and the terminal event —
`response.completed`, or `response.incomplete` / `response.failed` for a
run that was cut short — carries its `usage`. Nothing has to be enabled
client-side, so Responses streams are fully accounted like Anthropic's.
The dialect reports `input_tokens` / `output_tokens` exactly as Anthropic
does, so tokentrace tells the two apart by the response object itself.

**OpenAI chat completions** (`/v1/chat/completions` with `"stream": true`):
usage arrives only in a final extra chunk, and **only if the client asked
for it**:

```json
{ "stream": true, "stream_options": { "include_usage": true } }
```

Without that option the stream contains no usage. tokentrace still records
the request (status, latency, model) with `"unknown": true`, and `stats`
reports how many such requests it saw.

**Non-streaming** responses are buffered in memory (bounded) and the
`usage` object is read from the JSON. The provider dialect is detected
from the upstream hostname, or sniffed from the response shape when the
host is ambiguous (a gateway or a local server).

## Cost estimation

Costs are computed from a built-in table of USD prices per million tokens
(input / output / cached-read / cache-write where published), matched by
longest model-name prefix — so `claude-sonnet-4-5-20250929` resolves via
the `claude-sonnet-4` entry. Run `tokentrace prices` to see the table.

A model with no table entry is recorded with cost `0` and `"priced": false`;
`stats` marks affected rows with `*` so unknown spend is never silently
mixed into real spend.

## Data & privacy

- Request and response bodies are parsed **in memory, only to extract usage
  metadata**. Bodies are never logged and never written to disk.
- Auth headers (`Authorization`, `x-api-key`, and everything else) pass
  through to the upstream **untouched and are never logged or recorded**.
- The only thing persisted is the record itself: timestamp, project label,
  provider, model name, endpoint path, HTTP status, latency, token counts,
  and estimated cost. See `internal/record/record.go` — that struct is the
  entire disk footprint.
- The proxy listens on `127.0.0.1` only and adds no `X-Forwarded-*`
  headers.

## Limitations

- **OpenAI chat-completions streams need `stream_options.include_usage`**
  (see above); otherwise tokens for that request are recorded as unknown.
  `/v1/responses` streams need nothing.
- **Prices are estimates** from a static table — verify against your
  provider's bill. Unknown models cost `$0` with a `no price` marker.
- One proxy fronts one upstream. Run one `serve` per provider (different
  ports).
- The `tail --stats` footer needs a terminal that interprets ANSI escapes
  (macOS/Linux terminals, Windows Terminal). tokentrace does not enable VT
  processing itself, so a classic Windows console window may print them raw.
- Non-streaming response bodies larger than 8 MiB are relayed fine but
  recorded with unknown usage (the buffered copy is capped).
- The Anthropic messages, OpenAI chat-completions and OpenAI Responses
  dialects are parsed; other body shapes are proxied untouched and
  recorded with unknown usage.

## Development

```sh
make test    # go test ./... -count=1
make vet     # go vet ./...
make fmt     # fails if gofmt would change anything
make build   # build ./tokentrace with the version stamped in
make demo    # seed a throwaway log and print real stats output
make cross   # cross-compile every released platform into dist/
```

Releases are cut by [goreleaser](https://goreleaser.com) from
`.goreleaser.yml`: push a `v*` tag and `.github/workflows/release.yml`
builds the six platform archives, the checksum file and the changelog.
`make snapshot` runs the same pipeline locally without publishing, and
`make cross` covers the build matrix without goreleaser installed.

The end-to-end tests spin up a fake upstream (OpenAI JSON, Anthropic SSE
and OpenAI Responses SSE), proxy real HTTP through tokentrace, and assert
the records that land in the JSONL — including latency and per-field token
counts.

## License

MIT © 2026 Kandimalla Bruhadev — see [LICENSE](LICENSE).
