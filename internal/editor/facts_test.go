package editor

import "testing"

func TestCheckStoryNumbers(t *testing.T) {
	it := ItemIn{Facts: "CVSS 9.8 · patched in 25.0.5.1", Prose: "Costs $3 per million.", Article: "Released on 24 November 2026."}
	ok := Story{Paragraphs: []string{"Rated 9.8, fixed in 25.0.5.1, $3 per million, out 24 November 2026."}}
	bad := Story{Paragraphs: []string{"Rated 9.9 and fixed in 25.0.6."}}
	if !checkStory(ok, it) || checkStory(bad, it) {
		t.Fatal()
	}
}

func TestCheckStoryFields(t *testing.T) {
	it := ItemIn{Headline: "Patch 4 servers", Prose: "Four servers."}
	for name, s := range map[string]Story{
		"headline":   {Headline: "Patch 5 servers"},
		"standfirst": {Standfirst: "Affects 7 hosts"},
		"dateline":   {Dateline: "PARIS"},
	} {
		if checkStory(s, it) {
			t.Errorf("%s should fail", name)
		}
	}
	if !checkStory(Story{Headline: "Patch 4 servers", Standfirst: "No numbers here."}, it) {
		t.Fatal("clean story should pass")
	}
}

func TestCheckStoryCVE(t *testing.T) {
	it := ItemIn{Prose: "A flaw.", Article: "Tracked as CVE-2026-114938 upstream."}
	if !checkStory(Story{Paragraphs: []string{"It is CVE-2026-114938."}}, it) {
		t.Fatal("cve from article should pass")
	}
	if checkStory(Story{Paragraphs: []string{"It is CVE-2026-114939."}}, it) {
		t.Fatal("invented cve should fail")
	}
}

func TestCheckStoryDateline(t *testing.T) {
	it := ItemIn{Prose: "The commission in brussels said so."}
	if !checkStory(Story{Dateline: "BRUSSELS", Paragraphs: []string{"x"}}, it) {
		t.Fatal("case-insensitive place should pass")
	}
	if checkStory(Story{Dateline: "BRUSSELS"}, ItemIn{Prose: "Nothing."}) {
		t.Fatal("unsupported place should fail")
	}
	if checkStory(Story{Dateline: "NEW YORK"}, ItemIn{Article: "New plans."}) {
		t.Fatal("every word must be present")
	}
}

func TestCheckStoryThousands(t *testing.T) {
	if !checkStory(Story{Paragraphs: []string{"About 1,000,000 users."}}, ItemIn{Prose: "1000000 users"}) {
		t.Fatal("story commas")
	}
	if !checkStory(Story{Paragraphs: []string{"About 1000000 users."}}, ItemIn{Prose: "1,000,000 users"}) {
		t.Fatal("source commas")
	}
}

func TestCheckStoryCurrency(t *testing.T) {
	it := ItemIn{Prose: "It costs 5 dollars, or €5 in Europe."}
	if checkStory(Story{Paragraphs: []string{"It costs $5."}}, it) {
		t.Fatal("currency symbol not in source should fail")
	}
	if !checkStory(Story{Paragraphs: []string{"It costs €5."}}, it) {
		t.Fatal("currency in source should pass")
	}
}

func TestCheckStoryDatelineWholeWords(t *testing.T) {
	for _, tc := range []struct{ place, src string }{
		{"PARIS", "a comparison"}, {"LIMA", "the climate"}, {"ROME", "to promote"}, {"NICE", "a notice"},
	} {
		if checkStory(Story{Dateline: tc.place}, ItemIn{Prose: tc.src}) {
			t.Errorf("%s must not match %q", tc.place, tc.src)
		}
	}
}

func TestCheckStoryHost(t *testing.T) {
	if !checkStory(Story{Paragraphs: []string{"Reported by 9to5mac."}}, ItemIn{Host: "9to5mac.com"}) {
		t.Fatal("host digits are source text")
	}
}

func TestCheckStoryRejectsTheResearch(t *testing.T) {
	it := ItemIn{Prose: "Some prose."}
	for name, s := range map[string]Story{
		"paragraph":  {Paragraphs: []string{"Patch today, according to the research."}},
		"mixed case": {Paragraphs: []string{"The Research advises caution."}},
		"headline":   {Headline: "Which the research flags"},
		"standfirst": {Standfirst: "Per the research, it is fixed."},
		"says":       {Paragraphs: []string{"Which the research says fixes four advisories."}},
	} {
		if checkStory(s, it) {
			t.Errorf("%s should fail", name)
		}
	}
	for _, ok := range []string{
		"The research, published in Nature, found a new moon.",
		"The research team opened a lab.",
	} {
		if !checkStory(Story{Paragraphs: []string{ok}}, it) {
			t.Errorf("%q should pass", ok)
		}
	}
}

func TestFrontChecksRejectTheResearch(t *testing.T) {
	src := newFrontSources([]Chapter{{Stories: []Story{{ID: "a"}}}}, map[string]ItemIn{})
	if src.briefOK(BriefParagraph{Sentences: []BriefSentence{{ID: "a", Text: "According to the research, it fell."}}}) {
		t.Error("in brief sentence should fail")
	}
	if !src.briefOK(BriefParagraph{Sentences: []BriefSentence{{ID: "a", Text: "It fell."}}}) {
		t.Error("clean sentence should pass")
	}
	if src.leadsOK(FrontPage{Leads: []FrontLead{{ID: "a", Headline: "H", Paragraphs: []string{"The research says so."}}}, CoverLine: "C"}) {
		t.Error("lead should fail")
	}
	if src.leadsOK(FrontPage{Leads: []FrontLead{{ID: "a", Headline: "H", Paragraphs: []string{"Fine."}}}, CoverLine: "Per the research"}) {
		t.Error("cover line should fail")
	}
}
