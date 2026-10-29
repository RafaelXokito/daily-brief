package brief

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Whole runs of five topics against the realistic API when results run
// larger than the 30k assumed at first. A large result raises every later
// search's reserve (calibration only grows), so passes are allocated, and
// started, by what their first search needs with the run's calibration and
// the window they really send, a top-up's prior digest included. No pass
// planned to search runs without a search, no topic is done without one,
// the run never overshoots, it spends at least 80% at the baseline with
// results up to 45k, and twice the budget spends no less of it, with more
// searches.
//
// With every result at 60k, each search reserves about 355k, so up to that
// much is left unspendable after the last search that fits: a run spends
// about 77-79% at the baseline (documented in the README), and is held
// to 75% here.
func TestRunKeepsSearchingAfterLargeResults(t *testing.T) {
	base := Baseline(testConfig("anthropic", 0, "A", "B", "C", "D", "E"))
	for _, c := range []struct {
		name    string
		results []int64
		least   float64 // share of the baseline a run must spend, in percent
	}{
		{"typical", []int64{10_000, 30_000, 20_000}, 80},
		{"one 45k", []int64{20_000, 45_000, 15_000, 20_000, 25_000, 20_000}, 80},
		{"all 45k", []int64{45_000}, 80},
		{"all 60k", []int64{60_000}, 75},
	} {
		var baseUse float64
		var baseSearches int64
		for _, budget := range []int64{base, 2 * base} {
			sim := &realistic{results: c.results}
			b, reqs := scriptedAPI(t, apiBackend{}, sim.handle)
			cfg := testConfig("anthropic", budget, "A", "B", "C", "D", "E")
			b.cfg = cfg
			j, ups := runWithin(t, context.Background(), cfg, b, 60*time.Second)
			label := fmt.Sprintf("%s results, budget %s", c.name, HumanTokens(budget))
			checkRun(t, label, cfg, j, nil, ups)
			stalled := 0
			rewrites := map[string]int{}
			for _, r := range reqs() {
				first, _ := json.Marshal(r.Body["messages"].([]any)[0])
				// A pass planned with searches whose first request could not offer one.
				if strings.Contains(string(first), "Use web search to find") && len(r.Messages) == 1 && !toolsOn(r) {
					stalled++
				}
				if _, topic, ok := strings.Cut(string(first), "Topic: "); ok && strings.Contains(string(first), "Do not search") {
					rewrites[topic[:1]]++
				}
			}
			for topic, n := range rewrites {
				if n > 1 {
					t.Errorf("%s: topic %s got %d rewrite-only passes, want at most one", label, topic, n)
				}
			}
			var searches int64
			for _, s := range j.Sections {
				searches += s.Searches
				if s.Status == StatusDone && s.Searches == 0 {
					t.Errorf("%s: topic %s is done with no web search", label, s.Topic)
				}
			}
			u := j.Utilization()
			t.Logf("%s: %.1f%% used, %d searches, overshoot %d", label, u, searches, j.Overshoot)
			if j.Overshoot != 0 || j.TokensUsed() > budget {
				t.Errorf("%s: spent %d, overshoot %d", label, j.TokensUsed(), j.Overshoot)
			}
			if stalled > 0 {
				t.Errorf("%s: %d passes planned to search ran without a search", label, stalled)
			}
			if budget == base {
				baseUse, baseSearches = u, searches
				if u < c.least {
					t.Errorf("%s: only %.1f%% used at the baseline, want %.0f%%", label, u, c.least)
				}
				continue
			}
			if u < baseUse || searches <= baseSearches {
				t.Errorf("%s: %.1f%% used in %d searches, against %.1f%% in %d at the baseline", label, u, searches, baseUse, baseSearches)
			}
		}
	}
}
