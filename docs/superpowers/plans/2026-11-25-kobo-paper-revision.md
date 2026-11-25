# Kobo Paper Revision Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Apply the four changes Rafael asked for after the first real edition: work topics off the front page, source links, a reading flow for The Day in Brief, and one small lead image per chapter.

**Architecture:** Config gains a per-topic `front_page` flag; the editor carries it and story URLs; the paper renderer gets a new front page layout and source lines; fetch learns preview image URLs and a guarded image download; a small image package resizes to JPEG; epub carries image resources.

**Tech Stack:** Go 1.26, existing packages, `golang.org/x/image/draw` and `golang.org/x/image/webp` (already in go.mod as golang.org/x/image).

**Spec:** `docs/superpowers/specs/2026-11-24-kobo-paper-design.md`, section "Revision 2026-11-25: after the first real edition".

## Global Constraints

- gofmt, `go vet ./...`, `go test -race ./...` pass after every task; TUI behaviour and wording unchanged.
- Never use the em dash character in code, comments, docs or copy. Commit messages plain, no attribution trailers.
- Test fixtures invented text; tiny generated images only (no real photos in the repo).
- One `[source]` link per story; no images on the front page; at most one image per chapter (its lead story); each image at most 800 px wide, JPEG, target about 60 KB and never above 90 KB; total images per book at most 1 MB.
- Images are fetched with the same public-only guard as articles (internal/fetch), with a byte cap and a pixel-dimension cap checked before full decode (decompression bombs).
- Default for `front_page` is true; existing configs behave as before.

## Review Focus

1. A topic with `front_page: false` whose item is the day's only ACT highlight: the front page must not show it, and must still have leads (from other topics, else fallback). Test in Task 1.
2. An item URL with characters that need escaping in an href (`&`, quotes, spaces): the `[source]` link must stay valid XHTML and resolve. Test in Task 1.
3. A preview image that is huge in pixels (e.g. 20000x20000 PNG) but small in bytes: refused before decoding. Test in Task 2.
4. An image URL pointing to a private address or redirecting there: refused (ErrBlocked). Test in Task 2.
5. Images that would push the book over 1 MB: the later chapters simply get no image. Test in Task 2.

---

### Task 1: Front page without work topics, source links, The Day in Brief layout

**Files:**
- Modify: `internal/config/config.go` (Topic gains `FrontPage *bool \`yaml:"front_page,omitempty"\`` and `func (t Topic) OnFrontPage() bool` returning true when nil)
- Modify: `internal/editor/model.go`, `input.go`, `prompt.go`, `parse.go`, `fallback.go`, `facts.go`, `run.go` as needed
- Modify: `internal/paper/render.go`, `internal/paper/templates/front.xhtml`, `chapter.xhtml`, `internal/epub/paper.css`, `internal/paper/run.go`
- Modify: `docs/GUIDE.md` (document `front_page` and the source links)
- Test: `internal/config/*_test.go`, `internal/editor/*_test.go`, `internal/paper/render_test.go`, `render_adversarial_test.go`

**Interfaces:**
- Produces:
  ```go
  // editor
  type ChapterIn struct { Topic string; Items []ItemIn; FrontPage bool } // FrontPage false keeps the chapter's stories off the front page
  type Story struct { /* existing fields */ ; URL string } // set from ItemIn.URL by parseChapter and FallbackStory, never from the model
  func BuildInput(j *brief.Journal, articles map[string]fetch.Article, onFront func(topic string) bool) Input // nil onFront means every topic is on the front page
  ```
- Front page rules: frontPrompt only offers stories from chapters with FrontPage true; parseFront and checkFront reject IDs from other chapters; FallbackFront ignores highlights and items from those chapters; highlights from those topics are not given to the front pass.
- paper.Run passes `cfg` topics: `onFront(topic)` true unless the matching config topic has `front_page: false` (World is always on).

- [ ] **Step 1: Write the failing tests**
  - `TestTopicOnFrontPageDefault`: nil FrontPage gives true; `front_page: false` round-trips through Load/Save.
  - `TestFrontPageExcludesChapter`: a chapter with FrontPage false, holding the only ACT highlight: FallbackFront has no lead or in-brief entry from it and still has leads from other chapters; parseFront rejects a reply that uses its ID.
  - `TestStoryURLFromItem`: parseChapter and FallbackStory set Story.URL from the item, ignoring any "url" the model might add (the strict decoder already rejects unknown keys; assert the error).
  - `TestRenderSourceLine`: each story ends with `<p class="source"><a href="https://example.com/a?x=1&amp;y=2">[source]</a> From example.com</p>`; a story with no URL shows only "From <site>" (or nothing if no host).
  - `TestRenderFrontLayout`: leads separated by ⁂ with a "continued in Of <Topic>" link to `chN.xhtml#id`; an "In Brief" heading; in-brief lines grouped under one small-caps heading per topic in chapter order with a rule element between groups; every link resolves.
  - Adversarial `TestRenderSourceURLEscaping`: URL with `&`, `"` and a space renders as valid XML and a correct href; a non-http(s) URL (`javascript:alert(1)`) gets no link.
- [ ] **Step 2: Run them, expect FAIL.**
- [ ] **Step 3: Implement.** Only http(s) URLs become links (reuse `brief.SafeWebURL`). CSS: `.source` small italic right-aligned, `.topic-head` small caps green, `hr.topic-rule` thin ink rule, `.continued` italic.
- [ ] **Step 4: Run `go test -race ./...`, expect PASS.**
- [ ] **Step 5: Commit** `git commit -m "Paper: keep chosen topics off the front page, link each story to its source, and give The Day in Brief a reading flow"`

### Task 2: One small lead image per chapter

**Files:**
- Modify: `internal/fetch/fetch.go` (Article gains `Image string`: the preview image URL from the extractor's metadata, else og:image, made absolute and only http(s))
- Create: `internal/fetch/image.go` (`func FetchImage(ctx context.Context, url string, o Options) ([]byte, string, error)`: guarded GET, Content-Type image/jpeg|png|gif|webp, body cap 5 MiB, returns bytes and content type)
- Create: `internal/picture/picture.go` (`func Prepare(data []byte) ([]byte, error)`: DecodeConfig first and refuse above 8000x8000 or 40 megapixels; decode jpeg/png/gif/webp; scale to at most 800 px wide keeping aspect (CatmullRom, never upscale); encode JPEG starting at quality 70, lowering in steps of 10 down to 40 until at most 90 KB, else error)
- Modify: `internal/epub/epub.go` (Book gains `Images []Image{Name string; Data []byte}`; manifest items `img/<name>` with media-type image/jpeg; names validated like chapter files)
- Modify: `internal/editor/model.go` (Story gains `Image string`: the book-internal image file name, set by paper.Run, never by the model), `internal/paper/render.go` and `templates/chapter.xhtml` (an `<img>` with alt = headline after the lead story's headline, only when Image is set; never on the front page), `internal/paper/run.go` (after editing: for each chapter's lead story whose item article has an Image URL, FetchImage + Prepare, keep while the running total stays at most 1 MiB, name `chN.jpg`; failures are skipped silently but counted in the colophon as "pictures: X of Y")
- Modify: `docs/GUIDE.md` (one line on pictures)
- Test: `internal/fetch/image_test.go`, `internal/picture/picture_test.go`, `internal/epub/epub_test.go`, `internal/paper/run_test.go`, `render_test.go`

**Interfaces:**
- Consumes: Task 1's Story.URL and layout; fetch's guard.
- Produces: `fetch.Article.Image`, `fetch.FetchImage`, `picture.Prepare`, `epub.Image`, `editor.Story.Image`.

- [ ] **Step 1: Write the failing tests**
  - `TestArticleImageFromMeta`: an article page with `<meta property="og:image" content="/i.jpg">` gives an absolute https Image URL; a `data:` or `javascript:` image gives "".
  - `TestFetchImageBlocked` (Review Focus 4): default Options against loopback and a redirect to a private address: ErrBlocked.
  - `TestFetchImageNotImage`: text/html: error.
  - `TestPrepareScalesAndCompresses`: a generated 2400x1600 PNG becomes a JPEG at most 800 px wide, at most 90 KB, aspect kept within 1 px.
  - `TestPrepareNeverUpscales`: a 300x200 image stays 300 wide.
  - `TestPrepareRefusesHugeDimensions` (Review Focus 3): a PNG header claiming 20000x20000: error before full decode (assert quickly, low memory).
  - `TestBuildWithImages`: EPUB manifest lists `img/ch1.jpg` as image/jpeg, file present, epub still validates structurally; bad image names refused.
  - `TestRenderLeadImageOnly`: the image appears once, after the lead story's headline, with alt text; never in front.xhtml.
  - `TestRunImagesCapped` (Review Focus 5): fakes returning ~400 KB prepared images for 4 chapters: only the first two get images, total at most 1 MiB, colophon says "pictures: 2 of 4".
- [ ] **Step 2: Run them, expect FAIL.**
- [ ] **Step 3: Implement** as specified above; image fetches run with the fetch package's concurrency and timeouts; no new third-party dependencies beyond golang.org/x/image subpackages.
- [ ] **Step 4: Run `go test -race ./...`, expect PASS.**
- [ ] **Step 5: Commit** `git commit -m "Paper: one small lead picture per chapter, fetched safely and compressed"`
