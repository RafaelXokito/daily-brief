# daily-brief

A terminal journal of today's news on the topics you care about. Claude
researches each topic with web search within a token budget you set, then
writes a short digest per topic plus a "Today's highlights" overview: what
to act on, what is worth knowing, and how each topic fared.

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
max_read_minutes: 9         # the journal stays short enough to read in this many minutes
theme: auto                 # auto | dark | light: the TUI's and -print's colours
history_days: 3             # earlier days' journals not to repeat (0-14); 0 = off
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
  146k tokens per topic on the API (a search typically costs about 34k: the
  context, planned at 6k with a full history block, read twice plus ~20k of
  results) and 145k with `claude-code`, plus
  15k for the overview. On the API a search may only start when its worst
  case, about 213k on a fresh pass, is free (see below), so the baseline is at
  least that per topic. It is also at least 1.51M, so that what can be left
  after the last search that fits stays under 20% of the budget even once a
  run has measured one large (45k) result, which raises every later search's
  worst case to about 288k: (288k + 15k) / 0.2. That makes the API baseline
  1.51M for 1-7 topics and about 213k per topic beyond. With
  `claude-code` it is raised, in steps of one search, until a planned run
  spends at least 85% despite the safety margin (see below): about 610-630k
  for 1-3 topics. Weights only split the baseline; they don't grow it.
  `token_budget: 0` uses the baseline. For a `-topics` run it uses those
  topics' weighted share of the baseline for all configured topics, raised to
  at least one search pass each plus the overview reserve (so `-topics AI`
  out of 5 equal topics gets about 303k on the API, not the 1.51M floor).
  `daily-brief budget -topics …` shows both figures, and a run prints them
  as a note on stderr.
- **Warnings.** These never block a run. You're warned when the budget is
  under half the baseline or can't fund one search per topic (topics will be
  thin or skipped), above 2× or 4× the baseline (it costs more, but the journal
  stays capped), or estimated at over $8 per run (the API baseline itself is
  about $7.80 at the default prices). You're also warned when a
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
  last of it funds one pass per topic that tightens and
  re-ranks the digest without searching; its prompt says not to search. Each
  is allocated at least 8k tokens, and on the API at least what its request
  needs to be sent (its prompt with the history and the digest, plus room to
  write it), about 9-10k. It is the only pass that doesn't search, so a topic
  gets at most one per run.
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
  needs about 205-215k free to search (more with a long prior digest), though
  a search usually costs 30-80k.
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
  overshoot is recorded in the journal (`overshoot`, and in the run's record
  in `runs`), shown in the TUI header (`⚠ over budget by …`) and at the top
  of the Overview pane, noted at the top of `-print` output, and repeated by
  `daily-brief budget` for today's journal. These always describe the latest
  run against its own budget; if an earlier run today went over, the spend
  summary adds it apart, after today's total (`· 30.0k over earlier today`). The price of the margin is that runs on the API spend less of
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
items. The overview is capped in code at 6 highlights and about 180 words,
its numbers and status lines included (see below), or a third of the cap
when that is less (76 words at 1 minute), so at the default 9 minutes there
is room for 41 items with 5 topics, and even 1 minute leaves room for a few.
The config is rejected when `max_read_minutes` leaves no room for a single
item across all topics. The estimated reading time
appears in the TUI header.

### The overview

The overview prompt asks for JSON only (no forced tool use or structured
output, so the API and `claude-code` share one prompt and one tolerant
parser):

```json
{"highlights":[{"tier":"act","topic":"Security","headline":"<the item's headline>","text":"≤18 words","why":"≤14 words"}]}
```

`act` is for security actions and deadlines, `know` for what is worth
knowing; new stories are preferred over updates unless an update is major.
In code, the first JSON object of the reply with highlights is taken (code
fences and prose around it are fine), and each highlight must name a
finished topic and one of its items (the item with that very headline, else
compared as words, either headline starting with the other); others are
dropped. `act` goes first, at most 6 are kept, and text and why are made
plain (links become their text; URLs, control characters and leading `#`,
`>` and backticks are removed) and cut to their word limits and 160
characters. They are saved in the journal as the overview's `highlights`, and its
Markdown (for `-print` and the reading time) is built from them, each with a
link to its item's source. Two lines are added in code at no token cost: a
numbers line (`12 items · 2 updates · 5 topics · ~7 min`) and a status line
per topic (`Java & Quarkus — 3 items, 1 update`, `AI — quiet day`); with
many topics the status lines are left out rather than the highlights.
Highlights are dropped from the end until the whole fits about 180 words. A
reply in prose falls back to its first 5 bullets, made plain the same way,
and the overview then has no highlights. A reply that looks like JSON but
doesn't decode (cut off or mistyped) gets neither: the overview is just the
numbers and status lines, never raw JSON.

Research passes are told to strongly prefer primary sources (vendor
advisories, release notes, official blogs, CISA/NVD, regulator or company
announcements), then reputable outlets, over newsletter digests, directory
sites and unofficial trackers, and to say briefly when an item rests only on
a secondary source. This is guidance in the prompt; no domains are blocked.

Web search is billed separately per search, on top of tokens. See Anthropic's
pricing page.

### Cross-day deduplication

Yesterday's news shouldn't come back as new. Each research pass (first,
top-up and narrow) is shown what the journals of the `history_days` (default
3, at most 14) calendar days before today reported on its topic, as a
compact block in its prompt:

```
<previously_reported>
Tue 27 Oct · Widget 2.0 released · widget.dev/blog/2-0
Mon 26 Oct · Critical flaw in Foo server · nvd.nist.gov/vuln/detail/CVE-2026-1234
</previously_reported>
```

It is told not to repeat these, and to include one only if there is a
material new development since: a release or patch shipped, a decision,
ruling or vote, new figures or scope, an official response or a reversal.
Commentary, recaps and the same news from another outlet don't count. Such an
item is written as `### Update: <headline>` and starts with what is new
("Since Mon: …"); it counts toward the topic's item cap and ranks like any
other item. The TUI shows it as `↻ Update · …`, and the sidebar shows a
muted count of a topic's updates (`↻2`). The marker and the count recognise
the same forms: a heading, bullet or bold line starting `Update:`, bold or
not. The overview prefers new stories
over updates unless an update is major. A big story therefore only returns
as an update; there is no separate "ongoing" list.

This is done by the prompt alone: nothing is filtered or demoted in code.
The history is built from the finished topics of those days' journals, by
topic name (the overview is skipped, and so are today's journal and older
ones; missing or corrupt journals are skipped silently). Each item's
headline is its first line without heading, bullet or bold markers, an
`Update:` prefix or the source links that end it; its link is the first link
in the item, else a source whose title contains the headline, or is contained
in it, word for word, else none. Only http(s) links count; they are compared
without scheme, `www.`, default port, fragment, `utm_*` parameters or
trailing `/`. An item reported on several days with the same link and
headline (or, without a link, the same headline) is listed once, at its
latest date; stories that share a link, such as a releases page, stay apart.
Angle brackets in headlines and links, and in a prior digest, are replaced by
`‹ ›`, so text from the web can't close the prompt's tags. A topic lists at most 12 items, newest first, and about 1,500
characters. A `-topics` run gets history for the topics it researches only.

The block costs input tokens on every request of a pass: up to about 800 by
the API backend's generous estimate (2.5 characters a token), some 550 in
practice, and nothing when there is no history (the prompt is then exactly
as before). The API backend's reservations count it, since they are sized by
the prompt really sent, and `daily-brief budget` plans each first request at
6k tokens, which covers a full block, without reading any journals.
`history_days: 0` turns it off.

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
| `daily-brief -print` | Print the journal: rendered as in the TUI on a terminal, Markdown exactly as saved when piped (good for scripts / cron) |

Flags can go before or after a subcommand: `daily-brief budget -budget 1000000`
and `daily-brief auth status -provider aws` both work.

Keys: the TUI has two panes, Topics and Summary, and starts with Summary
focused. The focused pane's title is in the accent colour. The TUI never
captures the mouse, so you can always select and copy text with it; in the
alternate screen, terminals such as Ghostty, WezTerm and Kitty send the wheel
as `↑/↓`, which scroll the Summary.

| Key | Action |
| --- | --- |
| `←` `h` | focus Topics |
| `→` `l` | focus Summary |
| `enter` | Topics: focus Summary. Overview: go to the selected highlight |
| `⌫` (backspace) | back to the Overview, with the same highlight selected |
| `tab` `shift+tab` | switch pane |
| `↑` `k` / `↓` `j` | Topics: previous / next topic (wraps; doesn't leave Topics). Overview: previous / next highlight. Summary: scroll a line |
| `space` `pgdown` `f` `shift+↓` | page down |
| `b` `pgup` `shift+↑` | page up |
| `d` `ctrl+d` / `u` `ctrl+u` | half page down / up |
| `g` `home` / `G` `end` | top / bottom |
| `1`–`9` | jump to the section at that position (`1` = Overview) |
| `o` | open the source of the selected highlight's item, the item `enter` went to, or the item at the top of the Summary, in the browser (`open`, or `xdg-open`); only http(s) links are opened, others show "No web link" |
| `y` | copy that item as plain text, with its link: `pbcopy` on a Mac outside SSH, else OSC 52 (up to about 75 KB); the help line shows "Copied" only once it was sent |
| `z` | reader mode: hide Topics and use the full width (`z` again, or `←`/`tab`/`esc`, brings Topics back) |
| `?` | show or close the list of keys |
| `esc` | close the list of keys, or focus Topics |
| `r` | regenerate |
| `q` `ctrl+c` | quit |

On the Overview with the Summary focused, `↑/↓` move a cursor over the
highlights (grouped under "Act now" and "Worth knowing", each with its text,
a muted why and its topic) and `enter` opens that topic scrolled to the
item's heading, found by its exact headline among the item headings, or at
the top if the item is no longer there. Paging still
scrolls. `b` stays page up; `⌫` goes back.

The scrolling keys work in either pane, and every key except `r` works while a
journal is being generated (`r` waits for the run to finish). Quitting mid-run
saves the topics already done.

The list of keys opens over the Summary pane. If it doesn't fit, `↑/↓`,
`pgup/pgdn`, `space`, `b`, `g` and `G` scroll it, and a "↓ more" or "↑ more"
hint shows when keys are hidden; `?` or `esc` closes it, and other keys are
ignored while it is open. In a terminal smaller than 50×10 the Topics pane is
hidden and the header names the topic shown; below 20×4 the TUI only says the
terminal is too small. With more topics than fit, the Topics pane scrolls to
keep the selected one in view.

### Rendering

Topics are rendered as Markdown with glamour, in a style derived from its
dark or light style: headings without `#` marks (topic names in the accent
colour, item headlines bold with space above), a margin of one cell, updates
as `↻ Update · …`, a Related label as a rule and a small "Related" heading,
and each source as its title and domain. Digests come from web pages
through the model, so they are treated as hostile. Control characters (C0
but newline and tab, DEL and C1), which a terminal could act on, and the
invisible characters that reorder or hide text (bidi controls such as
U+202E, zero-width spaces, word joiners; the zero-width joiner emoji need
is kept) are removed where the model's output enters the journal, so saved
and piped journals are clean, and again before rendering. So are HTML
character references that would decode into one, such as `&#27;` or
`&#x202E;`, which Markdown decodes after that. As a last guard, the
rendered output, the Overview's list and `-print` on a terminal keep only
colour (SGR) sequences and the app's own OSC 8 hyperlinks; any other escape
sequence, BEL, backspace, carriage return, DEL and C1 is dropped. Only
http(s) links with a host become OSC 8 hyperlinks (Ghostty, WezTerm and
Kitty open them on click), without any `user@` part, so
`https://good.com@evil.com` links to and shows evil.com; a host with control
or invisible characters is not linked. An inline `[source](…)` link or a
bare URL shows as `↗ domain` (without `www.`), or `↗ link`, any other link
as its text. A domain with non-ASCII characters shows as punycode, so a
lookalike such as `аpple.com` (Cyrillic `а`) shows as `↗ xn--pple-43d.com`,
in the Sources list too. A link with
another scheme (`javascript:`, `file:`, …) shows only its text, unlinked. No
raw URL is shown, and URLs with balanced parentheses, as Wikipedia's, link
in full. Each link is replaced by a placeholder of its text's width, made of
private-use characters removed from the Markdown beforehand, before glamour
wraps the text and restored after, so a link never breaks across lines; one
too long for a line is hard-wrapped with its hyperlink closed and reopened
on each line. Notes and warnings keep their `│` bar on every line
they wrap to, and no line is wider than the pane. This is display only: the
saved Markdown is unchanged. The Overview with highlights is drawn by the
TUI itself, so each highlight is a known set of lines.

`theme: auto` uses `GLAMOUR_STYLE` if it is `light`, or `dark` (or another
dark glamour style), and otherwise asks the terminal for its background
before the TUI starts; `dark` and `light` force one. The TUI's own colours
follow it. `-print` on a terminal renders the same way at the terminal's
width; piped, it writes the journal's Markdown byte for byte.

Journals are saved as JSON in `~/.local/share/daily-brief/journals/`. A journal
in which no topic was summarized isn't saved, and a saved one like that is
treated as missing. Saving never loses what was written earlier today: the
file is written to a temporary file and renamed into place, and a saved
journal that can't be parsed is moved aside to `<date>.json.bak` (or
`.bak.2`, …, never over an earlier backup) before a new one is saved, with a
note on stderr. Until then, opening today's journal reports the parse error;
`-refresh` regenerates it. When
you regenerate, a topic that fails this time keeps its earlier digest, with a
note, and that digest's own tokens, searches and passes. A `-topics` run replaces only the topics it researched and keeps the
others, in the TUI and with `-print`. Its overview is rewritten from the
merged topics if the run's budget covers that; otherwise the earlier overview
stays, marked as possibly out of date (as it does when a full run's overview
fails). A `-topics` run keeps each topic's reading cap from the full config, so
the merged journal stays within `max_read_minutes`. Like a full run, it
records its own overshoot; earlier runs' stays in their records in `runs`.
`-print` shows a saved
journal without needing credentials.

**What a journal records about spending.** Each section's `tokens_used`,
`searches` and `passes` belong to the digest it shows: a digest kept from an
earlier run keeps that run's figures. `allocated` is the sum of what the
topic's passes were allocated (first pass plus top-ups and narrow pass), so
it can be larger than the topic's share; journals saved before it was
renamed call it `budget`, which is still read. `token_budget` is the budget of
the latest run, never a sum of budgets. `runs` lists every run saved into the
day's journal with its `budget`, what it `used` in all, and how much of that
was `failed`: spent on topics, or the overview, that failed in that run, even
when the journal shows an earlier digest in their place. A run in which every
call failed therefore leaves the earlier digests and their figures as they
were and adds a run record whose spend is all failed. The TUI header and
`-print` show the latest run against its own budget, what it spent on failed
topics, and what all of today's runs spent, e.g. `190.9k / 200.0k tokens
(95%), 190.9k on failed topics · 645.9k today in 2 runs`, followed by
`· 30.0k over earlier today` if an earlier run went over its budget. A journal saved
before `runs` existed counts as one run.
