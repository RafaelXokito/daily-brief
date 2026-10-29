package brief

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
)

type fakeTurn struct {
	text          string
	input, output int64
	searches      int64
	stop          string
}

// request is the part of a Messages request the tests inspect.
type request struct {
	MaxTokens int64             `json:"max_tokens"`
	Messages  []json.RawMessage `json:"messages"`
	Tools     []struct {
		MaxUses int64 `json:"max_uses"`
	} `json:"tools"`
	ToolChoice *struct {
		Type string `json:"type"`
	} `json:"tool_choice"`
}

// fakeAPI serves turns in order from a local server and records requests.
func fakeAPI(t *testing.T, turns ...fakeTurn) (*apiBackend, *[]request) {
	var reqs []request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		reqs = append(reqs, req)
		if len(reqs) > len(turns) {
			t.Errorf("unexpected request %d", len(reqs))
			w.WriteHeader(500)
			return
		}
		tr := turns[len(reqs)-1]
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"msg_%d","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":%q}],
			"stop_reason":%q,"usage":{"input_tokens":%d,"output_tokens":%d,"server_tool_use":{"web_search_requests":%d}}}`,
			len(reqs), tr.text, tr.stop, tr.input, tr.output, tr.searches)
	}))
	t.Cleanup(srv.Close)
	client := anthropic.NewClient(option.WithBaseURL(srv.URL), option.WithAPIKey("test"), option.WithMaxRetries(0))
	cfg := testConfig("anthropic", 0, "A")
	return &apiBackend{cfg: cfg, messages: client.Beta.Messages, name: "fake"}, &reqs
}

func TestAPIStaysWithinBudget(t *testing.T) {
	b, reqs := fakeAPI(t,
		fakeTurn{input: 20_000, output: 500, searches: 1, stop: "pause_turn"},
		fakeTurn{text: "### A\nx", input: 21_000, output: 1_500, stop: "end_turn"},
	)
	task := Task{Topic: b.cfg.Topics[0], Budget: 250_000, Searches: 4, Items: 5}
	res := b.Research(context.Background(), task)

	first, second := (*reqs)[0], (*reqs)[1]
	// 250k left: five iterations over the prompt and a 30k result, each
	// writing up to the search max_tokens, fit (about 202k). One search per
	// request, so its cost is measured before the next.
	if len(first.Tools) != 1 || first.Tools[0].MaxUses != 1 || first.MaxTokens != searchMaxTokens(5) {
		t.Fatalf("first turn tools = %+v, max_tokens %d; want max_uses 1 and %d", first.Tools, first.MaxTokens, searchMaxTokens(5))
	}
	// 229.5k left: five iterations over a 20.5k context and a 30k result
	// (about 282k) do not fit.
	if second.ToolChoice == nil || second.ToolChoice.Type != "none" {
		t.Fatalf("second turn should disable searching, tool_choice = %+v", second.ToolChoice)
	}
	if second.MaxTokens > 250_000-20_500-20_500 {
		t.Fatalf("max_tokens %d exceeds what is left", second.MaxTokens)
	}
	if len(*reqs) != 2 || res.Tokens > task.Budget || res.Markdown != "### A\nx" {
		t.Fatalf("%d requests, spent %d of %d, markdown %q", len(*reqs), res.Tokens, task.Budget, res.Markdown)
	}
}

// A search pass whose first search does not fit sends nothing and spends
// nothing, rather than write a digest without searching; a narrow pass
// writes without tools.
func TestAPISearchPassTooSmallSendsNothing(t *testing.T) {
	b, reqs := fakeAPI(t, fakeTurn{text: "### A\nx", input: 2_000, output: 1_000, stop: "end_turn"})
	res := b.Research(context.Background(), Task{Topic: b.cfg.Topics[0], Budget: 150_000, Searches: 1, Items: 3})
	if !errors.Is(res.Err, errNoSearchRoom) || res.Tokens != 0 || res.Markdown != "" || len(*reqs) != 0 {
		t.Fatalf("err %v, %d tokens, digest %q, %d requests; want errNoSearchRoom and nothing sent", res.Err, res.Tokens, res.Markdown, len(*reqs))
	}
	b.Research(context.Background(), Task{Topic: b.cfg.Topics[0], Budget: 25_000, Items: 3, Prior: "### A\nx"})
	if first := (*reqs)[0]; len(first.Tools) != 0 || first.MaxTokens > 25_000 {
		t.Fatalf("tools %d, max_tokens %d; want none and at most the budget", len(first.Tools), first.MaxTokens)
	}
}
