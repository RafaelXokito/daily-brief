"""Throwaway spike: The Daily Brief EPUB in the A+C identity.

Usage: python -I build2.py <journal.json> <paper.md with full articles> <outdir>
"""
import datetime as dt
import html
import json
import re
import subprocess
import sys
import uuid
import zipfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
journal = json.loads(Path(sys.argv[1]).read_text())
paper_md = Path(sys.argv[2]).read_text()
out = Path(sys.argv[3]).resolve()
out.mkdir(parents=True, exist_ok=True)
date = dt.date.fromisoformat(journal["date"])
LINK = re.compile(r"\[([^\]]*)\]\((https?://[^)\s]+)\)")
ORD = "First Second Third Fourth Fifth Sixth Seventh Eighth Ninth Tenth Eleventh Twelfth".split()
TAGS = {"ACT": "Act now", "WATCH": "Worth watching", "FYI": "For your information"}
esc = html.escape


def md2html(md):
    return subprocess.run(["pandoc", "-f", "markdown-auto_identifiers", "-t", "html5"], input=md,
                          capture_output=True, text=True, check=True).stdout


def norm(s):
    return re.sub(r"\W+", " ", s.lower()).strip()


def title_case(s):
    small = {"and", "of", "the", "&"}
    return " ".join(w if w in small or w.isupper() else w[:1].upper() + w[1:] for w in s.split())


def items_of(md):
    parts = re.split(r"^### (.+)$", md, flags=re.M)
    return [(parts[i].strip(), parts[i + 1].strip()) for i in range(1, len(parts), 2)]


# Full articles from the earlier spike's paper.md: "## headline {#aN}" blocks.
arts = {}
full = paper_md.split("# Full articles {#articles}", 1)[1].split("# Colophon", 1)[0]
for block in re.split(r"^## ", full, flags=re.M)[1:]:
    head, body = block.split("\n", 1)
    h = re.sub(r"\s*\{#a\d+\}\s*$", "", head).strip()
    body = re.sub(r"^\[← Back to the brief\]\(#[^)]+\)\s*$", "", body, flags=re.M).strip()
    m = re.match(r"^\*([^*]+)\*\s*\n", body)
    src = m.group(1) if m else ""
    arts[norm(h)] = (src, body[m.end():] if m else body)

sections = [s for s in journal["sections"] if s.get("status") == "done"]
chapters = []  # (file, toc_title, xhtml_body)
story_id = {}
art_list = []  # (aid, headline, source, html, back)


def story(ci, ii, head, body):
    sid = f"s{ci}-{ii}"
    story_id[norm(head)] = (f"ch{ci + 1}.xhtml", sid)
    lines = body.split("\n")
    tag, facts = "", ""
    m = re.match(r"^`(ACT|WATCH|FYI)`\s*(.*)$", lines[0])
    if m:
        tag, facts, lines = TAGS[m.group(1)], m.group(2).strip(), lines[1:]
    prose = "\n".join(lines).strip()
    url = (LINK.search(prose) or [None, None, None])[2] if LINK.search(prose) else None
    prose = re.sub(r"\s*\[source\]\([^)]+\)", "", prose)
    prose = LINK.sub(r"\1", prose)
    x = [f'<h2 class="story" id="{sid}">{esc(head)}</h2>']
    if tag:
        x.append(f'<p class="tag{" act" if tag == "Act now" else ""}">{tag}</p>')
    if facts:
        x.append(f'<p class="facts">{esc(facts)}</p>')
    x.append(md2html(prose))
    a = arts.get(norm(head))
    if a:
        aid = f"a{len(art_list)}"
        art_list.append((aid, head, a[0], md2html(a[1]), (f"ch{ci + 1}.xhtml", sid)))
        x.append(f'<p class="more"><a href="articles.xhtml#{aid}">Read the full account ❧</a></p>')
    elif url:
        host = re.sub(r"^https?://(www\.)?", "", url).split("/")[0]
        x.append(f'<p class="srcline">From {esc(host)}</p>')
    return "\n".join(x)


for ci, s in enumerate(sections):
    its = items_of(s["markdown"])
    body = [f'<p class="chap">Chapter the {ORD[ci]}</p>', f'<h1>Of {esc(title_case(s["topic"]))}</h1>',
            '<p class="fl">❧</p>']
    for ii, (h, b) in enumerate(its):
        if ii:
            body.append('<p class="sep">⁂</p>')
        body.append(story(ci, ii, h, b))
    chapters.append((f"ch{ci + 1}.xhtml", f"{ORD[ci]}: Of {title_case(s['topic'])}", "\n".join(body)))

# Front page: the day in brief.
hl = journal["overview"].get("highlights", [])
fp = ['<p class="chap">The Front Page</p>', "<h1>The Day in Brief</h1>", '<p class="fl">❦</p>']
for tier, label in (("act", "To be acted upon"), ("watch", "To be watched"), ("fyi", "Worth knowing")):
    hs = [h for h in hl if h["tier"] == tier]
    if not hs:
        continue
    fp.append(f'<h2 class="tier">{label}</h2>')
    for h in hs:
        ref = story_id.get(norm(h["headline"]))
        head = esc(h["headline"])
        head = f'<a href="{ref[0]}#{ref[1]}">{head}</a>' if ref else head
        fp.append(f'<p class="hl"><span class="hlh">{head}</span>. {esc(h["text"])} '
                  f'<span class="why">{esc(h["why"])}</span> <span class="topic">({esc(h["topic"])})</span></p>')
chapters.insert(0, ("front.xhtml", "The Day in Brief", "\n".join(fp)))

# Full accounts.
ab = ['<p class="chap">The Full Accounts</p>', "<h1>As Reported</h1>", '<p class="fl">❧</p>']
for n, (aid, h, src, body, back) in enumerate(art_list):
    if n:
        ab.append('<p class="sep">⁂</p>')
    ab.append(f'<h2 class="story" id="{aid}">{esc(h)}</h2><p class="facts">{esc(src)}</p>')
    body = re.sub(r"<(/?)h[1-6]([^>]*)>", r"<\1h3\2>", body)
    ab.append(body)
    ab.append(f'<p class="more"><a href="{back[0]}#{back[1]}">Back to the brief ❧</a></p>')
chapters.append(("articles.xhtml", "The Full Accounts", "\n".join(ab)))
chapters.append(("colophon.xhtml", "Colophon", (
    '<p class="chap">Colophon</p><h1>Of This Edition</h1><p class="fl">❦</p>'
    f'<p class="center">Edition of {date:%A, %-d %B %Y}. Set in IM Fell English and UnifrakturMaguntia.<br/>'
    f'Model {esc(str(journal.get("model")))}. Full accounts extracted: {len(art_list)}.</p>')))

# Cover, rendered with headless Chrome.
act = [h for h in hl if h["tier"] == "act"]
lead = act[0]["headline"] if act else (hl[0]["headline"] if hl else "")
fontdir = (HERE / "fonts").as_uri()
cover_html = (HERE / "cover.tmpl.html").read_text().format(
    fontdir=fontdir, no=date.timetuple().tm_yday, day=f"{date:%A, %-d %B %Y}", lead=esc(lead),
    topics=" · ".join(esc(s["topic"].split(" & ")[0]) for s in sections[:4]), roman="MMXXVI")
(out / "cover.html").write_text(cover_html)
subprocess.run(["/home/rpereira/.local/bin/google-chrome", "--headless=new", "--disable-gpu", "--hide-scrollbars",
                f"--user-data-dir={out / 'prof'}", "--window-size=1264,1680", "--force-device-scale-factor=1",
                f"--screenshot={out / 'cover.png'}", (out / "cover.html").as_uri()],
               check=True, capture_output=True, timeout=60)

# Assemble EPUB 3.
css = (HERE / "paper2.css").read_text()
title = f"The Daily Brief · {date:%-d %B %Y}"
ident = f"urn:uuid:{uuid.uuid5(uuid.NAMESPACE_URL, 'daily-brief:' + date.isoformat())}"


def xhtml(t, body, cls=""):
    return ('<?xml version="1.0" encoding="utf-8"?>\n<!DOCTYPE html>\n'
            '<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" lang="en" xml:lang="en">'
            f'<head><meta charset="utf-8"/><title>{esc(t)}</title><link rel="stylesheet" href="paper.css"/></head>'
            f'<body class="{cls}">{body}</body></html>')


fonts = sorted((HERE / "fonts").glob("*.ttf"))
manifest = ['<item id="css" href="paper.css" media-type="text/css"/>',
            '<item id="cover-img" href="cover.png" media-type="image/png" properties="cover-image"/>',
            '<item id="cover" href="cover.xhtml" media-type="application/xhtml+xml"/>',
            '<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>',
            '<item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>']
manifest += [f'<item id="f{i}" href="fonts/{f.name}" media-type="font/ttf"/>' for i, f in enumerate(fonts)]
manifest += [f'<item id="c{i}" href="{f}" media-type="application/xhtml+xml"/>' for i, (f, _, _) in enumerate(chapters)]
spine = ['<itemref idref="cover" linear="yes"/>'] + [f'<itemref idref="c{i}"/>' for i in range(len(chapters))]
opf = f'''<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid" xml:lang="en">
<metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
<dc:identifier id="uid">{ident}</dc:identifier><dc:title>{esc(title)}</dc:title><dc:language>en</dc:language>
<dc:creator>The Daily Brief</dc:creator><dc:date>{date.isoformat()}</dc:date>
<meta property="dcterms:modified">{dt.datetime.now(dt.timezone.utc):%Y-%m-%dT%H:%M:%SZ}</meta>
<meta name="cover" content="cover-img"/>
</metadata>
<manifest>{"".join(manifest)}</manifest>
<spine toc="ncx">{"".join(spine)}</spine>
</package>'''
nav = xhtml("Contents", '<nav epub:type="toc" id="toc"><h1>Contents</h1><ol>' +
            "".join(f'<li><a href="{f}">{esc(t)}</a></li>' for f, t, _ in chapters) + "</ol></nav>")
ncx = ('<?xml version="1.0" encoding="utf-8"?><ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">'
       f'<head><meta name="dtb:uid" content="{ident}"/></head><docTitle><text>{esc(title)}</text></docTitle><navMap>' +
       "".join(f'<navPoint id="n{i}" playOrder="{i + 1}"><navLabel><text>{esc(t)}</text></navLabel>'
               f'<content src="{f}"/></navPoint>' for i, (f, t, _) in enumerate(chapters)) + "</navMap></ncx>")
epub = out / f"The Daily Brief {date.isoformat()}.epub"
with zipfile.ZipFile(epub, "w") as z:
    z.writestr(zipfile.ZipInfo("mimetype"), "application/epub+zip", compress_type=zipfile.ZIP_STORED)
    zc = zipfile.ZIP_DEFLATED
    z.writestr("META-INF/container.xml", '<?xml version="1.0"?><container version="1.0" '
               'xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile '
               'full-path="EPUB/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>', zc)
    z.writestr("EPUB/content.opf", opf, zc)
    z.writestr("EPUB/nav.xhtml", nav, zc)
    z.writestr("EPUB/toc.ncx", ncx, zc)
    z.writestr("EPUB/paper.css", css, zc)
    z.write(out / "cover.png", "EPUB/cover.png", zc)
    z.writestr("EPUB/cover.xhtml", xhtml("Cover", '<div class="coverpage"><img src="cover.png" alt="Cover"/></div>', "cover"), zc)
    for f in fonts:
        z.write(f, f"EPUB/fonts/{f.name}", zc)
    for f, t, b in chapters:
        z.writestr(f"EPUB/{f}", xhtml(t, b), zc)
print(epub, f"chapters={len(chapters)} stories={len(story_id)} articles={len(art_list)} lead={lead!r}")
