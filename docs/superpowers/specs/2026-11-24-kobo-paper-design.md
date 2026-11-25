# Kobo paper: daily-brief as an unattended morning edition

Date: 2026-11-25 (started 2026-11-24)
Status: draft, awaiting approval

## Intent

Today daily-brief is read in a terminal, and a journal is only generated when the TUI is opened.
Rafael wants to read it on his Kobo Libra Colour instead.
Every morning, without him doing anything, his home server researches his configured topics, writes the day's paper as a book, and puts it where the Kobo picks it up on sync.
When he picks up the Kobo, today's paper is waiting and yesterday's is gone.

The architecture follows [jstriblet/kobo-morning-paper](https://github.com/jstriblet/kobo-morning-paper): a scheduled job, full-text extraction of articles, one edition per day, delivered to the Kobo.
daily-brief keeps its own strengths in place of that repo's interest model and external curation step: configured topics with focus and weights, the reader profile and mute list, history dedup, the ACT/WATCH/FYI judgement and the token budget.

The paper is not the terminal output in a different file.
It is a book with its own visual identity, written in modern newspaper English.
Where the source article could be fetched, the story is a long summary of it, so the paper reads well offline without carrying whole articles.

## What the reader gets

One book in the Kobo library per day, titled "The Daily Brief · 25 November 2026", replacing the previous one.

```
Cover               framed old-style title page: A's newspaper masthead (issue number, date,
                    double rules, blackletter "The Daily Brief", topics, "Edition of the Morning"),
                    and one italic line with the day's lead story
The Day in Brief    front page: the two or three biggest stories with their opening paragraphs,
                    then a "the day in brief" column with one sentence for each other story;
                    every entry links to its story inside
Chapter the First   "Of Java & Quarkus": lead story, other stories, then "In Brief" notes
Chapter the Second  "Of Security" ...
...                 one chapter per topic, in config order (World & hot topics if general: true)
Colophon            "Of This Edition": date, model, tokens spent of the budget (and any overshoot),
                    sources summarised and failed, editor fallbacks, topics failed or cut, fonts
```

Each chapter is a table-of-contents entry in the Kobo menu.

Story length follows what the editor has to work from (Rafael, 2026-11-25: "a long summarized version" of the source instead of the full article, which is bloated):

- **Story with a fetched source article:** a long summary of that article in newspaper form, about 250 to 450 words for a chapter's lead story and 150 to 300 for the others.
- **Story without one** (fetch failed, paywall, too short): one paragraph from the research digest alone.
- **"In Brief"** notes at a chapter's end: one paragraph, for the minor items.
- Every story ends with a quiet "From <site>" line naming its source.

There are no full articles in the book.

### Visual identity (chosen by Rafael on 2026-11-25)

A mix of two mockup directions, "A. The Broadsheet" and "C. The Almanac" (`.superpowers/brainstorm/`, files `identity.html` and `identity-v2.html`).

- **Cover:** C's framed title page (double frame, "Being a Record of the Day", fleurons ❦ and ⁂, the year in roman numerals) with A's masthead block in place of C's title and emblem, plus the day's lead story as one italic line (Rafael picked "cover with the day's lead" over the quiet cover).
- **Inside:** C's lettering and chapter style: "Chapter the Third" in green small caps, an italic chapter title "Of Security", a fleuron, a drop cap.
- Stories: small-caps headline, an italic standfirst, a dateline, then paragraphs; ⁂ between stories.
- Urgency shows as placement and wording; where a label is needed it is words, with "Act now" in A's ink red.
- Paper `#efe6d2`, ink `#2a2318`, green `#2f5a3a`, red `#8a1c1c`, muted for the Libra Colour's screen.
- Fonts embedded in the EPUB, all under the SIL Open Font License: IM Fell English (roman, italic, small caps) for text and headings, UnifrakturMaguntia for the nameplate.
  The Kobo only shows embedded fonts when the reading font is "Publisher Default"; the GUIDE says so.

The 2026-11-25 spike book is the visual reference, and it is on Rafael's Kobo.
It and everything that made it are kept in `docs/superpowers/specs/kobo-paper-assets/`: `reference-2026-11-19.epub` (local only and gitignored, because it contains whole copyrighted articles and the repo is public), `cover-reference.png` (the reference cover), the throwaway builder `build2.py` with `paper2.css` and `cover.tmpl.html`, and the two mockup pages `identity.html` and `identity-v2.html`.

### Wording (chosen by Rafael on 2026-11-25: modern newspaper English)

Before:

> **EU confirms AI-powered cyberattack on its systems**
> `FYI` detected Oct 22 · apps taken offline, mostly restored
> A Commission spokesperson confirmed that attackers used AI tools, the first such attack the bloc has reported. The investigation is ongoing, and so far there is no evidence of a breach affecting staff data. [source]

After (using only the facts in the item; the first version of this example added a place, a time and a detail the item did not contain, which is exactly what the editor must not do):

> **EU says attackers used AI against its systems**
> *The first such attack the bloc has reported; no sign so far that staff data was affected*
>
> The European Commission has confirmed that the attackers who hit its systems used AI tools, the first attack of its kind the bloc has reported. The breach was detected on 22 October, and the applications taken offline have mostly been restored. The investigation continues; so far there is no evidence of a breach affecting staff data.

Modern prose, not old-fashioned language: the type and ornaments carry the identity, the text stays quick to read.

## Goals

1. A new command runs the whole edition unattended: research today's journal, fetch the source articles, write the paper with an editor pass, build the EPUB, deliver it.
2. Every run has an explicit token budget, planned so the run stays within it (see "Token budget" for the `claude-code` caveat Rafael accepted).
3. The paper reaches the Kobo through its built-in Dropbox sync, and only the newest paper stays on the device.
4. The paper has the visual identity and the newspaper wording above.
5. The job runs on Rafael's home server on a schedule, wakes it if it is asleep, and lets it go back to sleep afterwards under its normal idle rule.
6. A failure in an article, in the editor pass, or in delivery never loses the edition and never replaces a good paper with a broken one.

## Non-goals

- A long-running daemon or HTTP server. "Server mode" means an unattended scheduled command on the home server.
- Instapaper, email, Calibre, Calibre-Web or the Kobo Sync protocol as delivery (see Approaches).
- That repo's interest model (Kobo reading progress, Seerr, TikTok, Kickstarter) and its external curation step.
- Changing how the TUI generates or shows journals, or the TUI's wording.
- KEPUB conversion: plain EPUB 3 worked on the device.
- Full source articles in the book, and images from them.
- Sharing config, journals or history between the home server and the laptop: each keeps its own, with its own topics (Rafael, 2026-11-25: the server has topics he does not want in the TUI and the reverse).
- Windows support for the new commands.

## Anti-goals

These would make it a failure even if it technically works:

- A run with no budget set, or a run whose overshoot goes unrecorded.
- The editor pass adds a fact, number, name or claim that is in neither the research item nor its source article, or drops a hedge the research made ("secondary reporting", "severity still unclear").
- A broken or empty paper replaces a good one on the Kobo, or the Kobo is left with no paper because a delivery half-failed.
- daily-brief deletes or overwrites anything in Dropbox that it did not create.
- Untrusted web content reaches the book unsanitised, or a fetched page makes daily-brief request non-http(s), private or local addresses.
- Secrets (the Dropbox refresh token) end up in `config.yaml`, logs, the journal, the repo or unencrypted on the server's disk.
- The interactive TUI flow changes behaviour for people who never use the paper.

## Constraints

- The Kobo always pulls; nothing can push to it.
  It downloads from Dropbox `Apps/Rakuten Kobo/` when it syncs.
  Verified on 2026-11-25: a new file appears after a sync, and a file deleted from Dropbox disappears from the Kobo after a sync.
- Replacing a book under the same file name and the same book identifier is unreliable: on 2026-11-25 the Kobo showed edition B in some places and edition A in others.
  Each edition must have its own file name and its own EPUB identifier.
- The `Apps/Rakuten Kobo` folder belongs to Kobo's own Dropbox app, so daily-brief's Dropbox app needs Full Dropbox access; an app-folder app cannot reach it.
- Dropbox setup traps met on 2026-11-25, for the GUIDE:
  - Team scopes on the app (`team_data.member`, `team_data.governance.*`) make Dropbox demand a work account ("Please sign in with a work account"); the app must have no team scopes.
  - Sign in with Apple looped on Dropbox's app-authorisation page in every browser; signing in with email and password (after setting a password at dropbox.com/forgot) worked.
  - The no-redirect flow showed no code; a registered redirect URI (`http://localhost:53682/`) whose address bar URL is pasted back worked, even with nothing listening there.
- The `claude-code` provider cannot stop a pass mid-run: the CLI has no token or turn limit, only `--max-budget-usd`, and daily-brief counts cache reads as full tokens while they cost about a tenth in dollars, so a dollar cap cannot express a token ceiling (see "Token budget").
- daily-brief stays a single Go binary with no runtime dependencies: no Chrome, Pandoc, Python or Calibre on the server.
- Existing conventions apply: gofmt, `go vet`, `go test -race` in CI, the test style already in the repo (including adversarial and security tests), docs in `README.md` and `docs/GUIDE.md`.
- The home server sleeps after 30 minutes with nothing running; the job must keep it awake while it runs and must not keep it awake afterwards.
- Source articles are copyrighted; the paper only summarises them, names each source, and is for Rafael's own device, never published.

## Approaches considered

**A. EPUB to Dropbox `Apps/Rakuten Kobo/`, a dated file per day, upload then delete (chosen).**
A real book: cover, chapters, table of contents, embedded fonts, colour, internal links.
Tested end to end through the Dropbox API on 2026-11-25: upload, Kobo sync, upload the next edition, delete the old one, Kobo sync; only the new edition remained.
Cost: a Dropbox app with Full Dropbox access and a long-lived refresh token on the server.

**B. Instapaper API v2, one private article per day.**
Tested on 2026-11-25: works on a free account, about 400 KB accepted, the article syncs to the Kobo and reads well.
Rejected because Instapaper flattens the paper to one plain article: no styling, no table of contents, no internal links (link text is even dropped), so the visual identity is impossible.
Kept as the fallback; the findings are in the appendix.

**C. Instapaper by email.**
Rejected on 2026-11-24: Microsoft's outbound filter blocked the message from Rafael's Hotmail account as spam (`550 5.7.520`).

**D. Calibre-Web Kobo Sync (that repo's route).**
Rejected: needs Calibre, Calibre-Web and Docker, network exposure, a device config edit the Libra Colour reportedly reverts, and the server awake on every sync.

## Design

### The command

`daily-brief paper` runs one edition end to end and exits.
It never opens the TUI and writes progress to stderr as plain lines, suitable for the systemd journal.

Steps:

1. **Check the budget** (below).
   Refuse to run, with a non-zero exit and a clear message, unless the budget is a positive number.
2. **Research today's journal** with the existing generator, unattended, within the research share of the budget, using the server's own config and topics.
   If today's journal already exists on that machine (an earlier run, or the TUI opened there), reuse it and spend no research tokens, unless `-refresh` is given.
3. **Fetch the source articles** for the items (no tokens).
4. **Edit**: write the paper's text from the journal and the fetched articles with the editor pass, within the editor share of the budget.
5. **Build the EPUB**, saved to `~/.local/share/daily-brief/papers/The Daily Brief <date>.epub`.
6. **Deliver** to Dropbox, unless `-no-deliver` is given.
7. **Report**: one summary line with tokens spent of the budget (and any overshoot), stories, sources summarised and failed, editor fallbacks, and the delivery result.
   Exit codes distinguish config or budget errors, research failure, and delivery failure (paper kept locally).

`daily-brief paper -no-deliver` builds the local book only, for testing and for opening it in any EPUB reader.

Partial editions (Claude's call, proposed to Rafael and not objected to): if some topics failed or were cut by the budget, the paper is still delivered and the colophon names them.
If no topic produced any story, nothing is delivered and yesterday's paper stays.

### Token budget: mandatory, kept within a wide margin

Rafael's decisions: every run must have a budget set (option a).
He first asked that a run never go over it; once it was clear that `claude-code` cannot guarantee that (Constraints), he chose to keep `claude-code` with a wide safety margin over moving the paper to an API provider (2026-11-25).

- New config key `paper.token_budget` (tokens per edition), required by `daily-brief paper`.
  `-budget` overrides it for one run and must also be positive; `0` (auto) is refused for the paper.
- The budget covers everything the run spends: research passes, the overview, and the editor pass.
- The editor share is reserved before research starts, the way the overview reserve works today, so research can never eat it.
  Its size is planned from the number of stories and the length of the article text they will be given (capped per article); the builder sets the formula and tests it.
- **The budget is a ceiling the plan keeps well clear of.**
  A pass starts only if its planned cost plus its margin fits in what is left.
  On API providers this already holds per request.
  On `claude-code` the existing per-pass margin (10% plus one search) is widened for the paper so that no run crosses the ceiling in the rollout's test runs; the builder sizes it from those runs and records the reasoning in the code.
  Editor passes are bounded mostly by their input, which is known before they start, so their margin can be smaller than a research pass's.
- `--max-budget-usd` is not a token ceiling (Constraints); the builder may still set it generously as a guard against a runaway pass, never as the budget.
- **Overshoot is recorded, never hidden:** if a run spends more than its budget, the summary line and the colophon say by how much, and the run logs a warning to the systemd journal.
- The spend is recorded in the journal as today, and shown in the colophon and the summary line.
- `daily-brief budget` shows what `paper.token_budget` buys (research, overview, editor) before Rafael schedules it.

### Fetching source articles

The articles are input for the editor only; they never appear in the book.


- Source URLs come from each item's link, using `itemURL` in `internal/brief/journal.go` (the logic history dedup already uses).
- Only `http` and `https` URLs with a public host are fetched; private, loopback and link-local addresses are refused, including after redirects.
- Fetch with a timeout, a response size cap, a fixed user agent and limited concurrency.
- Extract the main content with `github.com/markusmobius/go-trafilatura` (Go port of the trafilatura library that repo uses).
- Keep plain text only, capped per article (the builder picks the cap, a few thousand words), before it goes into the editor's prompt.
  The article is untrusted input: the editor prompt marks it as quoted source material, never as instructions.
- If a fetch fails, the text is too short after extraction (paywall or cookie wall), or the page is not HTML, the story is written from the digest alone, and the colophon counts the source as failed.
- The same URL cited by several items is fetched once.
- In the 2026-11-24 test on the 2026-11-19 journal, 30 of 42 sources extracted (Python trafilatura).

### The editor pass

The editor turns the journal and its source articles into newspaper text.
It never researches and never searches; it only writes from what it is given.

- **Input:** the day's journal sections (headline, tag, facts line, prose and source host of each item) and the overview's highlights, each item with a stable id, plus each item's fetched article text when there is one (Rafael, 2026-11-25).
- **Output:** structured JSON, per chapter and for the front page:
  - each story: item id, headline, standfirst, dateline (place when the research names one, otherwise none), paragraphs, placement (lead, story, in brief);
  - the front page: two or three lead stories with their opening paragraphs, and one sentence for every other story in "the day in brief";
  - the cover's lead line.
- **Rules given to the model:** modern newspaper English; most important fact first; no fragments or `·` lists; urgency as placement and wording ("Patch today"), never as tags; the lengths in "What the reader gets"; every story keeps the research's hedges, and where the article and the research disagree, the story says so rather than picking one; no facts, numbers, names or claims that are in neither the item nor its article; every item appears exactly once.
- **Passes:** one per chapter plus one for the front page and cover line, so a failure stays local and each output stays small (Claude's call).
- **Checks before a story is used** (Claude's call):
  - the JSON parses and matches the schema; every item id is known and appears exactly once;
  - every number, version, CVE id and money amount in the story's text also appears in its source item or its article.
  A story that fails falls back to a mechanical rendering of the item: its headline, its facts as the standfirst, its prose cleaned of tags, `·` lists and "source" links.
  A chapter whose pass fails entirely falls back the same way for all its stories.
  The colophon counts fallbacks.
- **Provider:** the run's provider and model, within the reserved editor share.

### The book (EPUB 3)

- Built in Go: XHTML chapters from `html/template`, an OPF package with a nav document and an NCX, zipped with `mimetype` first and stored.
  The spike's hand-written assembler (`build2.py`, about 150 lines) is the reference; a library such as `go-shiori/go-epub` is the builder's call if it handles embedded fonts and the cover properly.
- Spine: cover page, The Day in Brief, one chapter per topic, Colophon.
- Table of contents entries: "The Day in Brief", "First: Of Java & Quarkus", and so on, "Colophon".
- Internal links: front page entries to their stories.
  All internal links must resolve; a test checks it.
- The reference book still has a "Full Accounts" chapter with whole articles; that part is superseded by the long summaries.
- Fonts embedded with `go:embed` and shipped in the EPUB with their OFL licence texts.
- The EPUB identifier is derived from the date (and the rebuild number, below), never reused across editions.
- Title `paper.title` (default "The Daily Brief") plus the date; the issue number counts the editions delivered so far (stored in the paper's local state; Claude's call).

### The cover

- A raster image (PNG or JPEG) at 1264×1680, the Libra Colour's resolution, used as the EPUB cover image and as the library thumbnail.
- Rendered in Go from a fixed layout with the embedded fonts (`golang.org/x/image/font/opentype` and `image/draw`), so the server needs no browser.
- Content: the masthead block with that day's issue number and date, the first four topic names, "Edition of the Morning" in red, and the editor's lead line; if the editor fails, the first ACT highlight's headline.
  Long lead lines wrap to at most three lines and shrink if needed.
- The spike's cover image is the visual target; a golden-image test guards the layout.

### Delivery to Dropbox

- Config section `paper.dropbox` with the app key (not secret) and the folder (default `/Apps/Rakuten Kobo`).
- `daily-brief dropbox login`: OAuth 2 authorisation code flow with PKCE and `token_access_type=offline`.
  It prints the authorisation URL, listens on `http://localhost:53682/` for the redirect, and also accepts the redirect URL pasted back (for a headless server, as done on 2026-11-25).
  It stores the refresh token (see "Secrets") and never stores the password.
- `daily-brief dropbox status`: refreshes an access token and lists the folder, read-only.
- Each run: refresh a short-lived access token, then
  1. **upload** today's book with `files/upload`, mode `add`, no autorename;
  2. only after the upload succeeded, **delete** every older edition in the folder, with `files/delete_v2`.
- daily-brief only deletes files whose names match its own pattern (`<paper.title> YYYY-MM-DD.epub`, optionally with a rebuild suffix), and never the file it just uploaded (this also covers a same-day rebuild's earlier file).
  Everything else in the folder, such as Kobo's own PDF, is never touched.
  Deleting all older editions, not only yesterday's, means missed days never pile up.
- Never upload a book that is empty or fails validation.
- A second run on the same day without `-refresh` (Claude's call): if today's edition was already delivered, it does nothing and exits 0; if today's book was built but delivery failed, it re-delivers that local book and spends no tokens.
  This covers the `Persistent=true` catch-up after a manual run and a manual retry after a delivery failure.
  The issue number only advances on a delivered edition.
- A same-day rebuild (`-refresh`) uploads under a new name and identifier (`... 2026-11-25 (2).epub`), then deletes the earlier one, because same-name replacement is unreliable on the Kobo.
- Retry transient failures with backoff; on final failure keep the local book and yesterday's file, and exit with the delivery-failed code.
- Delivery sits behind a small interface (deliver a finished book) so the Instapaper fallback could be added without touching the rest.

### Secrets

- **On the home server** (the real setup): the Dropbox refresh token is a systemd encrypted credential, created once with `systemd-creds encrypt` (bound to the server's TPM where available) and loaded by the service with `LoadCredentialEncrypted=dropbox_refresh_token:...`.
  daily-brief reads it from `$CREDENTIALS_DIRECTORY/dropbox_refresh_token`.
- **Fallback** for manual runs: `~/.config/daily-brief/dropbox.token`, mode 0600; daily-brief refuses to use it if group or others can read it.
- Order: the systemd credential if present, otherwise the file.
- `dropbox login` writes the fallback file and prints the `systemd-creds` command to move it into a credential.
- The macOS Keychain is not used: nothing runs on a Mac.

### Scheduling on the home server

- A systemd **system** timer runs the service as Rafael's user: `WakeSystem=true` wakes the server from suspend, `Persistent=true` runs a missed edition at next boot.
- Default time 05:45, configurable in the unit.
- While it runs, the service holds a systemd inhibitor lock (`systemd-inhibit --what=idle:sleep`) so the server does not fall asleep mid-run.
  When it exits, the lock is gone and the server's normal rule applies: sleep after 30 minutes with nothing running.
  The builder checks this matches how the server's idle sleep is actually implemented.
- The `claude-code` provider needs the Claude Code CLI installed and logged in for that user on the server; `daily-brief auth status` is the check.
- The GUIDE documents the unit files and the credential setup; the builder decides whether daily-brief also prints or installs them.

## Rollout

1. Build `daily-brief paper -no-deliver`: budget check, unattended research, article fetching, editor pass, EPUB and cover.
   Testable on the laptop; compare against the spike book on the Kobo.
2. **Margin runs on `claude-code`:** several full runs at the intended budget, recording planned against actual spend per pass, to size the paper's margin so no run crosses the ceiling.
3. Add `dropbox login`, `status` and delivery (upload, then delete older editions).
4. Set up the home server: install daily-brief and the Claude Code CLI, log in, create the Dropbox credential, install the timer, and check wake, run and sleep once overnight.
5. Update `README.md` and `docs/GUIDE.md`, including the Dropbox setup traps and the "Publisher Default" font note.
6. Clean up the tests of 2026-11-24 and 2026-11-25: the Instapaper test article and Instapaper credentials, `~/kobo-spike`, the laptop's `~/.config/daily-brief/dropbox.env` once the server has its own credential.

## Left to the builder

- Package layout (for example `internal/paper`, `internal/extract`, `internal/editor`, `internal/epub`, `internal/deliver`) and names of internal types.
- Exact timeouts, size caps, concurrency and retry counts.
- The editor share formula and the editor prompt wording, within the rules above.
- Hand-written EPUB assembly or a library.
- The cover renderer's details, within the reference image.
- Whether `daily-brief paper schedule` exists or the GUIDE alone covers scheduling.
- Exact exit code numbers.

## Appendix: Instapaper findings (fallback route)

- API v2 (`https://www.instapaper.com/api/2/`) authenticates with a bearer token; an "Owner Only" application's page can generate a personal access token, with no callback URI and no review.
  API v1's xAuth is deprecated (removal announced for 30 September 2027).
- `POST /api/2/bookmarks` with `private_source` and `content` (HTML) saves a private article without a URL; `POST /api/2/bookmarks/{id}/move` with `{"section": "archive"}` archives it; `GET /api/2/me` checks the token.
- Works on a free account; a 400 KB paper was accepted.
- Instapaper's parser keeps headings, bold, italics and lists, but strips ids, internal links and their link text, and all styling.
- Instapaper articles on the Kobo have no table of contents.
