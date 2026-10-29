# daily-brief

A terminal journal of today's news on the topics you care about. Claude
researches each topic with web search within a token budget you set, then
writes a short digest per topic plus a "Today's highlights" overview.

## Setup

```sh
go build -o daily-brief .
./daily-brief auth status   # first run writes ~/.config/daily-brief/config.yaml
./daily-brief -edit         # choose provider, topics and budget
./daily-brief
```

## Connecting to Claude

Pick one with `provider.type` in the config (or just `provider: claude-code`),
or per run with `-provider <type>`. Only `anthropic` with `auth: env` needs an
exported API key.

| `type` | Uses | Setup |
|---|---|---|
| `claude-code` (default) | Your Claude Code login: Pro, Max, Team or **Enterprise** subscription | Install Claude Code and log in once (`claude`) |
| `anthropic` | Claude API (Console) account | `auth: keychain`: run `daily-brief auth set-key` (stored in the macOS Keychain). `auth: profile`: run `ant auth login` (OAuth, no key). `auth: env`: `ANTHROPIC_API_KEY` |
| `aws` | Claude Platform on AWS (company AWS account) | AWS credentials, e.g. `aws sso login`, plus `region`, `workspace_id`, optional `profile` |
| `vertex` | Claude on Google Cloud Vertex AI | `gcloud auth application-default login`, plus `project_id` and `region` |

```yaml
provider:
  type: anthropic
  auth: keychain
# or
provider:
  type: aws
  region: eu-west-1
  workspace_id: wrkspc_...
  profile: my-sso-profile
```

`daily-brief auth status` checks that the configured provider is ready.

Amazon Bedrock isn't supported because it has no web search tool. Vertex
AI only has the basic web search tool, and doesn't support refusal fallbacks
or the advisory task budget.

## Config

```yaml
token_budget: 0             # tokens per journal: a target and a cap kept by a safety margin; 0 = auto (recommended baseline)
max_searches_per_topic: 0   # optional cap per research pass; 0 = derived from the budget
max_read_minutes: 8         # the journal stays short enough to read in this many minutes
concurrency: 3              # topics researched in parallel
model: claude-opus-5-5
effort: medium              # low | medium | high | xhigh | max
country: PT                 # optional, ISO code to localize search results
price_input_per_mtok: 4     # USD, only for cost estimates on API providers
price_output_per_mtok: 20
price_per_1k_searches: 10
topics:
  - name: Java & Quarkus
    focus: OpenJDK releases and JEPs, Quarkus releases, ...
  - name: Security
    focus: Critical or actively exploited CVEs, supply-chain attacks, ...
    weight: 2               # gets twice the default share of the budget (weights may differ by at most 1000:1)
```

### The token budget

The budget is both a spending target and a cap. A run aims to spend 85-100%
of it with `claude-code` and 80-100% on the API providers, whose safety margin
is larger (see below). It never knowingly goes over; if it goes over anyway,
which is very unlikely but not impossible, the run stops and says so. More
budget buys broader and deeper research, not a longer journal.

Run `daily-brief budget` to see the recommended baseline for your provider and
topics, each topic's weighted share of the budget (shares add up to it: topics
draw from one pool), the searches and items each first pass buys,
the estimated reading time and cost, and any warnings.

- **Baseline.** Each topic is planned for 4 searches on the API providers (3
  with `claude-code`), plus about 10k tokens to write. That comes to roughly
  138k tokens per topic on the API (a search typically costs about 32k: the
  context read twice plus ~20k of results) and 145k with `claude-code`, plus
  15k for the overview. On the API a search may only start when its worst
  case, about 208k on a fresh pass, is free (see below), so the baseline is at
  least that per topic. It is also at least 1.49M, so that what can be left
  after the last search that fits stays under 20% of the budget even once a
  run has measured one large (45k) result, which raises every later search's
  worst case to about 283k: (283k + 15k) / 0.2. That makes the API baseline
  1.49M for 1-7 topics and about 208k per topic beyond. With
  `claude-code` it is raised, in steps of one search, until a planned run
  spends at least 85% despite the safety margin (see below): about 610-630k
  for 1-3 topics. Weights only split the baseline; they don't grow it.
  `token_budget: 0` uses the baseline. For a `-topics` run it uses those
  topics' weighted share of the baseline for all configured topics, raised to
  at least one search pass each plus the overview reserve (so `-topics AI`
  out of 5 equal topics gets about 298k on the API, not the 1.49M floor).
  `daily-brief budget -topics …` shows both figures, and a run prints them
  as a note on stderr.
- **Warnings.** These never block a run. You're warned when the budget is
  under half the baseline or can't fund one search per topic (topics will be
  thin or skipped), above 2× or 4× the baseline (it costs more, but the journal
  stays capped), or estimated at over $8 per run (the API baseline itself is
  about $7.50 at the default prices). You're also warned when a
  topic's weight is too small to fund a pass of its own, when
  `max_read_minutes` forces topics down to one item or skips some, when
  `max_searches_per_topic` leaves most of the budget unspendable, and when a
  `claude-code` budget funds only one search pass beside its safety margin, so
  85% is out of reach, or whenever a run is planned to spend under its target
  (85%, or 80% on the API). Warnings appear on stderr before generating, in full at
  the top of the TUI's Overview pane (the header shows `⚠ budget low/high` or
  `⚠ cost`) and in `daily-brief budget`.
- **Allocation.** About 5% (at most 15k tokens) is set aside for the overview.
  Each topic's share is fixed when it starts: its weighted part of the budget
  still free. Topics that finish under budget therefore fund later ones. On
  the API, a pass is allocated at least what its first search needs by what
  the run has measured so far (K and R below) and the context it really
  sends, a top-up's prior digest included; with `claude-code`, one search
  plus writing. A topic whose share can't pay for that is skipped at first
  and retried, ahead of everything else, in the top-up rounds. A search pass
  that still finds no room for its first search when it starts (another
  pass's large result raised R meanwhile) sends nothing and spends nothing:
  a first pass is marked skipped for budget and retried; a top-up is not
  counted. No topic is written without a search because its budget ran
  short; if the model itself declines to search, the digest is kept with a
  note saying so.
- **Depth.** On the API a pass plans as many searches as its search loop
  (below) can afford with typical 20k results. Each search in a pass costs more
  than the last, as the conversation grows by every search's results, so more
  budget buys more searches per pass and more deepen/widen passes. With
  `claude-code` a pass plans `max(1, 0.85 × share / 45k)` searches.
  `max_searches_per_topic` caps this if set. `daily-brief budget` follows a
  whole run with these plans, and the same reservations as the search loop,
  to estimate searches, spend and cost; when a run will likely spend under
  its target, it says so.
- **Top-up rounds.** Once every topic has had its first pass, the leftover
  budget funds rounds of "deepen or widen" passes. Each pass gets the topic's
  digest and looks for consequential items it missed. If the topic was quiet,
  it widens to adjacent areas: when Java is quiet, for example, it covers other
  languages and frameworks with notable releases or problems, listed under
  **Related**. The pass returns a revised digest, with its own sources, that
  replaces the old one; a failed or cut-short pass keeps the old one. Skipped
  topics go first, then high-weight topics, topics that used all their searches
  and thin topics. Rounds continue while the leftover covers at least one
  first search and the last round made progress (a pass that neither
  searched nor changed the digest is not progress), up to 15 top-ups per
  topic on the API (a fresh pass makes the next search cheap again) and 6
  with `claude-code`.
- **Narrow pass.** If the run would still end below 90% of the budget, the
  last of it (at least 8k tokens) funds one pass per topic that tightens and
  re-ranks the digest without searching; its prompt says not to search. It is
  the only pass that doesn't search, so a topic gets at most one per run.
- **The API search loop.** Each request offers one search (`max_uses: 1`)
  and the prompt asks for one search per turn. Claude searches once, writes its
  digest and ends the turn. If searches remain and the next request's worst
  case fits, the loop sends the reply back with "~N tokens left, M searches
  left: run one more search for a consequential item you haven't covered
  (widen to related areas if the topic is quiet), then rewrite the full
  digest, or reply with it unchanged". It stops when searches run out, the
  next search doesn't fit, Claude adds nothing, or after 12 requests. The
  digest is the last complete one written. If a later request fails or is cut
  off, the previous digest is kept.
- **The cap on the API providers: a large safety margin.** Every response's
  usage, cumulative over its sampling iterations, counts against its pass as
  soon as it arrives. A request offers a search only when what is left, of
  the pass and of the run, covers a deliberately pessimistic worst case:

  `K·(C + R) + K·O + 6k`

  K = 5 is the number of sampling iterations the request is assumed to take.
  A normal search request takes two (one issues the search, one reads the
  results and writes), but each further search Claude attempts is refused
  with `max_uses_exceeded` at the cost of another iteration that reads the
  whole context again, and dynamic filtering may add steps. C is the
  conversation: for the first request a generous estimate (2.5 characters
  per token plus 4k for the search tool's own system prompt and scaffolding),
  then measured from the last `message` or `fallback_message` iteration. R is
  the largest search result, assumed 30k (the top of the usual 10-30k) until
  a larger one is measured. O is the request's `max_tokens` (the digest at its
  item cap plus 4k, about 5k), counted once per iteration, so the bound holds
  whether `max_tokens` caps each iteration or the whole response. The 6k
  covers draft text written before a refused search, the search calls and
  error blocks. Every iteration is counted as reading the results, though the
  first doesn't, which leaves another ~30k of margin. A fresh pass therefore
  needs about 208k free to search, though a search usually costs 30-80k.
  A pass never starts with less than its first search needs. Once a later
  search doesn't fit, the pass keeps the digest it has (or, mid-turn,
  finishes it with a writing-only request, which takes one iteration and
  reserves exactly C plus its `max_tokens`).
  The share is also passed as an advisory task budget where supported.

  The run learns as it goes: if a search request takes more iterations than
  K, or costs more than its reservation, K is raised to match for every later
  request of the run, in every pass, and so is R when a larger result is
  measured. Allocations follow: later passes are sized by the raised worst
  case, so they still search rather than stall.

  The cost of this margin is the end of the budget: once no further search
  fits, up to one search's worst case is left, less what the narrow passes
  spend. In simulation of five topics, a run spends about 87-89% of the
  baseline with typical results, 82-85% when one or all results are 45k, but
  only about 77-79% when every result is 60k (each search then reserves about
  355k). `daily-brief budget` plans with typical results and can't know this
  in advance; a larger budget recovers it (about 90% at 2× the baseline in
  all three cases).

  This makes an overshoot very unlikely, not impossible: in simulation, a
  request at exactly one reservation stays within it with up to four refused
  searches (six iterations), and goes over with five or more. As defence in
  depth, the run tracks what every pass has spent: once the run as a whole
  goes over its budget, no further request, pass or overview starts. The
  overshoot is recorded in the journal (`overshoot`), shown in the TUI header
  (`⚠ over budget by …`) and at the top of the Overview pane, noted at the
  top of `-print` output, and repeated by `daily-brief budget` for today's
  journal. The price of the margin is that runs on the API spend less of
  their budget, typically 80-90% at the baseline, and need a larger
  baseline than before.
- **Enforcement on `claude-code`.** Claude Code runs the search loop itself
  and can't be stopped mid-pass, and its CLI has no turn or token limit
  (no `--max-turns`; only `--max-budget-usd`, which caps dollars), so the
  budget becomes a search limit planned at 85% of the pass's share. Beside each
  pass the run holds back a safety margin of 10% of the pass plus one search
  (45k tokens), which is never given to the pass. This keeps the run within the
  budget when a pass overshoots by up to about 15%. If a pass overshoots anyway,
  no new pass starts once what's left can't cover one, and the overview is
  skipped rather than going over. If the CLI's output can't be read, the pass's
  whole share counts as spent. The margin means a small `claude-code` budget
  can end well under 85%; the baseline is sized to avoid that, and you are
  warned below it. Claude Code's searches are mostly cheap Haiku or
  cache-read tokens, and they count against your subscription quota rather
  than dollars.

### Never overwhelming

`max_read_minutes` (at 230 words per minute) is a hard cap on the total
number of items. Each item is a headline plus at most two tight sentences,
about 45 words. Each topic gets 2 items, then the rest of the cap is split by
weight, up to 10 per topic. When there are too many topics for 2 each, the
floor drops to 1. When there are more topics than items, items go by weight
and the lightest topics are skipped. `daily-brief budget` warns in both cases.
Every pass is told its item and word limits and must rank items by importance.
After every pass the digest is cut to its topic's cap in code, whatever its
format. Items are recognised by `### ` or `## ` headings, top-level bullets or
bold-led lines, and kept whole, dropping items from the end, **Related** ones
included, until the topic fits `cap × 45` words (a Related label gets a few
words of allowance). A preamble gets little room. A single item, a preamble
or a digest with no recognisable items that is too long on its own is cut at
a sentence end and marked "…". A Related label left with nothing after it is
removed, however it is spelled. A topic never lists more sources than it has
items. The overview is capped in code at 5 bullets and about 90 words. The
estimated reading time appears in the TUI header.

Web search is billed separately per search, on top of tokens. See Anthropic's
pricing page.

## Usage

| Command | |
|---|---|
| `daily-brief` | Open today's journal (generated on first open, then cached) |
| `daily-brief -refresh` | Regenerate today's journal |
| `daily-brief budget` | Show the baseline, per-topic plan, read time, cost and warnings (respects `-budget`, `-topics`, `-provider`) |
| `daily-brief -budget 150000` | One-off budget override |
| `daily-brief -topics "AI,Security"` | Research only some topics and merge them into today's journal |
| `daily-brief -provider anthropic` | Use a different connection for this run |
| `daily-brief auth set-key` / `delete-key` / `status` | Manage the Keychain API key, check credentials |
| `daily-brief -print` | Print Markdown to stdout (good for scripts / cron) |

Flags can go before or after a subcommand: `daily-brief budget -budget 1000000`
and `daily-brief auth status -provider aws` both work.

Keys: `↑/↓` or `tab` switch topic, `pgup/pgdn` or the mouse wheel scroll,
`r` regenerates, and `q` quits. Quitting mid-run saves the topics already done.

Journals are saved as JSON in `~/.local/share/daily-brief/journals/`. A journal
in which no topic was summarized isn't saved, and a saved one like that is
treated as missing. Saving never loses what was written earlier today: the
file is written to a temporary file and renamed into place, and a saved
journal that can't be parsed is moved aside to `<date>.json.bak` (or
`.bak.2`, …, never over an earlier backup) before a new one is saved, with a
note on stderr. Until then, opening today's journal reports the parse error;
`-refresh` regenerates it. When
you regenerate, a topic that fails this time keeps its earlier digest, with a
note. A `-topics` run replaces only the topics it researched and keeps the
others, in the TUI and with `-print`. Its overview is rewritten from the
merged topics if the run's budget covers that; otherwise the earlier overview
stays, marked as possibly out of date (as it does when a full run's overview
fails). A `-topics` run keeps each topic's reading cap from the full config, so
the merged journal stays within `max_read_minutes`. Its recorded overshoot is
the sum of its own and the earlier runs' today, since all of it was spent.
`-print` shows a saved
journal without needing credentials.
