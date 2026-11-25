// Package epub assembles an EPUB 3 book: a cover page, XHTML chapters,
// a nav document and an NCX, the paper's stylesheet and the embedded
// fonts with their licences.
package epub

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"path"
	"strings"
	"time"

	"github.com/RafaelXokito/daily-brief/internal/fonts"
)

//go:embed paper.css
var css []byte

// Chapter is one spine document. Body is the XHTML inside <body>, which
// Build trusts as is; File is a plain name such as "ch1.xhtml".
type Chapter struct {
	File, TOCTitle, Body string
}

// Book is everything Build puts in the EPUB. Cover is a PNG. An empty
// Language means "en" and an empty Creator "The Daily Brief".
type Book struct {
	Title, Identifier, Language, Creator string
	Date                                 time.Time
	Cover                                []byte
	Chapters                             []Chapter
}

// entry is one file of the zip.
type entry struct {
	name string
	data []byte
}

// reserved are the documents Build writes itself next to the chapters.
var reserved = map[string]bool{"nav.xhtml": true, "cover.xhtml": true}

// Build returns the EPUB file's bytes.
func Build(b Book) ([]byte, error) {
	if err := validate(b); err != nil {
		return nil, err
	}
	if b.Language == "" {
		b.Language = "en"
	}
	if b.Creator == "" {
		b.Creator = "The Daily Brief"
	}
	fontFiles, err := fs.ReadDir(fonts.Files, ".")
	if err != nil {
		return nil, fmt.Errorf("epub: fonts: %w", err)
	}
	entries := []entry{
		{"META-INF/container.xml", []byte(container)},
		{"EPUB/content.opf", opf(b, fontFiles)},
		{"EPUB/nav.xhtml", nav(b)},
		{"EPUB/toc.ncx", ncx(b)},
		{"EPUB/paper.css", css},
		{"EPUB/cover.png", b.Cover},
		{"EPUB/cover.xhtml", xhtml(b.Language, "Cover", `<div class="coverpage"><img src="cover.png" alt="Cover"/></div>`, "cover")},
	}
	for _, f := range fontFiles {
		data, err := fs.ReadFile(fonts.Files, f.Name())
		if err != nil {
			return nil, fmt.Errorf("epub: fonts: %w", err)
		}
		entries = append(entries, entry{"EPUB/fonts/" + f.Name(), data})
	}
	for _, c := range b.Chapters {
		entries = append(entries, entry{"EPUB/" + c.File, xhtml(b.Language, c.TOCTitle, c.Body, "")})
	}
	return zipUp(entries)
}

// zipUp writes mimetype first, stored with no extra field as the OCF
// wants, then entries deflated.
func zipUp(entries []entry) ([]byte, error) {
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	// No Modified time: it would add an extra field to mimetype.
	w, err := z.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	if err != nil {
		return nil, err
	}
	if _, err := w.Write([]byte("application/epub+zip")); err != nil {
		return nil, err
	}
	now := time.Now()
	for _, e := range entries {
		w, err := z.CreateHeader(&zip.FileHeader{Name: e.name, Method: zip.Deflate, Modified: now})
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(e.data); err != nil {
			return nil, err
		}
	}
	if err := z.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func validate(b Book) error {
	switch {
	case strings.TrimSpace(b.Identifier) == "":
		return errors.New("epub: no identifier")
	case strings.TrimSpace(b.Title) == "":
		return errors.New("epub: no title")
	case len(b.Cover) == 0:
		return errors.New("epub: no cover")
	case b.Date.IsZero():
		return errors.New("epub: no date")
	case len(b.Chapters) == 0:
		return errors.New("epub: no chapters")
	}
	seen := map[string]bool{}
	for _, c := range b.Chapters {
		if c.File != path.Base(c.File) || strings.ContainsAny(c.File, `\:`) || !strings.HasSuffix(c.File, ".xhtml") ||
			len(c.File) == len(".xhtml") || c.File[0] == '.' {
			return fmt.Errorf("epub: bad chapter file name %q", c.File)
		}
		if reserved[c.File] || seen[c.File] {
			return fmt.Errorf("epub: chapter file name %q is taken", c.File)
		}
		seen[c.File] = true
	}
	return nil
}

const container = `<?xml version="1.0" encoding="utf-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="EPUB/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>
`

// esc makes s safe as XML text or an attribute value: characters XML
// cannot hold are dropped, invalid UTF-8 is replaced, and the rest escaped.
func esc(s string) string {
	return template.HTMLEscapeString(strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' || (r >= 0x20 && r <= 0xD7FF) ||
			(r >= 0xE000 && r <= 0xFFFD) || r >= 0x10000 {
			return r
		}
		return -1
	}, s))
}

func xhtml(lang, title, body, class string) []byte {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="utf-8"?>` + "\n<!DOCTYPE html>\n")
	sb.WriteString(`<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" lang="` +
		esc(lang) + `" xml:lang="` + esc(lang) + `">`)
	sb.WriteString(`<head><meta charset="utf-8"/><title>` + esc(title) + `</title><link rel="stylesheet" type="text/css" href="paper.css"/></head>`)
	if class != "" {
		sb.WriteString(`<body class="` + esc(class) + `">`)
	} else {
		sb.WriteString("<body>")
	}
	sb.WriteString(body)
	sb.WriteString("</body></html>\n")
	return []byte(sb.String())
}

func opf(b Book, fontFiles []fs.DirEntry) []byte {
	var m, s strings.Builder
	m.WriteString(`<item id="css" href="paper.css" media-type="text/css"/>`)
	m.WriteString(`<item id="cover-img" href="cover.png" media-type="image/png" properties="cover-image"/>`)
	m.WriteString(`<item id="cover" href="cover.xhtml" media-type="application/xhtml+xml"/>`)
	m.WriteString(`<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>`)
	m.WriteString(`<item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>`)
	for i, f := range fontFiles {
		mt := "font/ttf"
		if strings.HasSuffix(f.Name(), ".txt") {
			mt = "text/plain"
		}
		fmt.Fprintf(&m, `<item id="f%d" href="fonts/%s" media-type="%s"/>`, i, esc(f.Name()), mt)
	}
	s.WriteString(`<itemref idref="cover" linear="yes"/>`)
	for i, c := range b.Chapters {
		fmt.Fprintf(&m, `<item id="c%d" href="%s" media-type="application/xhtml+xml"/>`, i, esc(c.File))
		fmt.Fprintf(&s, `<itemref idref="c%d"/>`, i)
	}
	return fmt.Appendf(nil, `<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid" xml:lang="%[1]s">
<metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
<dc:identifier id="uid">%[2]s</dc:identifier>
<dc:title>%[3]s</dc:title>
<dc:language>%[1]s</dc:language>
<dc:creator>%[8]s</dc:creator>
<dc:date>%[4]s</dc:date>
<meta property="dcterms:modified">%[5]s</meta>
<meta name="cover" content="cover-img"/>
</metadata>
<manifest>%[6]s</manifest>
<spine toc="ncx">%[7]s</spine>
</package>
`, esc(b.Language), esc(b.Identifier), esc(b.Title), b.Date.Format(time.DateOnly),
		time.Now().UTC().Format("2006-01-02T15:04:05Z"), m.String(), s.String(), esc(b.Creator))
}

func nav(b Book) []byte {
	var sb strings.Builder
	sb.WriteString(`<nav epub:type="toc" id="toc"><h1>Contents</h1><ol>`)
	for _, c := range b.Chapters {
		sb.WriteString(`<li><a href="` + esc(c.File) + `">` + esc(c.TOCTitle) + `</a></li>`)
	}
	sb.WriteString("</ol></nav>")
	return xhtml(b.Language, "Contents", sb.String(), "")
}

func ncx(b Book) []byte {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="utf-8"?>` + "\n")
	sb.WriteString(`<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1"><head>`)
	sb.WriteString(`<meta name="dtb:uid" content="` + esc(b.Identifier) + `"/>`)
	sb.WriteString(`<meta name="dtb:depth" content="1"/><meta name="dtb:totalPageCount" content="0"/><meta name="dtb:maxPageNumber" content="0"/>`)
	sb.WriteString(`</head><docTitle><text>` + esc(b.Title) + `</text></docTitle><navMap>`)
	for i, c := range b.Chapters {
		fmt.Fprintf(&sb, `<navPoint id="n%d" playOrder="%d"><navLabel><text>%s</text></navLabel><content src="%s"/></navPoint>`,
			i, i+1, esc(c.TOCTitle), esc(c.File))
	}
	sb.WriteString("</navMap></ncx>\n")
	return []byte(sb.String())
}
