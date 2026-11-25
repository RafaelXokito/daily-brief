package paper

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/RafaelXokito/daily-brief/internal/config"
	"github.com/RafaelXokito/daily-brief/internal/editor"
	"github.com/RafaelXokito/daily-brief/internal/epub"
)

// picturePaper is invented: front leads on a chapter lead (t0i0) and on a
// plain story (t1i1); chapter leads t0i0 and t1i0; stories needing a
// picture t0i1 and t1i2. Every story's URL is https://example.com/<id>.
func picturePaper() editor.Paper {
	st := func(id, placement string, needs bool) editor.Story {
		return editor.Story{ID: id, Headline: "Story " + id, Paragraphs: []string{"Text."}, Placement: placement,
			URL: "https://example.com/" + id, NeedsPicture: needs}
	}
	return editor.Paper{
		Front: editor.FrontPage{Leads: []editor.FrontLead{{ID: "t0i0", Headline: "One"}, {ID: "t1i1", Headline: "Two"}}},
		Chapters: []editor.Chapter{
			{Topic: "Alpha", Stories: []editor.Story{st("t0i0", "lead", false), st("t0i1", "story", true)}},
			{Topic: "Empty"},
			{Topic: "Beta", Stories: []editor.Story{st("t1i0", "lead", false), st("t1i1", "story", false), st("t1i2", "brief", true)}},
		},
	}
}

// previews maps every story URL of p to a preview image URL.
func previews(p editor.Paper) map[string]string {
	out := map[string]string{}
	for _, c := range p.Chapters {
		for _, s := range c.Stories {
			out[s.URL] = s.URL + ".jpg"
		}
	}
	return out
}

func pictureRun(f *fakePictures) *run {
	return &run{ctx: context.Background(), o: Options{Log: io.Discard}, d: Deps{Picture: f.picture}}
}

func imageNames(pics []epub.Image) []string {
	var out []string
	for _, p := range pics {
		out = append(out, p.Name)
	}
	return out
}

// storyImages is each story's Image by id, and each front lead's under "front:<id>".
func storyImages(p editor.Paper) map[string]string {
	out := map[string]string{}
	for _, c := range p.Chapters {
		for _, s := range c.Stories {
			out[s.ID] = s.Image
		}
	}
	for _, l := range p.Front.Leads {
		out["front:"+l.ID] = l.Image
	}
	return out
}

func TestPicturesNamesAndPlaces(t *testing.T) {
	f := &fakePictures{size: 10_000}
	p, pics, candidates := pictureRun(f).pictures(picturePaper(), previews(picturePaper()))
	if got, want := imageNames(pics), []string{"ch1.jpg", "f2.jpg", "ch2.jpg", "ch1-1.jpg", "ch2-1.jpg"}; !slices.Equal(got, want) {
		t.Errorf("pictures %v, want %v (front leads, chapter leads, then stories that need one)", got, want)
	}
	want := map[string]string{
		"front:t0i0": "ch1.jpg", "front:t1i1": "f2.jpg",
		"t0i0": "ch1.jpg", "t0i1": "ch1-1.jpg", "t1i0": "ch2.jpg", "t1i1": "", "t1i2": "ch2-1.jpg",
	}
	if got := storyImages(p); !maps.Equal(got, want) {
		t.Errorf("images %v, want %v", got, want)
	}
	if candidates != 5 {
		t.Errorf("candidates %d, want 5", candidates)
	}
	if picturePaper().Chapters[0].Stories[0].Image != "" || picturePaper().Front.Leads[0].Image != "" {
		t.Error("the input paper was changed")
	}
}

// Review Focus 3: a front lead whose story has a chapter picture uses the
// same file, fetched once, and the book does not grow.
func TestRunFrontLeadReusesChapterPicture(t *testing.T) {
	f := &fakePictures{size: 10_000}
	in := picturePaper()
	in.Front.Leads = in.Front.Leads[:1] // t0i0, chapter one's lead
	p, pics, _ := pictureRun(f).pictures(in, previews(in))
	if p.Front.Leads[0].Image != "ch1.jpg" || p.Chapters[0].Stories[0].Image != "ch1.jpg" {
		t.Errorf("front lead %q and chapter lead %q, want both ch1.jpg", p.Front.Leads[0].Image, p.Chapters[0].Stories[0].Image)
	}
	if n := slices.Index(f.urls, "https://example.com/t0i0.jpg"); n < 0 || slices.Index(f.urls[n+1:], f.urls[n]) >= 0 {
		t.Errorf("picture downloads %v, want t0i0's once", f.urls)
	}
	if got := imageNames(pics); slices.Index(got, "ch1.jpg") < 0 || len(got) != 4 {
		t.Errorf("pictures %v, want ch1.jpg once among four", got)
	}

	// The whole run: the front leads are chapter leads, and the book holds
	// one file each.
	e := newRunEnv(t)
	e.picture.size = 50_000
	r, err := e.run(Options{NoDeliver: true})
	if err != nil {
		t.Fatal(err)
	}
	book, err := os.ReadFile(r.File)
	if err != nil {
		t.Fatal(err)
	}
	if names, total := bookImages(t, book); !slices.Equal(names, []string{"EPUB/img/ch1.jpg", "EPUB/img/ch2.jpg"}) || total != 100_000 {
		t.Errorf("pictures in the book %v (%d bytes), want ch1.jpg and ch2.jpg, 100000 bytes", names, total)
	}
	slices.Sort(e.picture.urls)
	if want := []string{"https://example.com/alpha/a.jpg", "https://example.com/beta/a.jpg"}; !slices.Equal(e.picture.urls, want) {
		t.Errorf("pictures fetched %v, want %v", e.picture.urls, want)
	}
}

func bookImages(t *testing.T, book []byte) (names []string, total int) {
	t.Helper()
	z, err := zip.NewReader(bytes.NewReader(book), int64(len(book)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range z.File {
		if strings.HasPrefix(f.Name, "EPUB/img/") {
			names = append(names, f.Name)
			total += int(f.UncompressedSize64)
		}
	}
	return names, total
}

// Review Focus 4: over the budget, stories that need a picture go first,
// then chapter leads; front leads are kept before either.
func TestRunPicturePriority(t *testing.T) {
	const kb = 1000
	for _, tc := range []struct {
		name  string
		sizes map[string]int // by story id
		want  []string
	}{
		{"all fit", map[string]int{"t0i0": 300 * kb, "t1i1": 300 * kb, "t1i0": 300 * kb, "t0i1": 300 * kb, "t1i2": 300 * kb},
			[]string{"ch1.jpg", "f2.jpg", "ch2.jpg", "ch1-1.jpg", "ch2-1.jpg"}},
		{"needs_picture dropped first", map[string]int{"t0i0": 500 * kb, "t1i1": 500 * kb, "t1i0": 500 * kb, "t0i1": 100 * kb, "t1i2": 10 * kb},
			[]string{"ch1.jpg", "f2.jpg", "ch2.jpg"}},
		{"then chapter leads", map[string]int{"t0i0": 700 * kb, "t1i1": 700 * kb, "t1i0": 700 * kb, "t0i1": 10 * kb, "t1i2": 10 * kb},
			[]string{"ch1.jpg", "f2.jpg"}},
		{"front leads before chapter leads", map[string]int{"t0i0": 100 * kb, "t1i1": 1400 * kb, "t1i0": 100 * kb, "t0i1": 10 * kb, "t1i2": 10 * kb},
			[]string{"ch1.jpg", "f2.jpg"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakePictures{sizes: map[string]int{}}
			for id, n := range tc.sizes {
				f.sizes["https://example.com/"+id+".jpg"] = n
			}
			p, pics, _ := pictureRun(f).pictures(picturePaper(), previews(picturePaper()))
			if got := imageNames(pics); !slices.Equal(got, tc.want) {
				t.Errorf("pictures %v, want %v", got, tc.want)
			}
			total := 0
			for _, pic := range pics {
				total += len(pic.Data)
			}
			if total > maxPictureBytes {
				t.Errorf("pictures total %d bytes, over %d", total, maxPictureBytes)
			}
			for id, img := range storyImages(p) {
				if img != "" && !slices.Contains(imageNames(pics), img) {
					t.Errorf("%s shows %s, not in the book", id, img)
				}
			}
		})
	}
}

func TestPicturesAtMostSixNeedsPicture(t *testing.T) {
	var stories []editor.Story
	for i := range 9 {
		placement := "story"
		if i == 0 {
			placement = "lead"
		}
		id := fmt.Sprintf("t0i%d", i)
		stories = append(stories, editor.Story{ID: id, Headline: id, Paragraphs: []string{"Text."}, Placement: placement,
			URL: "https://example.com/" + id, NeedsPicture: true})
	}
	in := editor.Paper{Chapters: []editor.Chapter{{Topic: "Alpha", Stories: stories}}}
	f := &fakePictures{size: 10_000}
	p, pics, candidates := pictureRun(f).pictures(in, previews(in))
	want := []string{"ch1.jpg", "ch1-1.jpg", "ch1-2.jpg", "ch1-3.jpg", "ch1-4.jpg", "ch1-5.jpg", "ch1-6.jpg"}
	if got := imageNames(pics); !slices.Equal(got, want) {
		t.Errorf("pictures %v, want %v", got, want)
	}
	if len(f.urls) != 7 || candidates != 7 {
		t.Errorf("downloads %d, candidates %d; want 7 and 7 (the lead and six stories)", len(f.urls), candidates)
	}
	for _, s := range p.Chapters[0].Stories[7:] {
		if s.Image != "" {
			t.Errorf("story %s past the sixth has picture %s", s.ID, s.Image)
		}
	}
}

// Two stories whose sources share a preview image download it once and
// print it once, like a site's generic logo.
func TestPicturesOneDownloadPerURL(t *testing.T) {
	in := picturePaper()
	images := previews(in)
	images["https://example.com/t0i1"] = images["https://example.com/t0i0"]
	f := &fakePictures{size: 10_000}
	p, pics, _ := pictureRun(f).pictures(in, images)
	if n := len(f.urls); n != 4 {
		t.Errorf("downloads %v, want 4: one per preview image", f.urls)
	}
	if got := imageNames(pics); slices.Contains(got, "ch1-1.jpg") {
		t.Errorf("pictures %v: the repeated picture is printed twice", got)
	}
	if img := p.Chapters[0].Stories[1].Image; img != "" {
		t.Errorf("t0i1 shows %q, want no picture", img)
	}
}

// End to end: a story the editor says needs a picture shows its source's
// preview image after its standfirst.
func TestRunNeedsPicture(t *testing.T) {
	e := newRunEnv(t)
	e.cfg.Topics = []config.Topic{{Name: "Alpha"}}
	e.picture.size = 20_000
	e.fetch.allImages = true
	e.backend.needsPicture = true
	r, err := e.run(Options{NoDeliver: true})
	if err != nil {
		t.Fatal(err)
	}
	book, err := os.ReadFile(r.File)
	if err != nil {
		t.Fatal(err)
	}
	if names, _ := bookImages(t, book); !slices.Equal(names, []string{"EPUB/img/ch1.jpg", "EPUB/img/ch1-1.jpg"}) {
		t.Errorf("pictures in the book %v, want ch1.jpg and ch1-1.jpg", names)
	}
	ch1 := string(epubFile(t, book, "EPUB/ch1.xhtml"))
	if !strings.Contains(ch1, `<img class="story-pic" src="img/ch1-1.jpg" alt="Edited story"/>`) {
		t.Errorf("chapter lacks the story picture\n%s", ch1)
	}
	if c := string(epubFile(t, book, "EPUB/colophon.xhtml")); !strings.Contains(c, "Pictures: 2 of 2 ") {
		t.Errorf("colophon lacks Pictures: 2 of 2\n%s", c)
	}
}

// Review Focus 1 end to end: whatever marks the model writes, the chapter
// prints its text whole, valid, and highlights only what is well formed.
func TestRunHighlights(t *testing.T) {
	for _, tc := range []struct {
		name, paragraph, want string
	}{
		{"valid", "The team ==shipped the widget== at last.", `<p>The team <mark class="key">shipped the widget</mark> at last.</p>`},
		{"unbalanced", "The team ==shipped the widget at last.", `<p>The team shipped the widget at last.</p>`},
		{"nested", "The ==team ==shipped== widget== ok.", `<p>The team shipped widget ok.</p>`},
		{"three marks", "==One== and ==two== and ==three==.", `<p><mark class="key">One</mark> and <mark class="key">two</mark> and three.</p>`},
		{"over twelve words", "==" + strings.TrimSpace(strings.Repeat("very ", 13)) + "== end.", "<p>" + strings.TrimSpace(strings.Repeat("very ", 13)) + " end.</p>"},
		{"script", "Hi ==<script>alert(x)</script>== there.", `<p>Hi &lt;script&gt;alert(x)&lt;/script&gt; there.</p>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newRunEnv(t)
			e.cfg.Topics = []config.Topic{{Name: "Alpha"}}
			e.backend.paragraph = tc.paragraph
			r, err := e.run(Options{NoDeliver: true})
			if err != nil {
				t.Fatal(err)
			}
			book, err := os.ReadFile(r.File)
			if err != nil {
				t.Fatal(err)
			}
			ch1 := string(epubFile(t, book, "EPUB/ch1.xhtml"))
			if !strings.Contains(ch1, tc.want) {
				t.Errorf("chapter lacks %s\n%s", tc.want, ch1)
			}
			if strings.Contains(ch1, "==") || strings.Contains(ch1, "<script>") {
				t.Errorf("chapter keeps a mark or raw markup\n%s", ch1)
			}
			assertXML(t, "ch1.xhtml", ch1[strings.Index(ch1, "<body"):strings.Index(ch1, "</body>")+len("</body>")])
		})
	}
}
