package epub

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"regexp"
	"strings"
	"testing"
	"time"
)

var fakePNG = []byte("\x89PNG\r\n\x1a\nnot really a picture")

func sampleBook() Book {
	return Book{
		Title:      "The Daily Brief · 25 November 2026",
		Identifier: "urn:daily-brief:2026-11-25",
		Language:   "en",
		Date:       time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC),
		Cover:      fakePNG,
		Chapters: []Chapter{
			{File: "front.xhtml", TOCTitle: "The Day in Brief", Body: `<h1>The Day in Brief</h1><p><a href="ch1.xhtml#t0i0">A link</a></p>`},
			{File: "ch1.xhtml", TOCTitle: "First: Of Java & Quarkus", Body: `<h1>Of Java &amp; Quarkus</h1><h2 class="story" id="t0i0">A story</h2><p>Text.</p>`},
			{File: "colophon.xhtml", TOCTitle: "Colophon", Body: `<h1>Of This Edition</h1>`},
		},
	}
}

func build(t *testing.T, b Book) map[string][]byte {
	t.Helper()
	data, err := Build(b)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}
	files := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		files[f.Name], _ = io.ReadAll(rc)
		rc.Close()
	}
	return files
}

func TestBuildLayout(t *testing.T) {
	data, err := Build(sampleBook())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}
	first := zr.File[0]
	if first.Name != "mimetype" || first.Method != zip.Store {
		t.Fatalf("first entry %q method %d, want mimetype stored", first.Name, first.Method)
	}
	if len(first.Extra) != 0 {
		t.Errorf("mimetype has an extra field, readers expect none")
	}
	// The OCF requires the literal bytes at offset 30 of the file.
	if got := string(data[30:58]); got != "mimetypeapplication/epub+zip" {
		t.Errorf("bytes at offset 30 = %q, want mimetypeapplication/epub+zip", got)
	}
	files := build(t, sampleBook())
	if got := string(files["mimetype"]); got != "application/epub+zip" {
		t.Errorf("mimetype content %q", got)
	}
	for _, name := range []string{
		"META-INF/container.xml", "EPUB/content.opf", "EPUB/nav.xhtml", "EPUB/toc.ncx",
		"EPUB/cover.png", "EPUB/cover.xhtml", "EPUB/paper.css",
		"EPUB/front.xhtml", "EPUB/ch1.xhtml", "EPUB/colophon.xhtml",
		"EPUB/fonts/IMFeENrm28P.ttf", "EPUB/fonts/IMFeENit28P.ttf", "EPUB/fonts/IMFeENsc28P.ttf",
		"EPUB/fonts/UnifrakturMaguntia-Book.ttf",
		"EPUB/fonts/OFL-IMFellEnglish.txt", "EPUB/fonts/OFL-UnifrakturMaguntia.txt",
	} {
		if len(files[name]) == 0 {
			t.Errorf("missing or empty %s", name)
		}
	}
	if !bytes.Equal(files["EPUB/cover.png"], fakePNG) {
		t.Errorf("cover.png is not the book's cover")
	}
	if !strings.Contains(string(files["META-INF/container.xml"]), `full-path="EPUB/content.opf"`) {
		t.Errorf("container.xml does not point at the OPF")
	}
	css := string(files["EPUB/paper.css"])
	for _, want := range []string{"fonts/IMFeENrm28P.ttf", "fonts/UnifrakturMaguntia-Book.ttf", "#2f5a3a", "#8a1c1c",
		"mark.key { background-color: #ecd9a0; color: inherit; padding: 0 0.1em; }", "img.story-pic { width: 60%;"} {
		if !strings.Contains(css, want) {
			t.Errorf("paper.css lacks %q", want)
		}
	}
}

type opfDoc struct {
	Metadata struct {
		Identifier string `xml:"identifier"`
		Title      string `xml:"title"`
		Creator    string `xml:"creator"`
		Language   string `xml:"language"`
		Date       string `xml:"date"`
		Meta       []struct {
			Property string `xml:"property,attr"`
			Value    string `xml:",chardata"`
		} `xml:"meta"`
	} `xml:"metadata"`
	Items []struct {
		ID         string `xml:"id,attr"`
		Href       string `xml:"href,attr"`
		MediaType  string `xml:"media-type,attr"`
		Properties string `xml:"properties,attr"`
	} `xml:"manifest>item"`
	Spine struct {
		Toc  string `xml:"toc,attr"`
		Refs []struct {
			IDRef string `xml:"idref,attr"`
		} `xml:"itemref"`
	} `xml:"spine"`
}

func TestBuildMetadata(t *testing.T) {
	files := build(t, sampleBook())
	var p opfDoc
	if err := xml.Unmarshal(files["EPUB/content.opf"], &p); err != nil {
		t.Fatalf("OPF does not parse: %v", err)
	}
	if p.Metadata.Identifier != "urn:daily-brief:2026-11-25" {
		t.Errorf("dc:identifier %q", p.Metadata.Identifier)
	}
	if p.Metadata.Title != "The Daily Brief · 25 November 2026" {
		t.Errorf("dc:title %q", p.Metadata.Title)
	}
	if p.Metadata.Creator != "The Daily Brief" {
		t.Errorf("dc:creator %q, want the default The Daily Brief", p.Metadata.Creator)
	}
	if p.Metadata.Language != "en" || p.Metadata.Date != "2026-11-25" {
		t.Errorf("dc:language %q, dc:date %q", p.Metadata.Language, p.Metadata.Date)
	}
	modified := false
	for _, m := range p.Metadata.Meta {
		if m.Property == "dcterms:modified" {
			modified = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$`).MatchString(m.Value)
		}
	}
	if !modified {
		t.Errorf("no well-formed dcterms:modified")
	}
	byID := map[string]string{}
	hrefs := map[string]bool{}
	for _, it := range p.Items {
		byID[it.ID] = it.Href
		hrefs[it.Href] = true
		switch it.Href {
		case "cover.png":
			if it.Properties != "cover-image" || it.MediaType != "image/png" {
				t.Errorf("cover.png item %+v, want cover-image image/png", it)
			}
		case "nav.xhtml":
			if it.Properties != "nav" {
				t.Errorf("nav.xhtml item %+v, want properties nav", it)
			}
		}
	}
	for _, f := range []string{"fonts/IMFeENrm28P.ttf", "fonts/OFL-IMFellEnglish.txt", "paper.css", "toc.ncx", "cover.xhtml"} {
		if !hrefs[f] {
			t.Errorf("manifest lacks %s", f)
		}
	}
	var spine []string
	for _, r := range p.Spine.Refs {
		spine = append(spine, byID[r.IDRef])
	}
	want := []string{"cover.xhtml", "front.xhtml", "ch1.xhtml", "colophon.xhtml"}
	if strings.Join(spine, ",") != strings.Join(want, ",") {
		t.Errorf("spine %v, want %v", spine, want)
	}
	if byID[p.Spine.Toc] != "toc.ncx" {
		t.Errorf("spine toc %q does not name the NCX", p.Spine.Toc)
	}
	nav := string(files["EPUB/nav.xhtml"])
	ncx := string(files["EPUB/toc.ncx"])
	for _, entry := range []string{"The Day in Brief", "First: Of Java &amp; Quarkus", "Colophon"} {
		if !strings.Contains(nav, entry) || !strings.Contains(ncx, entry) {
			t.Errorf("nav or NCX lacks %q", entry)
		}
	}
	if !strings.Contains(ncx, `content="urn:daily-brief:2026-11-25"`) {
		t.Errorf("NCX dtb:uid is not the identifier")
	}
}

func TestBuildXHTMLValid(t *testing.T) {
	b := sampleBook()
	b.Title = `Brief & "Co" <daily>` + "\x07"
	b.Chapters[0].TOCTitle = "Front & <centre>"
	files := build(t, b)
	for name, data := range files {
		if !strings.HasSuffix(name, ".xhtml") && !strings.HasSuffix(name, ".opf") &&
			!strings.HasSuffix(name, ".ncx") && !strings.HasSuffix(name, ".xml") {
			continue
		}
		d := xml.NewDecoder(bytes.NewReader(data))
		d.Strict = true
		for {
			_, err := d.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Errorf("%s does not parse: %v\n%s", name, err, data)
				break
			}
		}
	}
	if !strings.Contains(string(files["EPUB/content.opf"]), "Brief &amp; &#34;Co&#34; &lt;daily&gt;") {
		t.Errorf("title not escaped in the OPF:\n%s", files["EPUB/content.opf"])
	}
}

func TestBuildRefusesBadBooks(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Book)
	}{
		{"no identifier", func(b *Book) { b.Identifier = " " }},
		{"no title", func(b *Book) { b.Title = "" }},
		{"no cover", func(b *Book) { b.Cover = nil }},
		{"no date", func(b *Book) { b.Date = time.Time{} }},
		{"no chapters", func(b *Book) { b.Chapters = nil }},
		{"path in file name", func(b *Book) { b.Chapters[1].File = "../ch1.xhtml" }},
		{"subdirectory", func(b *Book) { b.Chapters[1].File = "fonts/ch1.xhtml" }},
		{"not xhtml", func(b *Book) { b.Chapters[1].File = "ch1.html" }},
		{"reserved name", func(b *Book) { b.Chapters[1].File = "nav.xhtml" }},
		{"duplicate", func(b *Book) { b.Chapters[1].File = "front.xhtml" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := sampleBook()
			b.Chapters = append([]Chapter(nil), b.Chapters...)
			tt.edit(&b)
			if _, err := Build(b); err == nil {
				t.Errorf("Build accepted a book with %s", tt.name)
			}
		})
	}
}

func TestBuildCreatorAndLanguage(t *testing.T) {
	b := sampleBook()
	b.Creator, b.Language = "Le Brief & Co", "pt"
	files := build(t, b)
	opf := string(files["EPUB/content.opf"])
	if !strings.Contains(opf, "<dc:creator>Le Brief &amp; Co</dc:creator>") || !strings.Contains(opf, "<dc:language>pt</dc:language>") {
		t.Errorf("creator or language not in the OPF:\n%s", opf)
	}
	for _, name := range []string{"EPUB/ch1.xhtml", "EPUB/nav.xhtml", "EPUB/cover.xhtml"} {
		if !strings.Contains(string(files[name]), `lang="pt" xml:lang="pt"`) {
			t.Errorf("%s does not carry the book's language:\n%s", name, files[name])
		}
	}
}

var fakeJPEG = []byte("\xff\xd8\xff\xe0 invented picture bytes")

func TestBuildWithImages(t *testing.T) {
	b := sampleBook()
	b.Chapters = append([]Chapter(nil), b.Chapters...)
	b.Chapters[1].Body += `<img src="img/ch1.jpg" alt="A story"/>`
	b.Images = []Image{{Name: "ch1.jpg", Data: fakeJPEG}}
	files := build(t, b)
	if !bytes.Equal(files["EPUB/img/ch1.jpg"], fakeJPEG) {
		t.Errorf("EPUB/img/ch1.jpg missing or not the picture")
	}
	var p opfDoc
	if err := xml.Unmarshal(files["EPUB/content.opf"], &p); err != nil {
		t.Fatalf("OPF does not parse: %v", err)
	}
	found := false
	for _, it := range p.Items {
		if it.Href == "img/ch1.jpg" {
			found = true
			if it.MediaType != "image/jpeg" {
				t.Errorf("img/ch1.jpg media type %q, want image/jpeg", it.MediaType)
			}
		}
	}
	if !found {
		t.Errorf("manifest lacks img/ch1.jpg")
	}
	ids := map[string]bool{}
	for _, it := range p.Items {
		if ids[it.ID] {
			t.Errorf("manifest id %q used twice", it.ID)
		}
		ids[it.ID] = true
	}
	for _, r := range p.Spine.Refs {
		if r.IDRef != "" && strings.HasPrefix(r.IDRef, "i") {
			t.Errorf("a picture %q is in the spine", r.IDRef)
		}
	}
}

func TestBuildRefusesBadImages(t *testing.T) {
	tests := map[string]Image{
		"path":           {Name: "../ch1.jpg", Data: fakeJPEG},
		"subdirectory":   {Name: "x/ch1.jpg", Data: fakeJPEG},
		"backslash":      {Name: `x\ch1.jpg`, Data: fakeJPEG},
		"colon":          {Name: "c:ch1.jpg", Data: fakeJPEG},
		"not jpg":        {Name: "ch1.png", Data: fakeJPEG},
		"bare extension": {Name: ".jpg", Data: fakeJPEG},
		"hidden":         {Name: ".ch1.jpg", Data: fakeJPEG},
		"not a jpeg":     {Name: "ch1.jpg", Data: fakePNG},
		"empty":          {Name: "ch1.jpg"},
	}
	for name, img := range tests {
		t.Run(name, func(t *testing.T) {
			b := sampleBook()
			b.Images = []Image{img}
			if _, err := Build(b); err == nil {
				t.Errorf("Build accepted image %q", img.Name)
			}
		})
	}
	b := sampleBook()
	b.Images = []Image{{Name: "ch1.jpg", Data: fakeJPEG}, {Name: "ch1.jpg", Data: fakeJPEG}}
	if _, err := Build(b); err == nil {
		t.Error("Build accepted two pictures with one name")
	}
}
