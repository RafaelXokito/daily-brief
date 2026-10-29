package brief

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"

	"github.com/rafaelpereira/daily-brief/internal/config"
)

const (
	// Share of a claude-code pass's budget planned for searches; the rest
	// covers the prompt, context resends and writing the digest.
	planFactor = 0.85
	// Context a pass's first request is planned with: system prompt, task
	// prompt, a prior digest and the search tool.
	planWindow = 5_000
	// Tokens one search's results are planned to add, the middle of the
	// usual 10-30k, and what a search request is planned to write.
	typicalResult = 20_000
	typicalOut    = 2_000
	// largeResult is a result size a run of some twenty searches may well
	// meet once. Every later search of the run then reserves for it, so the
	// API baseline is sized for it (see Baseline).
	largeResult = 45_000
	// Tokens to write a digest on top of its searches.
	writeOverhead = 10_000
	// Most tokens set aside for the overview.
	maxOverviewReserve = 15_000
	// Least budget for a narrow pass, which rewrites a digest without
	// searching.
	narrowMin = 8_000
	// Least share of the budget a run aims to spend: on the API, less,
	// since each search must fit a worst case several times what it
	// usually costs, and what is left after the last one stays unspent.
	targetShare    = 0.85
	apiTargetShare = 0.80
	// Narrow passes run when a run would end below this share.
	narrowBelow = 0.9

	wordsPerItem  = 45 // headline plus at most two tight sentences
	overviewWords = 90 // 3-5 one-line bullets, enforced by capOverview
	// Words a Related label may add to a topic beside its items.
	relatedAllowance   = 3
	maxOverviewBullets = 5
	readingWPM         = 230
	minTopicItems      = 2
	maxTopicItems      = 10
	minSearchItems     = 3

	// Assumed share of spent tokens that are output, for cost estimates.
	outputShare = 0.1
	// Runs estimated above this many dollars get a warning: above the API
	// baseline's own cost at the default prices (about $7.50).
	costWarnUSD = 8
)

// costPerSearch is the rough end-to-end token cost of one web search,
// including the results fed back to the model.
func costPerSearch(provider string) int64 {
	if provider == "claude-code" {
		// Claude Code's WebSearch runs its own model call over the results
		// (measured ~45k per search end to end).
		return 45_000
	}
	// The first search of a pass reads the context twice and the results
	// once, then writes the digest. Later searches in the same pass cost
	// more, since the context grows by each search's results.
	return 2*planWindow + typicalResult + typicalOut
}

func target(provider string) float64 {
	if provider == "claude-code" {
		return targetShare
	}
	return apiTargetShare
}

func recommendedSearches(provider string) int64 {
	if provider == "claude-code" {
		return 3
	}
	return 4
}

// passCost is the least budget worth starting a research or top-up pass
// with. For claude-code that is one search plus writing the digest; the
// API backend offers a search only when its worst case fits.
func passCost(provider string) int64 {
	if provider == "claude-code" {
		return costPerSearch(provider) + writeOverhead
	}
	return searchReserve(searchIterations, planWindow, initialResult, searchMaxTokens(maxTopicItems))
}

// apiPlan follows the API backend's search loop over a pass of budget,
// making at most limit searches, with typical results: it returns how many
// searches the loop's reservations allow and what they are expected to
// spend. The context grows by each search's results and rewritten digest,
// so each search costs more than the one before.
func apiPlan(budget int64, items int, limit int64) (searches, spent int64) {
	c, o := int64(planWindow), searchMaxTokens(items)
	more := estimateTokens(continuePrompt(budget, limit))
	for searches < min(limit, maxRequests) && searchReserve(searchIterations, c, initialResult, o) <= budget-spent {
		spent += 2*c + typicalResult + typicalOut
		c += typicalResult + typicalOut + more
		searches++
	}
	return searches, spent
}

// planSpend is what a pass of t is expected to spend and how many
// searches it makes, by the plan its backend follows.
func planSpend(provider string, t Task) (tokens, searches int64) {
	write := min(t.Budget, planWindow+digestTokens(t.Items)) // a pass that only writes
	switch {
	case t.Searches == 0:
		return write, 0
	case provider == "claude-code":
		return min(t.Budget, t.Searches*costPerSearch(provider)+writeOverhead), t.Searches
	}
	if searches, spent := apiPlan(t.Budget, t.Items, t.Searches); searches > 0 {
		return spent, searches
	}
	return write, 0
}

// margin is held back beside a pass's allocation for the overshoot of a
// pass that cannot be stopped mid-run: claude-code's, by 10% plus one search
// when it may search. The API backend enforces its allocation per request.
func margin(provider string, alloc int64, search bool) int64 {
	if provider != "claude-code" {
		return 0
	}
	m := alloc / 10
	if search {
		m += costPerSearch(provider)
	}
	return m
}

// spendable is the most n passes may be allocated in total out of room,
// leaving each its margin.
func spendable(provider string, room int64, n int, search bool) int64 {
	if provider != "claude-code" {
		return room
	}
	if search {
		room -= int64(n) * costPerSearch(provider)
	}
	return room * 10 / 11
}

// passNeed is the least free budget a search pass can start with.
func passNeed(provider string) int64 {
	return passCost(provider) + margin(provider, passCost(provider), true)
}

func overviewReserve(budget int64) int64 {
	return min(maxOverviewReserve, budget/20)
}

// planSearches is how many searches a pass with budget is planned for,
// honouring an explicit max_searches_per_topic cap. On the API it is what
// the backend's search loop can afford; claude-code gets a search limit
// from its share.
func planSearches(cfg config.Config, budget int64) int64 {
	limit := cfg.MaxSearchesPerTopic
	if limit <= 0 {
		limit = math.MaxInt64
	}
	if cfg.Provider.Type == "claude-code" {
		return min(limit, max(1, int64(planFactor*float64(budget))/costPerSearch(cfg.Provider.Type)))
	}
	n, _ := apiPlan(budget, maxTopicItems, limit)
	return max(1, n)
}

// itemCeiling is how many items a pass with this many searches can
// support, before the reading cap applies.
func itemCeiling(searches int64) int {
	return int(min(maxTopicItems, max(minSearchItems, 2*searches)))
}

// totalItems is how many items fit in max_read_minutes beside the overview
// and each topic's Related label.
func totalItems(cfg config.Config) int {
	return max(0, (cfg.MaxReadMinutes*readingWPM-overviewWords-len(cfg.Topics)*relatedAllowance)/wordsPerItem)
}

// itemCaps splits the reading-time cap into a per-topic item cap: each
// topic gets a floor of 2 items (1 when that does not fit), and the rest is
// split by weight. When there are more topics than items, items go by weight
// and some topics get none.
//
// A run over some of the topics gets the caps of all of them, since it is
// merged into a journal that has the others.
func itemCaps(cfg config.Config) []int {
	if len(cfg.AllTopics) > len(cfg.Topics) {
		all := cfg
		all.Topics, all.AllTopics = cfg.AllTopics, nil
		allCaps := itemCaps(all)
		caps := make([]int, len(cfg.Topics))
		for i, t := range cfg.Topics {
			if k := slices.IndexFunc(all.Topics, func(a config.Topic) bool { return a.Name == t.Name }); k >= 0 {
				caps[i] = allCaps[k]
			}
		}
		return caps
	}
	total, n := totalItems(cfg), len(cfg.Topics)
	floor := minTopicItems
	for floor > 0 && n*floor > total {
		floor--
	}
	ws := make([]float64, n)
	for i, t := range cfg.Topics {
		ws[i] = weight(t)
	}
	caps := apportion(total-n*floor, ws)
	for i := range caps {
		caps[i] = min(maxTopicItems, caps[i]+floor)
	}
	return caps
}

// topicCaps maps every configured topic, not only those a run covers, to
// its item cap.
func topicCaps(cfg config.Config) map[string]int {
	if len(cfg.AllTopics) > len(cfg.Topics) {
		cfg.Topics, cfg.AllTopics = cfg.AllTopics, nil
	}
	caps := map[string]int{}
	for i, c := range itemCaps(cfg) {
		caps[cfg.Topics[i].Name] = c
	}
	return caps
}

// apportion splits n units by weight with the largest remainder method.
func apportion(n int, ws []float64) []int {
	out := make([]int, len(ws))
	var sum float64
	for _, w := range ws {
		sum += w
	}
	if n <= 0 || sum <= 0 {
		return out
	}
	rem := make([]float64, len(ws))
	left := n
	for i, w := range ws {
		x := float64(n) * w / sum
		out[i] = int(x)
		rem[i] = x - float64(out[i])
		left -= out[i]
	}
	order := make([]int, len(ws))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(rem[b], rem[a]) })
	for k := 0; k < left; k++ {
		out[order[k%len(order)]]++
	}
	return out
}

func weight(t config.Topic) float64 {
	if t.Weight == 0 {
		return 1
	}
	return t.Weight
}

// activeWeights are the topics' weights, 0 for topics the reading cap
// leaves no items.
func activeWeights(cfg config.Config, caps []int) ([]float64, float64) {
	ws := make([]float64, len(cfg.Topics))
	var total float64
	for i, t := range cfg.Topics {
		if caps[i] > 0 {
			ws[i] = weight(t)
			total += ws[i]
		}
	}
	return ws, total
}

// Baseline is the recommended token budget for cfg's provider and topics.
// Weights only split it.
//
// It starts from each topic's planned searches plus writing. On the API a
// search needs its worst case free, so it is at least one pass cost per
// topic, and at least what keeps a run's unspendable end under 20%: up to
// one search's reserve when the last search that fits is done, plus the
// overview reserve. The reserve grows with the largest result a run
// measures, so it is sized for one large (45k) result, not the 30k
// assumed before any is measured. It then steps up by one search until a
// planned run spends at least the target share; on claude-code that covers
// the safety margin held beside each pass (one search plus 10%), which is
// never spent.
//
// A run over some of the configured topics gets SubsetBaseline.
func Baseline(cfg config.Config) int64 {
	if len(cfg.AllTopics) > len(cfg.Topics) {
		b, _ := SubsetBaseline(cfg)
		return b
	}
	p := cfg.Provider.Type
	var n int64
	for _, c := range itemCaps(cfg) {
		if c > 0 {
			n++
		}
	}
	perTopic := recommendedSearches(p)*costPerSearch(p) + writeOverhead
	base := n*perTopic + maxOverviewReserve
	if p != "claude-code" {
		floor := float64(unspendable()+maxOverviewReserve) / (1 - target(p))
		base = max(base, n*passCost(p)+maxOverviewReserve, int64(floor))
	}
	for b := base; b <= 4*base; b += costPerSearch(p) {
		cfg.TokenBudget = b
		if run, _ := planRun(cfg); float64(run.TokensUsed()) >= target(p)*float64(b) {
			return b
		}
	}
	return base
}

// SubsetBaseline is the baseline of a run over some of the configured
// topics (cfg.Topics of cfg.AllTopics): their weighted share of the
// baseline of all of them, which it also returns, raised to at least one
// search pass, with its margin, per topic and the overview reserve.
func SubsetBaseline(cfg config.Config) (subset, full int64) {
	all := cfg
	all.Topics, all.AllTopics = cfg.AllTopics, nil
	full = Baseline(all)
	ws, total := activeWeights(all, itemCaps(all))
	var share float64
	var n int64
	for i, t := range all.Topics {
		if ws[i] > 0 && slices.ContainsFunc(cfg.Topics, func(s config.Topic) bool { return s.Name == t.Name }) {
			share += ws[i]
			n++
		}
	}
	if total == 0 {
		return full, full
	}
	return max(int64(float64(full)*share/total), n*passNeed(cfg.Provider.Type)+maxOverviewReserve), full
}

// unspendable is the most an API run may be left with once no further
// search fits, after one large result has raised every later search's
// reserve: just under a fresh search's reserve at largeResult.
func unspendable() int64 {
	return searchReserve(searchIterations, planWindow, largeResult, searchMaxTokens(maxTopicItems))
}

// ResolveBudget replaces an auto (0) token budget with the baseline and
// reports whether it did.
func ResolveBudget(cfg *config.Config) bool {
	if cfg.TokenBudget != 0 {
		return false
	}
	cfg.TokenBudget = Baseline(*cfg)
	return true
}

// split returns the overview reserve and each topic's initial share.
func split(cfg config.Config) (int64, []int64) {
	reserve := overviewReserve(cfg.TokenBudget)
	ws, total := activeWeights(cfg, itemCaps(cfg))
	shares := make([]int64, len(cfg.Topics))
	for i, w := range ws {
		if w > 0 {
			shares[i] = int64(float64(cfg.TokenBudget-reserve) * w / total)
		}
	}
	return reserve, shares
}

type Level string

const (
	LevelOK       Level = "ok"
	LevelLow      Level = "low"
	LevelHigh     Level = "high"
	LevelVeryHigh Level = "very-high"
	// LevelCostly flags a budget whose only issue is its dollar cost.
	LevelCostly Level = "costly"
)

// TopicPlan is a topic's first pass. Budget is that pass's allocation, a
// share of what is free when it starts, so allocations may add up to more
// than the budget; Share is the topic's weighted share of the topic budget,
// and shares add up to it.
type TopicPlan struct {
	Name     string
	Share    int64
	Budget   int64
	Searches int64
	Items    int
}

// Assessment describes what a budget buys and whether it looks sensible.
type Assessment struct {
	Budget   int64
	Baseline int64
	// FullBaseline is set on a run over some of the configured topics: the
	// baseline of all of them, of which Baseline is the subset's share.
	FullBaseline int64
	Reserve      int64
	Level        Level
	Messages     []string
	Topics       []TopicPlan
	// Searches is planned for the first passes; RunSearches and Spend are
	// what a whole run, top-ups included, is expected to make and spend.
	Searches    int64
	RunSearches int64
	Spend       int64
	Items       int
	// ReadMinutes is the estimated reading time of a full journal.
	ReadMinutes float64
	// CostUSD is set for API providers; Subscription for claude-code.
	CostUSD      float64
	Subscription bool
}

// Assess estimates a run of cfg (whose budget must be resolved) and warns
// about budgets that are too low or high. Warnings never block a run.
func Assess(cfg config.Config) Assessment {
	p := cfg.Provider.Type
	reserve, shares := split(cfg)
	pool := cfg.TokenBudget - reserve
	a := Assessment{Budget: cfg.TokenBudget, Baseline: Baseline(cfg), Reserve: reserve, Level: LevelOK, Subscription: p == "claude-code"}
	if len(cfg.AllTopics) > len(cfg.Topics) {
		_, a.FullBaseline = SubsetBaseline(cfg)
	}
	caps := itemCaps(cfg)
	// Shares after claude-code's safety margin.
	scale := 0.0
	if pool > 0 {
		scale = max(0, float64(spendable(p, pool, 1, true))/float64(pool))
	}
	active := 0
	for _, c := range caps {
		if c > 0 {
			active++
		}
	}
	even := int64(0)
	if active > 0 {
		even = int64(scale * float64(pool) / float64(active))
	}
	// Searches, items and spend come from following a run with the plan
	// each backend follows; each topic shows its first pass.
	run, tasks := planRun(cfg)
	a.Spend = run.TokensUsed()
	var unread, weightThin []string
	budgetThin := false
	for i, t := range cfg.Topics {
		alloc := int64(scale * float64(shares[i]))
		switch {
		case caps[i] == 0:
			unread = append(unread, t.Name)
		case alloc < passCost(p) && even >= passCost(p):
			weightThin = append(weightThin, t.Name)
		case alloc < passCost(p):
			budgetThin = true
		}
		tp := TopicPlan{Name: t.Name, Share: shares[i], Budget: shares[i]}
		if k := slices.IndexFunc(tasks, func(task Task) bool { return task.Topic.Name == t.Name }); k >= 0 {
			tp.Budget = tasks[k].Budget
			_, tp.Searches = planSpend(p, tasks[k])
			tp.Items = tasks[k].Items
		}
		a.Topics = append(a.Topics, tp)
		a.Searches += tp.Searches
		a.RunSearches += run.Sections[i].Searches
		a.Items += countItems(run.Sections[i].Markdown)
	}
	a.ReadMinutes = float64(a.Items*wordsPerItem+overviewWords) / readingWPM
	if !a.Subscription {
		tokens := float64(a.Spend)
		a.CostUSD = tokens*(1-outputShare)*cfg.PriceInputPerMTok/1e6 +
			tokens*outputShare*cfg.PriceOutputPerMTok/1e6 +
			float64(a.RunSearches)*cfg.PricePer1KSearches/1000
	}

	ratio := float64(a.Budget) / float64(a.Baseline)
	switch {
	case ratio < 0.5 || budgetThin:
		a.Level = LevelLow
		a.Messages = append(a.Messages, fmt.Sprintf("budget is too low (%.0f%% of the %s baseline): topics will be thin or skipped", 100*ratio, HumanTokens(a.Baseline)))
	case ratio > 4:
		a.Level = LevelVeryHigh
		a.Messages = append(a.Messages, fmt.Sprintf("budget is very high (%.1f× the %s baseline): it costs much more for diminishing returns; the journal stays capped at ~%d min", ratio, HumanTokens(a.Baseline), cfg.MaxReadMinutes))
	case ratio > 2:
		a.Level = LevelHigh
		a.Messages = append(a.Messages, fmt.Sprintf("budget is high (%.1f× the %s baseline): costs more; research will be broader but the journal stays capped at ~%d min", ratio, HumanTokens(a.Baseline), cfg.MaxReadMinutes))
	}
	for _, name := range weightThin {
		a.Messages = append(a.Messages, fmt.Sprintf("topic %q has too small a weight for a research pass of its own; it is covered only from budget other topics leave over", name))
	}
	if total := totalItems(cfg); len(unread) > 0 {
		a.Messages = append(a.Messages, fmt.Sprintf("max_read_minutes %d leaves room for only %d items, so these topics are skipped: %s", cfg.MaxReadMinutes, total, strings.Join(unread, ", ")))
	} else if total < minTopicItems*len(cfg.Topics) {
		a.Messages = append(a.Messages, fmt.Sprintf("max_read_minutes %d leaves room for only %d items across %d topics, so some topics get a single item", cfg.MaxReadMinutes, total, len(cfg.Topics)))
	}
	marginWarned := margin(p, passCost(p), true) > 0 && pool >= passNeed(p) && pool < 2*passNeed(p)
	if marginWarned {
		a.Messages = append(a.Messages, fmt.Sprintf("budget funds only one search pass beside %s's safety margin (one search plus 10%% per pass), so a run will likely spend under %.0f%% of it", p, 100*target(p)))
	}
	capped := false
	if c := cfg.MaxSearchesPerTopic; c > 0 && active > 0 {
		perPass := planSpendCapped(p, c) + writeOverhead
		if most := int64(active) * (1 + int64(maxPasses(p))) * perPass; float64(most) < target(p)*float64(pool) {
			capped = true
			a.Messages = append(a.Messages, fmt.Sprintf("max_searches_per_topic %d limits each pass to about %s tokens, so most of this budget cannot be spent; set it to 0 (auto)", c, HumanTokens(perPass)))
		}
	}
	// On the API every search must fit its worst case, and on claude-code
	// each pass holds a safety margin, so the last of a budget may fund no
	// further search. Say so rather than promise the target share.
	if share := float64(a.Spend) / float64(a.Budget); !capped && !marginWarned && a.Level != LevelLow && share < target(p) {
		why := fmt.Sprintf("each search must fit its worst case (about %s on a fresh pass), and what is left after the last one that fits stays unspent", HumanTokens(passCost(p)))
		if a.Subscription {
			why = fmt.Sprintf("each pass holds back one search plus 10%% for overshoot, and what is left after the last pass that fits stays unspent (the %s baseline aims for %.0f%%)", HumanTokens(a.Baseline), 100*target(p))
		}
		a.Messages = append(a.Messages, fmt.Sprintf("a run will likely spend only about %.0f%% of this budget: %s", 100*share, why))
	}
	if a.CostUSD > costWarnUSD {
		if a.Level == LevelOK {
			a.Level = LevelCostly
		}
		a.Messages = append(a.Messages, fmt.Sprintf("estimated cost is about $%.2f per run", a.CostUSD))
	}
	return a
}

// planSpendCapped is what one pass of at most c searches spends at most.
func planSpendCapped(provider string, c int64) int64 {
	if provider == "claude-code" {
		return c * costPerSearch(provider)
	}
	_, spent := apiPlan(math.MaxInt64/4, maxTopicItems, c)
	return spent
}

// planBackend spends what the plan expects of each pass, so Assess can
// follow a whole run, top-ups and all, without calling Claude.
type planBackend struct {
	provider string
	mu       sync.Mutex
	tasks    []Task
}

func (p *planBackend) Research(_ context.Context, t Task) Result {
	p.mu.Lock()
	p.tasks = append(p.tasks, t)
	p.mu.Unlock()
	tokens, searches := planSpend(p.provider, t)
	return Result{Markdown: strings.Repeat("### Planned\nx\n\n", t.Items), Tokens: tokens, Searches: searches}
}

func (p *planBackend) Summarize(_ context.Context, system, input string) Result {
	return Result{Markdown: "- Planned", Tokens: estimateTokens(system+input) + 300}
}

func (p *planBackend) Name() string { return "plan" }

// planRun follows a run of cfg with planBackend, one pass at a time so the
// estimate does not depend on scheduling. It returns the journal and the
// passes in the order they ran.
func planRun(cfg config.Config) (*Journal, []Task) {
	cfg.Concurrency = 1
	b := &planBackend{provider: cfg.Provider.Type}
	g := NewGenerator(cfg, b)
	g.partial = false
	j := g.emptyJournal()
	updates := make(chan Update)
	go func() {
		for range updates {
		}
	}()
	g.Run(context.Background(), j, updates)
	return j, b.tasks
}

// HumanTokens formats a token count compactly, e.g. 64.2k.
func HumanTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	}
	return fmt.Sprint(n)
}

// readMinutes estimates how long text takes to read.
func readMinutes(text string) float64 {
	return math.Round(10*float64(countWords(text))/readingWPM) / 10
}
