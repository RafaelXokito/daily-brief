# Kobo Paper Revision 2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Front page lead pictures, In Brief as linked paragraphs with title-style topic headings, pictures where the text needs them, and highlighted key phrases in chapter stories.

**Architecture:** Task 1 changes the front pass (JSON shape, parse, fact check, fallback) and the front page template, with a picture slot on leads. Task 2 changes the chapter pass (needs_picture, `==highlight==` marks), the chapter template, and the picture selection in paper.Run (front leads, chapter leads, needs_picture, 1.5 MB cap).

**Tech Stack:** Go 1.26, existing packages only.

**Spec:** `docs/superpowers/specs/2026-11-24-kobo-paper-design.md`, section "Revision 2026-11-25 (second)".

## Global Constraints

- gofmt, `go vet ./...`, `go test -race ./...` pass; TUI unchanged; never the em dash; plain commit messages without attribution trailers; invented fixtures, generated images only.
- All model text stays escaped through html/template; highlights become `<mark class="key">` only via the parser, never raw HTML from the model.
- Picture cap 1.5 MiB per book; at most 6 `needs_picture` pictures; priority front leads, chapter leads, needs_picture; byte-identical pictures used once (a reused file does not count twice).
- The fact check ignores highlight marks and covers every In Brief sentence.

## Review Focus

1. A model reply with `==` marks unbalanced, nested, more than two per story, a mark over 12 words, or a mark containing `<script>`: the story renders with marks dropped and text intact, valid XHTML. Test in Task 2.
2. An In Brief paragraph sentence that cites a number not in its story: the front page falls back (or that topic's paragraph does). Test in Task 1.
3. A front lead whose story already has a chapter picture: the same `img/` file is referenced, the book does not grow. Test in Task 2.
4. Pictures exceeding 1.5 MiB: lower-priority pictures are dropped first, front leads kept. Test in Task 2.
5. A front page where no lead has a picture: the layout stays clean, no empty figure element. Test in Task 1.

---

### Task 1: In Brief paragraphs, title-style topic headings, a picture slot on front leads

**Files:** `internal/editor/model.go`, `prompt.go`, `parse.go`, `facts.go`, `fallback.go`, `run.go` (front pass only); `internal/paper/render.go`, `templates/front.xhtml`, `internal/epub/paper.css`; tests beside them.

**Interfaces:**
- Produces:
  ```go
  type BriefSentence struct { ID, Text string }       // one sentence, linked to story ID
  type BriefParagraph struct { Topic string; Sentences []BriefSentence }
  type FrontPage struct { Leads []FrontLead; InBrief []BriefParagraph; CoverLine string } // InBrief replaces the old []FrontLine
  type FrontLead struct { ID, Headline string; Paragraphs []string; Image string } // Image is set by paper.Run (Task 2), never by the model
  ```
- Front pass JSON, verbatim in the prompt:
  ```json
  {"leads":[{"id":"","headline":"","paragraphs":[""]}],"in_brief":[{"topic":"","sentences":[{"id":"","text":""}]}],"cover_line":""}
  ```
- Rules: one paragraph per front-page chapter that has non-lead stories, in chapter order, 2 to 4 sentences, each sentence's ID a story of that topic, every non-lead story of the topic covered by at most one sentence (not required to cover all); fact check each sentence against its story text plus item and article; any failure falls back to the mechanical front page (whole front page, as today).
- Fallback: per topic, one paragraph whose sentences are the stories' headlines, each ending with a period.
- Render: `<h2 class="brief-topic">Of Portugal</h2>` (italic, larger, with a fleuron line after), then `<p class="brief">` with each sentence wrapped in `<a href="chN.xhtml#id">`; leads render `<img class="lead-main">` for the first lead and `<img class="lead-pic">` for others only when `FrontLead.Image` is set (relative `img/<name>`), never an empty element.

- [ ] **Step 1: Failing tests:** parse accepts the new shape and rejects unknown IDs, IDs from another topic, more than 4 sentences, an ID reused; `TestCheckFrontBriefSentence` (Review Focus 2); fallback builds one linked paragraph per topic; render shows `brief-topic` headings in chapter order, one `<p class="brief">` per topic with resolving links, `lead-main` image only when Image is set (Review Focus 5, both cases), XHTML valid.
- [ ] **Step 2: Run, expect FAIL.**
- [ ] **Step 3: Implement.** CSS: `.brief-topic` like `h1` chapter titles but about 1.3em, green fleuron after; `.brief a` ink-coloured with no underline; `img.lead-main` width 100%; `img.lead-pic` width 45%, float right, margin.
- [ ] **Step 4: `go test -race ./...` PASS.**
- [ ] **Step 5: Commit** `git commit -m "Paper: In Brief as linked paragraphs with title headings, and a picture slot on the front page leads"`

### Task 2: Picture intent, front lead pictures, highlights

**Files:** `internal/editor/model.go`, `prompt.go`, `parse.go`, `facts.go` (chapter pass only); `internal/paper/render.go`, `templates/chapter.xhtml`, `internal/epub/paper.css`, `internal/paper/run.go` (picture selection), `docs/GUIDE.md`; tests beside them.

**Interfaces:**
- Consumes: Task 1's `FrontLead.Image`.
- Chapter JSON gains `"needs_picture": false` per story (optional, default false) and paragraphs may contain `==phrase==`.
- Produces:
  ```go
  type Story struct { /* existing */ ; NeedsPicture bool; Highlights [][2]int } // or an equivalent safe representation of highlighted spans per paragraph; choose and document
  ```
- Picture selection in paper.Run: candidates in priority order: front leads (by story URL), chapter leads, needs_picture stories in chapter order (at most 6); one download per URL; byte-identical results share one file; stop adding when the next picture would exceed 1.5 MiB; front lead images reference the same file as the chapter story when it exists; names `fN.jpg` for front-only pictures, `chN.jpg` for chapter leads, `chN-K.jpg` for needs_picture.
- Render: needs_picture image after the story's standfirst, `img.story-pic` width 60% centred; highlights as `<mark class="key">` with CSS `background-color:#ecd9a0; color:inherit; padding:0 0.1em`.

- [ ] **Step 1: Failing tests:** highlight parsing table (Review Focus 1: unbalanced, nested, three marks, 13-word mark, `<script>` inside); fact check ignores marks; render produces `<mark class="key">` escaped correctly; `needs_picture` parsed; `TestRunFrontLeadReusesChapterPicture` (Review Focus 3); `TestRunPicturePriority` (Review Focus 4: total over 1.5 MiB drops needs_picture first, then chapter leads, never front leads before them); at most 6 needs_picture pictures; colophon counts pictures.
- [ ] **Step 2: Run, expect FAIL.**
- [ ] **Step 3: Implement**; the prompt tells the model when to set needs_picture (an image genuinely helps: a device, place, spacecraft, route, chart) and how to use highlights (at most two short key phrases per story, never in headlines or standfirsts).
- [ ] **Step 4: `go test -race ./...` PASS.**
- [ ] **Step 5: Commit** `git commit -m "Paper: pictures where the text needs them, front lead pictures, and highlighted key phrases"`
