# daily-brief

A terminal journal of today's news on the topics you pick. Claude searches
the web for each topic within a token budget you set, writes a short digest
per topic, and adds an overview of what to act on today. It exists so you
can catch up on what matters in under about 10 minutes.

![demo](docs/demo.gif)

30-second demo, also as [MP4](docs/demo.mp4).

## Requirements

- Go 1.26+
- Claude Code installed and logged in. The default provider uses your
  Claude subscription, so no API key is needed.
- macOS or Linux

## Install

```sh
git clone git@github.com:RafaelXokito/daily-brief.git
cd daily-brief
go build -o daily-brief .
```

Or with `go install`:

```sh
go install github.com/RafaelXokito/daily-brief@latest
```

While the repo is private, `go install` needs `GOPRIVATE=github.com/RafaelXokito`
and SSH access to GitHub (for example `git config --global
url."git@github.com:".insteadOf "https://github.com/"`).

## Quick start

The first run writes a default config with five topics and tells you where
it put it.

```sh
daily-brief -edit     # open the config in $EDITOR (or press s in the TUI)
daily-brief budget    # see the recommended budget, plan, read time and cost
daily-brief           # open today's journal; generated on first open, then cached
```

Useful flags:

| Flag | What it does |
| --- | --- |
| `-refresh` | Regenerate today's journal |
| `-topics "AI,Security"` | Research only these topics and merge them into today's journal |
| `-print` | Print the journal. Rendered on a terminal, Markdown when piped |
| `-provider anthropic` | Use another provider for this run |
| `-budget 1000000` | Token budget for this run only |

Flags work before or after a subcommand, so `daily-brief budget -topics AI`
works too. `daily-brief -h` lists everything.

## Keys

| Key | Action |
| --- | --- |
| `↑` `k` / `↓` `j` | Previous / next topic, highlight or item (long items scroll first) |
| `←` `h` / `→` `l` | Focus Topics / Summary |
| `tab` | Switch pane |
| `enter` | Read the topic, or go to the selected highlight |
| `space` / `b` | Page down / up |
| `o` | Open the focused item's source in the browser |
| `y` | Copy the focused item and its link |
| `z` | Reader mode (hide Topics) |
| `m` | Mouse on/off |
| `s` `,` | Settings |
| `r` | Regenerate |
| `?` | All keys |
| `q` | Quit (saves topics already done) |

The mouse is on by default: click a topic or highlight to open it, click an
item to focus it, and the wheel scrolls. Shift-drag selects text and
shift-click opens links (Cmd in Ghostty). `m` turns it off for the session,
`mouse: false` for good.

## Configuration

The config lives at `~/.config/daily-brief/config.yaml` (or under
`$XDG_CONFIG_HOME`). A short example:

```yaml
token_budget: 0          # 0 = auto, the recommended baseline
max_read_minutes: 9      # keep the journal readable in this many minutes
history_days: 3          # don't repeat what the last N days reported (0 = off)
profile: "Java 21, Quarkus 3.x, Kafka; EU ETF investor"   # about you, optional
mute: [crypto, celebrity tech gossip]                     # subjects to skip
topics:
  - name: Java & Quarkus
    focus: OpenJDK releases and JEPs, Quarkus releases, Jakarta EE
  - name: Security
    focus: Critical or actively exploited CVEs, supply-chain attacks
    weight: 2            # twice the default share of the budget
```

Every item gets one tag: `ACT` (do something today), `WATCH` (likely to
matter soon) or `FYI` (worth knowing), and items are ranked in that order.

## Providers

Set `provider.type` in the config, or pass `-provider` for one run.

| Type | Uses | Setup |
| --- | --- | --- |
| `claude-code` (default) | Your Claude Code login | Install Claude Code and log in once |
| `anthropic` | Claude API | Key in the macOS Keychain (`daily-brief auth set-key`), an `ant` profile, or `ANTHROPIC_API_KEY` |
| `aws` | Claude Platform on AWS | AWS credentials plus `region` and `workspace_id` |
| `vertex` | Claude on Vertex AI | `gcloud` credentials plus `project_id` and `region`. Basic web search only |

Amazon Bedrock isn't supported because it has no web search tool.
`daily-brief auth status` checks that the configured provider is ready.

## Cost

On `claude-code` a run uses your subscription quota, not dollars. The API
providers bill tokens plus a fee per web search.

The budget is a cap, not a target to beat: runs stop before it, and on the
API providers an overshoot is very unlikely and ends the run if it happens.
A run aims to spend most of it, because more
budget buys broader and deeper research, not a longer journal. Run
`daily-brief budget` before your first run to see the baseline, what each
topic gets, and the estimated cost.

## Data

Journals are saved as JSON, one file per day, in
`~/.local/share/daily-brief/journals/` (or `$XDG_DATA_HOME/daily-brief/journals/`).
`daily-brief -print` reads a saved journal without credentials.

## More

[docs/GUIDE.md](docs/GUIDE.md) covers budgeting, the safety margin, the
overview, deduplication across days, settings and rendering in detail.
