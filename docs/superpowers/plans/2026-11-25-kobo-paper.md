# Kobo Paper Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `daily-brief paper` researches the day unattended, writes long newspaper-style stories with an editor pass, builds an EPUB with its own identity and cover, and delivers it to the Kobo through Dropbox.

**Architecture:** New packages around the existing generator: `fetch` (safe article fetching), `editor` (the newspaper rewrite, its checks and fallbacks), `fonts` + `epub` + `cover` (the book), `dropbox` (API client, login, credentials) and `paper` (orchestration, budget split, local state, delivery rule, report).
`main.go` gains the `paper` and `dropbox` subcommands; `brief` gains a configurable claude-code margin and an optional `Composer` interface for long outputs.

**Tech Stack:** Go 1.26, `github.com/markusmobius/go-trafilatura`, `golang.org/x/image/font/opentype`, `html/template`, `archive/zip`, `net/http`, existing `brief` and `config` packages.

**Spec:** `docs/superpowers/specs/2026-11-24-kobo-paper-design.md` (read it before any task; references in `docs/superpowers/specs/kobo-paper-assets/`).

## Global Constraints

- Single Go binary, no runtime dependencies: no Chrome, Pandoc, Python or Calibre at run time.
- gofmt, `go vet ./...`, `go test -race ./...` pass after every task.
- Tests follow the repo style: table tests, adversarial tests for untrusted input (`*_adversarial_test.go`).
- Never use the em dash character in code, comments, docs or copy.
- Commit messages are plain sentences in the repo's style, with no attribution trailers.
- No secrets in `config.yaml`, logs, journals, test fixtures or the repo; the repo is public, so no copyrighted article text in fixtures beyond a few invented sentences.
- The TUI's behaviour and wording do not change.
- Identity values: paper `#efe6d2`, ink `#2a2318`, green `#2f5a3a`, red `#8a1c1c`; fonts IM Fell English (roman, italic, small caps) and UnifrakturMaguntia, SIL OFL, licence texts shipped.
- Cover image 1264×1680 PNG.
- Dropbox folder default `/Apps/Rakuten Kobo`; file name `<paper.title> YYYY-MM-DD.epub`, a same-day rebuild `<paper.title> YYYY-MM-DD (N).epub`.
- Default paper title `The Daily Brief`.
- Upload first, then delete stale editions; delete only names matching daily-brief's pattern, never the file just uploaded.
- `paper.token_budget` must be positive for `daily-brief paper`; `0` (auto) is refused.
- Overshoot is always recorded in the summary line, the colophon, and a warning on stderr.
- Story lengths: a chapter's lead story with an article 250 to 450 words, other stories with an article 150 to 300, stories without one and "In Brief" notes one paragraph.

## Review Focus

1. A non-ASCII `paper.title` (for example "Le Brief Quotidien · été"): the Dropbox upload must still work (the `Dropbox-API-Arg` header must be ASCII-escaped). Test in Task 9.
2. An editor reply wrapped in ```` ```json ```` fences or preceded by a sentence: the parser must still find the JSON object. Test in Task 5.
3. An article containing the prompt's closing delimiter or instructions ("ignore the rules above"): the delimiter must be neutralised so the article stays quoted material. Test in Task 5.
4. Story text with `&`, `<`, `>` and control characters: the EPUB must still be valid XHTML. Test in Task 7.
5. A morning with no network (research yields no stories): nothing is delivered, yesterday's paper stays, the local state is unchanged, and the exit code is 3. Test in Task 11.

---

### Task 1: Paper config section

**Files:**
- Modify: `internal/config/config.go` (Config struct, `Default`, `Validate`)
- Test: `internal/config/paper_test.go`

**Interfaces:**
- Produces:
  ```go
  type Paper struct {
      TokenBudget int64        `yaml:"token_budget"`
      Title       string       `yaml:"title,omitempty"`
      Dropbox     PaperDropbox `yaml:"dropbox,omitempty"`
  }
  type PaperDropbox struct {
      AppKey string `yaml:"app_key,omitempty"`
      Folder string `yaml:"folder,omitempty"`
  }
  // Config gains: Paper Paper `yaml:"paper,omitempty"`
  func (p Paper) TitleOrDefault() string  // "The Daily Brief" when empty
  func (p Paper) FolderOrDefault() string // "/Apps/Rakuten Kobo" when empty
  ```

- [ ] **Step 1: Write the failing tests**

```go
func TestPaperDefaults(t *testing.T) {
    var p Paper
    if p.TitleOrDefault() != "The Daily Brief" { t.Fatal(p.TitleOrDefault()) }
    if p.FolderOrDefault() != "/Apps/Rakuten Kobo" { t.Fatal(p.FolderOrDefault()) }
}

func TestPaperValidate(t *testing.T) {
    for _, tc := range []struct{ name string; p Paper; ok bool }{
        {"empty is fine", Paper{}, true},
        {"budget", Paper{TokenBudget: 400_000}, true},
        {"negative budget", Paper{TokenBudget: -1}, false},
        {"slash in title", Paper{Title: "a/b"}, false},
        {"control in title", Paper{Title: "a\x07b"}, false},
        {"relative folder", Paper{Dropbox: PaperDropbox{Folder: "Apps/Kobo"}}, false},
        {"absolute folder", Paper{Dropbox: PaperDropbox{Folder: "/Apps/Rakuten Kobo"}}, true},
        {"non-ascii title", Paper{Title: "Le Brief Quotidien · été"}, true},
    } {
        cfg := Default(); cfg.Paper = tc.p
        if err := cfg.Validate(); (err == nil) != tc.ok { t.Errorf("%s: err=%v", tc.name, err) }
    }
}

func TestPaperRoundTrip(t *testing.T) // Save then Load keeps all Paper fields; a config without `paper:` loads with the zero value and saves without a paper key
```

- [ ] **Step 2: Run `go test ./internal/config -run Paper`**, expect FAIL (undefined: Paper).
- [ ] **Step 3: Implement** the types, the two helpers, and the checks in `Validate` (title: no `/`, `\`, control or invisible characters, at most 80 runes; folder: starts with `/` when set).
- [ ] **Step 4: Run `go test ./internal/config`**, expect PASS.
- [ ] **Step 5: Commit** `git add internal/config && git commit -m "Config: a paper section for the Kobo edition"`

### Task 2: Generator margin and the Composer interface

**Files:**
- Modify: `internal/brief/budget.go` (`margin`, `spendable`), `internal/brief/generator.go` (Generator field, `margin`/`spendable` methods), `internal/brief/backend.go`, `internal/brief/api.go`, `internal/brief/claudecode.go`
- Test: `internal/brief/margin_test.go`

**Interfaces:**
- Produces:
  ```go
  func (g *Generator) WithMarginPercent(pct int) *Generator // default 10; affects claude-code only
  // Composer is implemented by the real backends; maxOutput caps the reply's tokens where the provider allows it.
  type Composer interface {
      Compose(ctx context.Context, system, input string, maxOutput int64) Result
  }
  func MaxItems(cfg config.Config) int // sum of itemCaps over effective topics
  func EstimateTokens(s string) int64   // exported wrapper of estimateTokens
  ```

- [ ] **Step 1: Write the failing tests**

```go
func TestMarginPercent(t *testing.T) {
    if got := marginPct("claude-code", 10, 100_000, false); got != 10_000 { t.Fatal(got) }
    if got := marginPct("claude-code", 30, 100_000, false); got != 30_000 { t.Fatal(got) }
    if got := marginPct("claude-code", 30, 100_000, true); got != 30_000+ccSearchCost { t.Fatal(got) }
    if got := marginPct("anthropic", 30, 100_000, true); got != 0 { t.Fatal(got) }
    if got := spendablePct("claude-code", 10, 110_000, 1, false); got != 100_000 { t.Fatal(got) }
    if got := spendablePct("claude-code", 30, 130_000, 1, false); got != 100_000 { t.Fatal(got) }
}
func TestDefaultMarginUnchanged(t *testing.T) // NewGenerator without WithMarginPercent plans exactly as before: reuse an existing generator_test fixture and compare its pass allocations
func TestBackendsCompose(t *testing.T)        // *apiBackend and *claudeCodeBackend satisfy Composer (compile-time assertions)
```

- [ ] **Step 2: Run `go test ./internal/brief -run 'Margin|Compose'`**, expect FAIL.
- [ ] **Step 3: Implement.** Rename the bodies to `marginPct(provider string, pct int, alloc int64, search bool) int64` (alloc*pct/100, plus `ccSearchCost` when searching) and `spendablePct(provider string, pct int, room int64, n int, search bool) int64` (`room*100/(100+pct)` after the search deduction); keep `margin`/`spendable` as `pct=10` wrappers for existing callers.
  `apiBackend.Compose` is `Summarize` with `b.params(maxOutput, system)`; `claudeCodeBackend.Compose` calls `b.run` with planned budget `EstimateTokens(system+input)+maxOutput`.
- [ ] **Step 4: Run `go test -race ./internal/brief`**, expect PASS (all existing tests too).
- [ ] **Step 5: Commit** `git commit -am "Generator: a configurable claude-code margin, and Compose for long replies"`

### Task 3: Safe article fetching

**Files:**
- Create: `internal/fetch/fetch.go`, `internal/fetch/guard.go`
- Test: `internal/fetch/fetch_test.go`, `internal/fetch/fetch_adversarial_test.go`
- Modify: `go.mod`, `go.sum` (add `github.com/markusmobius/go-trafilatura`)

**Interfaces:**
- Produces:
  ```go
  type Options struct {
      Timeout     time.Duration // default 20s
      MaxBytes    int64         // default 5 MiB
      MinWords    int           // default 150
      MaxWords    int           // default 2500; text is cut at a sentence end at or before it
      Concurrency int           // default 4
      UserAgent   string        // default "daily-brief (+https://github.com/RafaelXokito/daily-brief)"
      AllowIP     func(net.IP) bool // default PublicIP; tests inject one that allows 127.0.0.1
  }
  type Article struct { Text string; Words int; Err error }
  var ErrTooShort, ErrNotHTML, ErrBlocked error
  func PublicIP(ip net.IP) bool
  func Articles(ctx context.Context, urls []string, o Options) map[string]Article // each URL fetched once
  ```

- [ ] **Step 1: Write the failing tests** (httptest servers on 127.0.0.1 with `AllowIP` allowing loopback, except where the guard is tested)

```go
func TestPublicIP(t *testing.T) // false for 127.0.0.1, ::1, 10.0.0.1, 172.16.0.1, 192.168.1.1, 169.254.1.1, fe80::1, 100.64.0.1, 0.0.0.0, 224.0.0.1; true for 93.184.216.34 and 2606:4700::1111
func TestArticleExtracted(t *testing.T) // a 400-word article page yields Words >= 300, Text has no "<"
func TestArticleCappedAtMaxWords(t *testing.T) // 4000-word page with MaxWords 2500: Words <= 2500, Text ends with ".", "!" or "?"
func TestTooShort(t *testing.T)        // 80-word page: errors.Is(a.Err, ErrTooShort)
func TestNotHTML(t *testing.T)         // application/pdf: errors.Is(a.Err, ErrNotHTML)
func TestDuplicateURLsFetchedOnce(t *testing.T) // same URL twice: server hit count 1
```
Adversarial:
```go
func TestLoopbackBlockedByDefault(t *testing.T) // default Options against an httptest URL: errors.Is(a.Err, ErrBlocked)
func TestRedirectToLoopbackBlocked(t *testing.T) // allowed server 302s to http://127.0.0.2/: ErrBlocked (AllowIP allows only the first server's port via a dial counter)
func TestNonHTTPSchemes(t *testing.T)  // file:///etc/passwd, gopher://x, ftp://x: ErrBlocked, no request made
func TestOversizedBody(t *testing.T)   // 6 MiB body: error, no panic, memory bounded by MaxBytes
func TestTooManyRedirects(t *testing.T) // 10 chained redirects: error after 5
```

- [ ] **Step 2: Run `go test ./internal/fetch`**, expect FAIL.
- [ ] **Step 3: Implement.** The IP guard lives in the `net.Dialer.Control` hook, so it applies after DNS and on every redirect hop; `CheckRedirect` allows 5 hops and only `http`/`https`.
  Extract with `trafilatura.Extract(io.LimitReader(body, MaxBytes), trafilatura.Options{OriginalURL: u, ExcludeComments: true})`, use `ContentText`.
- [ ] **Step 4: Run `go test -race ./internal/fetch`**, expect PASS.
- [ ] **Step 5: Commit** `git add internal/fetch go.mod go.sum && git commit -m "Fetch: public-only article fetching with trafilatura extraction"`

### Task 4: Editor input and the mechanical fallback

**Files:**
- Create: `internal/editor/model.go`, `internal/editor/input.go`, `internal/editor/fallback.go`
- Test: `internal/editor/input_test.go`, `internal/editor/fallback_test.go`

**Interfaces:**
- Consumes: `brief.Journal`, `brief.Section.Items()`, `brief.Item`, `brief.Highlight`, `fetch.Article`
- Produces:
  ```go
  type ItemIn struct { ID, Headline, Tag, Facts, Prose, Host, URL, Article string }
  type ChapterIn struct { Topic string; Items []ItemIn }
  type Input struct { Date time.Time; Chapters []ChapterIn; Highlights []brief.Highlight }
  type Story struct {
      ID, Headline, Standfirst, Dateline string
      Paragraphs []string
      Placement  string // "lead" | "story" | "brief"
      Host       string
      Fallback   bool
  }
  type Chapter struct { Topic string; Stories []Story }
  type FrontLead struct { ID, Headline string; Paragraphs []string }
  type FrontLine struct { ID, Sentence string }
  type FrontPage struct { Leads []FrontLead; InBrief []FrontLine; CoverLine string }
  type Paper struct { Date time.Time; Front FrontPage; Chapters []Chapter; Fallbacks int; Tokens int64 }

  func BuildInput(j *brief.Journal, articles map[string]fetch.Article) Input
  func FallbackStory(it ItemIn, placement string) Story
  func FallbackFront(in Input) FrontPage // leads: first two "act" highlights (else first two items); in brief: every other item's headline; cover line: first act headline
  ```

- [ ] **Step 1: Write the failing tests**, using a fixture journal built in the test (two done sections, items in the real format: `### headline`, then `` `WATCH` facts · more facts``, then prose ending in `[source](https://example.com/a)`)

```go
func TestBuildInputSplitsItem(t *testing.T) {
    in := BuildInput(fixtureJournal(), map[string]fetch.Article{"https://example.com/a": {Text: "Body.", Words: 400}})
    it := in.Chapters[0].Items[0]
    // ID "t0i0", Tag "WATCH", Facts "facts · more facts", Prose without "[source](...)",
    // Host "example.com", URL "https://example.com/a", Article "Body."
}
func TestBuildInputSkipsNonDoneSections(t *testing.T)
func TestBuildInputFailedArticleIsEmpty(t *testing.T) // Article{Err: fetch.ErrTooShort} gives Article ""
func TestFallbackStory(t *testing.T) {
    s := FallbackStory(ItemIn{ID: "t0i0", Headline: "H", Tag: "ACT", Facts: "a · b", Prose: "One. Two.", Host: "x.org"}, "story")
    // Headline "H", Standfirst "a; b", Paragraphs ["One. Two."], Fallback true, Host "x.org", no "`", no "·" anywhere
}
func TestFallbackFrontUsesActHighlights(t *testing.T)
```

- [ ] **Step 2: Run `go test ./internal/editor`**, expect FAIL.
- [ ] **Step 3: Implement.** Facts are the text after the tag on the item's first body line; prose is the rest with Markdown links reduced to their text and `[source](...)` removed (reuse `brief.PlainText`/`brief.Domain` where they fit).
- [ ] **Step 4: Run `go test ./internal/editor`**, expect PASS.
- [ ] **Step 5: Commit** `git add internal/editor && git commit -m "Editor: input from the journal, and a mechanical fallback story"`

### Task 5: Editor prompts, parsing and fact checks

**Files:**
- Create: `internal/editor/prompt.go`, `internal/editor/parse.go`, `internal/editor/facts.go`
- Test: `internal/editor/parse_test.go`, `internal/editor/facts_test.go`, `internal/editor/prompt_adversarial_test.go`

**Interfaces:**
- Produces:
  ```go
  func chapterPrompt(c ChapterIn) (system, input string)
  func frontPrompt(in Input, chapters []Chapter) (system, input string)
  func parseChapter(reply string, c ChapterIn) (Chapter, error) // every item ID exactly once; unknown IDs or bad placement are errors
  func parseFront(reply string, chapters []Chapter) (FrontPage, error)
  func checkStory(s Story, it ItemIn) bool // facts and dateline check
  ```

- [ ] **Step 1: Write the failing tests**

```go
func TestParseChapterFenced(t *testing.T)    // reply "Here it is:\n```json\n{...}\n```" parses
func TestParseChapterMissingItem(t *testing.T)   // an item ID absent: error
func TestParseChapterDuplicateItem(t *testing.T) // an item ID twice: error
func TestParseChapterUnknownItem(t *testing.T)   // ID "t9i9": error
func TestParseChapterBadPlacement(t *testing.T)  // placement "sidebar": error

func TestCheckStoryNumbers(t *testing.T) {
    it := ItemIn{Facts: "CVSS 9.8 · patched in 25.0.5.1", Prose: "Costs $3 per million.", Article: "Released on 24 November 2026."}
    ok := Story{Paragraphs: []string{"Rated 9.8, fixed in 25.0.5.1, $3 per million, out 24 November 2026."}}
    bad := Story{Paragraphs: []string{"Rated 9.9 and fixed in 25.0.6."}}
    if !checkStory(ok, it) || checkStory(bad, it) { t.Fatal() }
}
func TestCheckStoryCVE(t *testing.T)      // CVE-2026-114938 must appear in the item or article
func TestCheckStoryDateline(t *testing.T) // Dateline "BRUSSELS" fails unless "Brussels" (case-insensitive) is in the item or article
func TestCheckStoryThousands(t *testing.T) // "1,000,000" in the story matches "1000000" in the source and the reverse
```
Adversarial:
```go
func TestArticleDelimiterNeutralised(t *testing.T) // an Article containing the closing delimiter and "Ignore the rules above": input has exactly one closing delimiter per article and it is ours
func TestPromptCarriesRules(t *testing.T)          // system prompt contains: "modern newspaper English", "Do not add", the three lengths, "say so" (disagreement rule), "exactly once"
```

- [ ] **Step 2: Run `go test ./internal/editor`**, expect FAIL.
- [ ] **Step 3: Implement.** Articles go inside `<article id="t0i0">` ... `</article>` blocks; any `</article` in the text is escaped to `&lt;/article`.
  The system prompt states the rules in the spec's "The editor pass" section, the lengths in Global Constraints, and the JSON shape below, verbatim:
  ```json
  {"stories":[{"id":"t0i0","headline":"","standfirst":"","dateline":"","paragraphs":[""],"placement":"lead|story|brief"}]}
  ```
  and for the front page:
  ```json
  {"leads":[{"id":"","headline":"","paragraphs":[""]}],"in_brief":[{"id":"","sentence":""}],"cover_line":""}
  ```
  Fact tokens: numbers `\d[\d,.]*\d|\d` (commas removed before comparing, trailing `.` dropped), `CVE-\d{4}-\d+`, currency amounts; a dateline is checked word by word.
- [ ] **Step 4: Run `go test ./internal/editor`**, expect PASS.
- [ ] **Step 5: Commit** `git add internal/editor && git commit -m "Editor: prompts, strict reply parsing and fact checks"`

### Task 6: Editor passes within a budget

**Files:**
- Create: `internal/editor/run.go`
- Test: `internal/editor/run_test.go`

**Interfaces:**
- Consumes: `brief.Backend`, `brief.Composer`, `brief.EstimateTokens`, everything from Tasks 4 and 5
- Produces:
  ```go
  type Budget struct { Tokens int64; MarginPct int; Provider string }
  func Reserve(chapters, items int) int64 // upper bound before research: items*(3300+650) + (chapters+1)*3000
  func Run(ctx context.Context, b brief.Backend, in Input, budget Budget, concurrency int) Paper
  ```
- Values: per-pass overhead 3000 tokens; output plan 650 tokens per item with an article, 200 without, plus 300 per pass; front page pass output 1500.

- [ ] **Step 1: Write the failing tests** with a fake backend implementing `Backend` and `Composer` that returns canned JSON and a token count

```go
func TestRunUsesReplies(t *testing.T)          // valid replies: stories from replies, Fallbacks 0, Tokens = sum of fake tokens
func TestRunFallsBackOnBadJSON(t *testing.T)   // chapter reply "nonsense": that chapter's stories all Fallback, Fallbacks = its item count, other chapters unaffected
func TestRunFallsBackOnFailedFactCheck(t *testing.T) // one story adds "9.9": only that story is FallbackStory
func TestRunSkipsPassThatDoesNotFit(t *testing.T) // Budget.Tokens too small for chapter 2's planned cost plus margin: no call made for it, its stories fall back
func TestRunNeverStartsPastBudget(t *testing.T) // fake reports more tokens than planned: later passes that no longer fit are not called
func TestRunPrefersCompose(t *testing.T)       // backend with Compose: Compose called with maxOutput from the output plan; without it Summarize is used
func TestReserve(t *testing.T)                 // Reserve(8, 30) == 30*(3300+650) + 9*3000
```

- [ ] **Step 2: Run `go test ./internal/editor -run Run`**, expect FAIL.
- [ ] **Step 3: Implement.** Chapter passes run concurrently (up to `concurrency`), each claims `planned + planned*MarginPct/100` from a mutex-guarded remainder before calling and settles to the reported tokens after; the front-page pass runs last, from the edited chapters; if it cannot run or fails, `FallbackFront`.
- [ ] **Step 4: Run `go test -race ./internal/editor`**, expect PASS.
- [ ] **Step 5: Commit** `git add internal/editor && git commit -m "Editor: chapter and front-page passes within a reserved budget"`

### Task 7: Fonts, EPUB assembly and the paper's pages

**Files:**
- Create: `internal/fonts/fonts.go`, `internal/fonts/*.ttf` (IMFeENrm28P, IMFeENit28P, IMFeENsc28P, UnifrakturMaguntia-Book), `internal/fonts/OFL-IMFellEnglish.txt`, `internal/fonts/OFL-UnifrakturMaguntia.txt` (from github.com/google/fonts `ofl/imfellenglish`, `ofl/imfellenglishsc`, `ofl/unifrakturmaguntia`)
- Create: `internal/epub/epub.go`, `internal/epub/paper.css` (port of `kobo-paper-assets/paper2.css`)
- Create: `internal/paper/render.go`, `internal/paper/templates/*.xhtml`
- Test: `internal/epub/epub_test.go`, `internal/paper/render_test.go`, `internal/paper/render_adversarial_test.go`

**Interfaces:**
- Produces:
  ```go
  // package fonts
  var Files embed.FS // the four TTFs and the OFL texts
  func Face(name string, size float64) (font.Face, error) // names: "fell", "fell-italic", "fell-sc", "fraktur"

  // package epub
  type Chapter struct { File, TOCTitle, Body string } // Body: XHTML inside <body>
  type Book struct { Title, Identifier, Language string; Date time.Time; Cover []byte; Chapters []Chapter }
  func Build(b Book) ([]byte, error)

  // package paper
  type Meta struct { Title string; Issue int; Model string; Spent, Budget, Over int64; Summarised, Failed int; Cut []string }
  func Render(p editor.Paper, m Meta) []epub.Chapter // front.xhtml, ch1.xhtml... , colophon.xhtml
  ```

- [ ] **Step 1: Write the failing tests**

```go
func TestBuildLayout(t *testing.T) // zip: first entry "mimetype", stored, content "application/epub+zip"; has META-INF/container.xml, EPUB/content.opf, EPUB/nav.xhtml, EPUB/toc.ncx, EPUB/cover.png, all four fonts and both OFL texts
func TestBuildMetadata(t *testing.T) // OPF has dc:identifier == Book.Identifier, dc:title, cover-image property on cover.png, spine order cover then chapters
func TestBuildXHTMLValid(t *testing.T) // every .xhtml parses with encoding/xml
func TestRenderStructure(t *testing.T) // TOC titles "The Day in Brief", "First: Of Java & Quarkus", ..., "Colophon"; chapter body has "Chapter the First", h1 "Of Java &amp; Quarkus", one ⁂ between consecutive stories, "From example.com" per story
func TestRenderLinksResolve(t *testing.T) // every href="x.xhtml#id" in the rendered chapters points at an existing id
func TestRenderColophonOvershoot(t *testing.T) // Meta{Spent: 412000, Budget: 400000, Over: 12000}: colophon text contains "over by 12k"
func TestRenderActLabel(t *testing.T) // a story placed on the front page from an ACT item shows "Act now" with class "tag act"
```
Adversarial:
```go
func TestRenderEscapes(t *testing.T) // headline "A & B <script>" plus "\x07" and U+202E: output parses as XML, contains "A &amp; B &lt;script&gt;", no control or bidi characters
```

- [ ] **Step 2: Run `go test ./internal/epub ./internal/paper`**, expect FAIL.
- [ ] **Step 3: Implement.** `Build` follows `kobo-paper-assets/build2.py` (container, OPF with a nav and an NCX, cover page, `mimetype` stored first); `Render` uses `html/template` with the chapter style of the spike (`Chapter the <Ordinal>`, italic `Of <Topic>` in title case, fleuron, drop cap on a lead story's first paragraph, small-caps headlines, italic standfirst, "In Brief" heading before brief stories).
  Ordinals up to "Twelfth"; beyond that, "Chapter 13".
- [ ] **Step 4: Run `go test -race ./internal/epub ./internal/paper`**, expect PASS.
- [ ] **Step 5: Commit** `git add internal/fonts internal/epub internal/paper && git commit -m "Paper: embedded fonts, EPUB assembly and the newspaper pages"`

### Task 8: The cover

**Files:**
- Create: `internal/cover/cover.go`
- Test: `internal/cover/cover_test.go`, `internal/cover/testdata/golden.png`
- Modify: `go.mod`, `go.sum` (`golang.org/x/image`)

**Interfaces:**
- Consumes: `fonts.Face`
- Produces:
  ```go
  type Content struct { Issue int; Date time.Time; Topics []string; Lead string }
  func Render(c Content) ([]byte, error) // PNG, 1264x1680
  func fitLead(text string, width int) (size float64, lines []string) // 66px italic, at most 3 lines, shrinking by 4px down to 44px; past that, the last line ends with "..."
  ```
- Layout (from `kobo-paper-assets/cover.tmpl.html`, which is the source of truth for anything not listed): outer padding 64; frame border 8 plus an inner line of 4 at offset 28; inner padding 96 top and 72 sides; "Being a Record of the Day" small caps 46px; ornaments ❦ and ⁂ 90px green; meta lines small caps 40px; double rule 12px; nameplate fraktur 178px on two lines; edition line red; the year in roman numerals at the bottom.
  If a font lacks the ❦ or ⁂ glyph, draw it from another embedded font that has it, else as a vector shape; never leave a blank.

- [ ] **Step 1: Write the failing tests**

```go
func TestRenderSize(t *testing.T)      // decoded PNG bounds 1264x1680, background pixel at (10,10) == #efe6d2
func TestRenderGolden(t *testing.T)    // Content{Issue: 323, Date: 2026-11-19, Topics: [Java Security AI Tech], Lead: "Apache Roller RCE exploited in the wild"} differs from testdata/golden.png in under 0.5% of pixels; `-update` rewrites the golden
func TestFitLeadShort(t *testing.T)    // "Short" -> 66, 1 line
func TestFitLeadWraps(t *testing.T)    // a 20-word lead -> at most 3 lines, size <= 66
func TestFitLeadTruncates(t *testing.T) // a 200-word lead -> 3 lines at 44, last ends "..."
```

- [ ] **Step 2: Run `go test ./internal/cover`**, expect FAIL.
- [ ] **Step 3: Implement** with `opentype` faces and `image/draw`; generate the golden once with `-update` and check it by eye against `kobo-paper-assets/cover-reference.png` before committing it.
- [ ] **Step 4: Run `go test -race ./internal/cover`**, expect PASS.
- [ ] **Step 5: Commit** `git add internal/cover go.mod go.sum && git commit -m "Cover: the masthead title page rendered in Go"`

### Task 9: Dropbox client and credentials

**Files:**
- Create: `internal/dropbox/client.go`, `internal/dropbox/login.go`, `internal/dropbox/creds.go`
- Test: `internal/dropbox/client_test.go`, `internal/dropbox/login_test.go`, `internal/dropbox/creds_test.go`

**Interfaces:**
- Produces:
  ```go
  type Client struct {
      AppKey, RefreshToken string
      HTTP *http.Client
      TokenURL, APIBase, ContentBase string // defaults https://api.dropboxapi.com/oauth2/token, https://api.dropboxapi.com/2, https://content.dropboxapi.com/2
  }
  func (c *Client) Upload(ctx context.Context, path string, data []byte) error // mode "add", autorename false, mute true
  func (c *Client) List(ctx context.Context, folder string) ([]string, error)  // file names; follows has_more via list_folder/continue
  func (c *Client) Delete(ctx context.Context, path string) error              // files/delete_v2
  var ErrConflict error // upload target exists (HTTP 409, path/conflict)

  func NewPKCE() (verifier, challenge string)
  func AuthURL(appKey, challenge, redirect string) string // response_type=code, token_access_type=offline, code_challenge_method=S256, redirect_uri
  func ParseRedirect(pasted string) (string, error)       // full redirect URL or bare code; error on "error=" redirects
  func ExchangeCode(ctx context.Context, h *http.Client, tokenURL, appKey, code, verifier, redirect string) (refresh string, err error)
  func Listen(ctx context.Context, addr string) (code string, err error) // one request on addr (default "localhost:53682"), replies with a short "you can close this tab" page

  const CredentialName = "dropbox_refresh_token"
  func LoadRefreshToken(credDir, fallback string) (string, error) // credDir/$CREDENTIALS_DIRECTORY first, then fallback file; refuses a fallback readable by group or others
  func SaveRefreshToken(path, token string) error                // 0700 dir, 0600 file, atomic write
  func DefaultTokenPath() string                                  // ~/.config/daily-brief/dropbox.token
  ```

- [ ] **Step 1: Write the failing tests** (httptest server standing in for all three Dropbox hosts)

```go
func TestUploadRequest(t *testing.T) // refresh call first (grant_type=refresh_token, client_id); upload: Bearer header, Dropbox-API-Arg {"path":"/Apps/Rakuten Kobo/The Daily Brief 2026-11-25.epub","mode":"add","autorename":false,"mute":true}, body bytes equal
func TestUploadNonASCIIName(t *testing.T) // name "Le Brief Quotidien · été 2026-11-25.epub": header is pure ASCII with \u escapes and decodes back to the name
func TestUploadConflict(t *testing.T) // 409 {"error_summary":"path/conflict/file/.."}: errors.Is(err, ErrConflict)
func TestListPaging(t *testing.T)     // two pages via has_more/cursor: all names returned, folders skipped
func TestDeleteRequest(t *testing.T)  // delete_v2 body {"path": ...}
func TestTokenCachedPerClient(t *testing.T) // two calls on one Client: one refresh request
func TestErrorsCarryNoToken(t *testing.T)   // a 401 error string contains neither the refresh nor the access token
func TestRetriesTransient(t *testing.T)     // 503 then 200: one retry, success; 429 with Retry-After 1: waits, retries; 3 consecutive 503s: error; 400: no retry
func TestPKCE(t *testing.T)           // challenge == base64url(sha256(verifier)) without padding; verifier 43..128 chars
func TestParseRedirect(t *testing.T)  // "http://localhost:53682/?code=abc" -> "abc"; "abc" -> "abc"; "...?error=access_denied" -> error
func TestLoadPrefersCredential(t *testing.T) // both present: credential value
func TestLoadRefusesOpenFile(t *testing.T)   // fallback mode 0644: error naming the path and "chmod 600"
func TestSaveRefreshToken(t *testing.T)      // file 0600, dir 0700
```

- [ ] **Step 2: Run `go test ./internal/dropbox`**, expect FAIL.
- [ ] **Step 3: Implement** with `net/http` only; every call retries 429 and 5xx up to 3 attempts with exponential backoff from 1s (honouring `Retry-After`); the `Dropbox-API-Arg` value is JSON with every rune above 0x7E written as `\uXXXX` (surrogate pairs above the BMP).
- [ ] **Step 4: Run `go test -race ./internal/dropbox`**, expect PASS.
- [ ] **Step 5: Commit** `git add internal/dropbox && git commit -m "Dropbox: client, PKCE login helpers and credential loading"`

### Task 10: Local state, the delivery rule and the budget split

**Files:**
- Create: `internal/paper/state.go`, `internal/paper/deliver.go`, `internal/paper/budget.go`
- Test: `internal/paper/state_test.go`, `internal/paper/deliver_test.go`, `internal/paper/budget_test.go`

**Interfaces:**
- Consumes: `dropbox.Client`, `editor.Reserve`, `brief.MaxItems`
- Produces:
  ```go
  type State struct {
      Issue     int    `json:"issue"`      // editions delivered so far
      Date      string `json:"date"`       // last edition built, YYYY-MM-DD
      File      string `json:"file"`       // its file name
      Rebuild   int    `json:"rebuild"`    // 0 for the first build of Date
      Delivered bool   `json:"delivered"`
  }
  func LoadState(dir string) (State, error) // missing file: zero State
  func SaveState(dir string, s State) error // atomic
  type Action int
  const ( Build Action = iota; Skip; Redeliver; RebuildToday )
  func Decide(s State, today string, refresh, localExists bool) Action
  func FileName(title, date string, rebuild int) string // "T 2026-11-25.epub", rebuild 2 -> "T 2026-11-25 (2).epub"
  func Stale(names []string, title, keep string) []string

  type Deliverer interface { Deliver(ctx context.Context, name string, data []byte) error }
  func NewDropboxDeliverer(c *dropbox.Client, folder, title string) Deliverer // upload, then delete Stale(List(folder))

  const MarginPercent = 30 // claude-code margin for paper runs; resized in rollout step 2
  type Split struct { Research, Editor int64 }
  func SplitBudget(total int64, cfg config.Config) (Split, error) // Editor = editor.Reserve(chapters, brief.MaxItems(cfg)); error unless Research >= total/2
  ```

- [ ] **Step 1: Write the failing tests**

```go
func TestDecide(t *testing.T) {
    for _, tc := range []struct{ s State; refresh, local bool; want Action }{
        {State{}, false, false, Build},
        {State{Date: "2026-11-24", Delivered: true}, false, false, Build},
        {State{Date: "2026-11-25", Delivered: true}, false, true, Skip},
        {State{Date: "2026-11-25", Delivered: false}, false, true, Redeliver},
        {State{Date: "2026-11-25", Delivered: false}, false, false, Build},
        {State{Date: "2026-11-25", Delivered: true}, true, true, RebuildToday},
    } { /* Decide(tc.s, "2026-11-25", tc.refresh, tc.local) == tc.want */ }
}
func TestStale(t *testing.T) {
    names := []string{"How to add books to your eReader using Dropbox.pdf", "The Daily Brief 2026-11-24.epub",
        "The Daily Brief 2026-11-25.epub", "The Daily Brief 2026-11-25 (2).epub", "The Daily Brief notes.epub", "My Book.epub"}
    got := Stale(names, "The Daily Brief", "The Daily Brief 2026-11-25 (2).epub")
    // == ["The Daily Brief 2026-11-24.epub", "The Daily Brief 2026-11-25.epub"]
}
func TestStaleRegexTitle(t *testing.T) // title "A+B (x)" only matches literally
func TestDelivererUploadsThenDeletes(t *testing.T) // fake dropbox server: upload request precedes delete requests; a failed upload makes no delete
func TestDelivererDeleteFailureIsNotFatal(t *testing.T) // upload ok, delete 500: Deliver returns nil and the error is reported as a warning
func TestSplitBudget(t *testing.T) // total 400k with a config of 8 topics and MaxItems 30: Editor == editor.Reserve(9, 30) (general on adds World); a total below twice the reserve: error mentioning the smallest budget that works
func TestStateRoundTrip(t *testing.T)
```

- [ ] **Step 2: Run `go test ./internal/paper -run 'Decide|Stale|Deliverer|Split|State'`**, expect FAIL.
- [ ] **Step 3: Implement.** The deliverer reports a failed delete through a `Warnings []string` field on its result value instead of failing the edition: the new paper is already on Dropbox.
- [ ] **Step 4: Run `go test -race ./internal/paper`**, expect PASS.
- [ ] **Step 5: Commit** `git add internal/paper && git commit -m "Paper: local state, the delivery rule and the budget split"`

### Task 11: The paper run

**Files:**
- Create: `internal/paper/run.go`, `internal/paper/report.go`
- Test: `internal/paper/run_test.go`

**Interfaces:**
- Consumes: Tasks 2 to 10, `brief.NewGenerator`, `brief.Generator.Run`, `brief.SaveJournal`, `brief.LoadJournal`, `brief.DataDir`
- Produces:
  ```go
  type Options struct { Budget int64; Refresh, NoDeliver bool; Log io.Writer }
  type Deps struct {
      Backend   brief.Backend
      Fetch     func(context.Context, []string) map[string]fetch.Article
      Deliverer Deliverer // nil with NoDeliver
      Now       func() time.Time
      Dir       string    // papers directory, default brief.DataDir()/papers
  }
  type Report struct {
      Date string; Spent, Budget, Over int64; Stories, Summarised, Failed, Fallbacks int
      File string; Delivered bool; Action Action; Warnings []string
  }
  func (r Report) Line() string
  var ErrNoBudget, ErrNoStories, ErrDelivery error
  func ExitCode(err error) int // nil 0, ErrNoBudget 2, ErrNoStories 3, ErrDelivery 4, other 1
  func Run(ctx context.Context, cfg config.Config, o Options, d Deps) (Report, error)
  ```
- Report line format, exact: `paper 2026-11-25: 312k of 400k tokens, 41 stories, 28 of 39 sources summarised, 2 editor fallbacks, delivered as The Daily Brief 2026-11-25.epub`; with overshoot the token part reads `412k of 400k tokens (over by 12k)`; with `-no-deliver` the end reads `saved to <path>`.

- [ ] **Step 1: Write the failing tests** (fake backend returning a fixed research digest and editor JSON, fake fetch, fake deliverer, temp `Dir` and `XDG_DATA_HOME`)

```go
func TestRunNoBudget(t *testing.T)      // cfg.Paper.TokenBudget 0 and Options.Budget 0: ErrNoBudget, ExitCode 2, backend never called
func TestRunHappyPath(t *testing.T)     // book saved in Dir, Deliverer got its bytes and name "The Daily Brief 2026-11-25.epub", State{Issue:1, Delivered:true}, Report.Line matches the format
func TestRunReusesJournal(t *testing.T) // a saved journal for today: no research call, editor still runs
func TestRunNoStories(t *testing.T)     // research produces no items (Review Focus 5): ErrNoStories, ExitCode 3, Deliverer not called, state file unchanged
func TestRunDeliveryFails(t *testing.T) // Deliverer errors: ErrDelivery, ExitCode 4, book kept, State{Delivered:false, Issue unchanged}; a second Run redelivers without calling the backend
func TestRunSkipWhenDelivered(t *testing.T) // second Run the same day: Action Skip, nil error, nothing called
func TestRunRefreshRebuilds(t *testing.T)   // Refresh after delivery: name "The Daily Brief 2026-11-25 (2).epub", a different identifier, State.Rebuild 2; Issue advances by one on its delivery like any edition
func TestRunOvershootReported(t *testing.T) // fake backend reports more tokens than allocated: Report.Over > 0, Line contains "over by", Log contains "warning:"
func TestRunUsesPaperMargin(t *testing.T)   // the generator is built WithMarginPercent(MarginPercent)
```

- [ ] **Step 2: Run `go test ./internal/paper -run Run`**, expect FAIL.
- [ ] **Step 3: Implement** the steps of the spec's "The command": budget check and split; `Decide`; research with `cfg.TokenBudget = split.Research` and `WithMarginPercent(MarginPercent)` (reusing today's journal unless refreshing); fetch item URLs; `editor.Run` with `Budget{split.Editor + unspent research, MarginPercent, provider}`; cover and `Render`; `epub.Build` with identifier `urn:uuid:` + UUIDv5 of `daily-brief:<date>:<rebuild>`; save; deliver; save state; report.
- [ ] **Step 4: Run `go test -race ./internal/paper`**, expect PASS.
- [ ] **Step 5: Commit** `git add internal/paper && git commit -m "Paper: the unattended edition run and its report"`

### Task 12: Commands

**Files:**
- Modify: `main.go` (usage text, `-no-deliver` flag, `paper` and `dropbox` dispatch, `budgetCommand`)
- Create: `paper_cmd.go` (package main)
- Test: `main_paper_test.go`

**Interfaces:**
- Consumes: `paper.Run`, `paper.ExitCode`, `paper.SplitBudget`, `dropbox.*`
- Usage lines, exact:
  ```
    daily-brief paper            research, write and deliver today's Kobo paper (see -no-deliver)
    daily-brief dropbox login    connect Dropbox for paper delivery
    daily-brief dropbox status   check the Dropbox connection (read-only)
  ```
- Flag: `-no-deliver` "build today's paper without delivering it".

- [ ] **Step 1: Write the failing tests**

```go
func TestUsageListsPaper(t *testing.T)        // usage contains the three lines above
func TestPaperCommandNoBudget(t *testing.T)   // run with a temp config lacking paper.token_budget: exit code 2, stderr names "paper.token_budget"
func TestPaperCommandNoDropbox(t *testing.T)  // budget set, no app key, without -no-deliver: error tells to run `daily-brief dropbox login`
func TestDropboxStatusNoToken(t *testing.T)   // no credential and no file: error tells to run `daily-brief dropbox login`
func TestBudgetShowsPaperSplit(t *testing.T)  // paper.token_budget 400000: `daily-brief budget` output has "Paper: 400k" with its research and editor shares
```

- [ ] **Step 2: Run `go test . -run 'Paper|Dropbox|Usage|BudgetShows'`**, expect FAIL.
- [ ] **Step 3: Implement.** `paper` exits with `paper.ExitCode(err)`; progress lines go to stderr, the report line to stdout.
  `dropbox login`: needs `paper.dropbox.app_key`; prints the `AuthURL` with redirect `http://localhost:53682/`, runs `Listen` and a stdin prompt for a pasted redirect URL concurrently and takes whichever arrives first, exchanges the code, saves to `DefaultTokenPath()`, then prints the `systemd-creds encrypt` command for the server.
  `dropbox status`: loads the token, lists the folder, prints the number of files and the daily-brief editions among them.
- [ ] **Step 4: Run `go test -race ./...`**, expect PASS.
- [ ] **Step 5: Commit** `git add main.go paper_cmd.go main_paper_test.go && git commit -m "Commands: daily-brief paper and dropbox login/status"`

### Task 13: Documentation and server setup

**Files:**
- Modify: `README.md` (a "Kobo paper" section under Quick start, pointing to the GUIDE), `docs/GUIDE.md` (new "## The Kobo paper" section after "## Usage")

The GUIDE section covers, in this order: what the paper is; the `paper:` config block; creating the Dropbox app (Scoped access, Full Dropbox, only `files.metadata.read`, `files.content.read`, `files.content.write`, no team scopes, redirect URI `http://localhost:53682/`) and the three traps from the spec's Constraints; `daily-brief dropbox login` and `status`; "Publisher Default" on the Kobo; budget and overshoot; the server setup below; what each exit code means.

Server units, verbatim (with `<user>` and paths as placeholders):

```ini
# /etc/systemd/system/daily-brief-paper.service
[Unit]
Description=daily-brief morning paper for the Kobo
Wants=network-online.target
After=network-online.target

[Service]
Type=oneshot
User=<user>
LoadCredentialEncrypted=dropbox_refresh_token:/etc/credstore.encrypted/dropbox_refresh_token
ExecStart=/usr/bin/systemd-inhibit --what=idle:sleep --why="daily-brief paper" /home/<user>/.local/bin/daily-brief paper
```

```ini
# /etc/systemd/system/daily-brief-paper.timer
[Timer]
OnCalendar=*-*-* 05:45
WakeSystem=true
Persistent=true

[Install]
WantedBy=timers.target
```

```sh
sudo systemd-creds encrypt --name=dropbox_refresh_token ~/.config/daily-brief/dropbox.token /etc/credstore.encrypted/dropbox_refresh_token
shred -u ~/.config/daily-brief/dropbox.token
sudo systemctl daemon-reload && sudo systemctl enable --now daily-brief-paper.timer
```

- [ ] **Step 1: Write the section and the README pointer.**
- [ ] **Step 2: Verify** `go test ./...` still passes (main tests read the usage text) and that no em dash appears: `grep -c "—" README.md docs/GUIDE.md` prints `0` for both.
- [ ] **Step 3: Commit** `git add README.md docs/GUIDE.md && git commit -m "Docs: the Kobo paper, Dropbox setup and the home server timer"`

---

## After the plan (rollout, by hand with Rafael)

These follow the spec's Rollout and are not code tasks:

1. Build `daily-brief paper -no-deliver` on the laptop with the 2026-11-19-like config; open the EPUB on the Kobo next to the reference book and compare.
2. Margin runs on `claude-code`: several full runs at the intended budget; compare planned and actual spend per pass in the journal; set `paper.MarginPercent` so none crossed the ceiling, with the reasoning in a comment.
3. Server: install daily-brief and the Claude Code CLI, log in, run `dropbox login` there, encrypt the credential, install the timer; check one overnight wake, run and sleep, including that `systemd-inhibit --what=sleep` is permitted for that user and matches how the server's 30-minute idle sleep is implemented.
4. Cleanup: the Instapaper test article and `~/.config/daily-brief/instapaper.env`, `~/kobo-spike`, and the laptop's `~/.config/daily-brief/dropbox.env` once the server has its own credential.
