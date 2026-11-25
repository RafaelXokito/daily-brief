package paper

import (
	"fmt"

	"github.com/RafaelXokito/daily-brief/internal/brief"
	"github.com/RafaelXokito/daily-brief/internal/config"
	"github.com/RafaelXokito/daily-brief/internal/editor"
)

// MarginPercent is the claude-code margin for paper runs; resized in
// rollout step 2.
const MarginPercent = 30

// editorMargin is the editor's margin for provider: MarginPercent on
// claude-code, which cannot stop a pass part-way, and none elsewhere, as
// for the generator.
func editorMargin(provider string) int {
	if provider == "claude-code" {
		return MarginPercent
	}
	return 0
}

// Split divides a paper's token budget between research and the editor.
type Split struct{ Research, Editor int64 }

// SplitBudget reserves the editor's worst case, margin included, out of
// total and leaves the rest to research, which must be at least half of
// total.
func SplitBudget(total int64, cfg config.Config) (Split, error) {
	reserve := editor.Reserve(len(cfg.EffectiveTopics()), brief.MaxItems(cfg), editorMargin(cfg.Provider.Type))
	s := Split{Research: total - reserve, Editor: reserve}
	if s.Research*2 < total {
		return Split{}, fmt.Errorf("paper: token budget %d is too small: the editor needs %d, and research must keep at least half; the smallest budget that works is %d", total, reserve, 2*reserve)
	}
	return s, nil
}
