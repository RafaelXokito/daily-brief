package paper

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RafaelXokito/daily-brief/internal/dropbox"
)

type fakeDropbox struct {
	mu         sync.Mutex
	requests   []string // "upload <path>", "delete <path>"
	files      []string
	uploadCode int
	uploadBody string
	deleteCode int
	deleteBody string
	moveCode   int
	moveBody   string
	archive    []string // names listed in /A
	archiveErr int      // status for listing /A
	uploaded   []byte
}

func newFakeDropbox(t *testing.T, files []string) (*fakeDropbox, *dropbox.Client) {
	t.Helper()
	f := &fakeDropbox{files: files}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"access_token":"tok","expires_in":14400}`)
	})
	mux.HandleFunc("/content/files/upload", func(w http.ResponseWriter, r *http.Request) {
		var arg struct{ Path string }
		json.Unmarshal([]byte(r.Header.Get("Dropbox-API-Arg")), &arg)
		data, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.requests = append(f.requests, "upload "+arg.Path)
		f.uploaded = data
		if f.uploadCode != 0 {
			w.WriteHeader(f.uploadCode)
			io.WriteString(w, f.uploadBody)
			return
		}
		io.WriteString(w, `{}`)
	})
	mux.HandleFunc("/api/files/list_folder", func(w http.ResponseWriter, r *http.Request) {
		type entry struct {
			Tag  string `json:".tag"`
			Name string `json:"name"`
		}
		var arg struct{ Path string }
		json.NewDecoder(r.Body).Decode(&arg)
		names := f.files
		if arg.Path == "/A" {
			if f.archiveErr != 0 {
				w.WriteHeader(f.archiveErr)
				io.WriteString(w, `{"error_summary":"path/not_found/.."}`)
				return
			}
			names = f.archive
		}
		var es []entry
		for _, n := range names {
			es = append(es, entry{"file", n})
		}
		json.NewEncoder(w).Encode(map[string]any{"entries": es, "has_more": false})
	})
	mux.HandleFunc("/api/files/move_v2", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			From       string `json:"from_path"`
			To         string `json:"to_path"`
			Autorename bool   `json:"autorename"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.requests = append(f.requests, "move "+body.From+" -> "+body.To)
		if !body.Autorename {
			f.requests = append(f.requests, "NOT AUTORENAME")
		}
		if f.moveCode != 0 {
			w.WriteHeader(f.moveCode)
			b := f.moveBody
			if b == "" {
				b = `{"error_summary":"boom"}`
			}
			io.WriteString(w, b)
			return
		}
		io.WriteString(w, `{}`)
	})
	mux.HandleFunc("/api/files/delete_v2", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Path string }
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.requests = append(f.requests, "delete "+body.Path)
		if f.deleteCode != 0 {
			w.WriteHeader(f.deleteCode)
			body := f.deleteBody
			if body == "" {
				body = `{"error_summary":"boom"}`
			}
			io.WriteString(w, body)
			return
		}
		io.WriteString(w, `{}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return f, &dropbox.Client{
		AppKey: "k", RefreshToken: "r", HTTP: srv.Client(),
		Sleep:    func(context.Context, time.Duration) error { return nil },
		TokenURL: srv.URL + "/token", APIBase: srv.URL + "/api", ContentBase: srv.URL + "/content",
	}
}

const (
	testTitle = "The Daily Brief"
	testName  = "The Daily Brief 2026-11-25.epub"
)

var existing = []string{
	"How to add books.pdf",
	"The Daily Brief 2026-11-23.epub",
	"The Daily Brief 2026-11-24.epub",
	"The Daily Brief 2026-11-25.epub",
	"My Book.epub",
}

func TestDelivererUploadsThenMoves(t *testing.T) {
	f, c := newFakeDropbox(t, existing)
	warns, err := NewDropboxDeliverer(c, "/Apps/Rakuten Kobo/", testTitle, "/A", 0).Deliver(context.Background(), "The Daily Brief 2026-11-25 (2).epub", []byte("book"))
	if err != nil || len(warns) != 0 {
		t.Fatalf("warns %v err %v", warns, err)
	}
	if string(f.uploaded) != "book" {
		t.Errorf("uploaded %q, want %q", f.uploaded, "book")
	}
	want := []string{
		"upload /Apps/Rakuten Kobo/The Daily Brief 2026-11-25 (2).epub",
		"move /Apps/Rakuten Kobo/The Daily Brief 2026-11-23.epub -> /A/The Daily Brief 2026-11-23.epub",
		"move /Apps/Rakuten Kobo/The Daily Brief 2026-11-24.epub -> /A/The Daily Brief 2026-11-24.epub",
		"move /Apps/Rakuten Kobo/The Daily Brief 2026-11-25.epub -> /A/The Daily Brief 2026-11-25.epub",
	}
	if strings.Join(f.requests, "\n") != strings.Join(want, "\n") {
		t.Errorf("requests:\n%s\nwant:\n%s", strings.Join(f.requests, "\n"), strings.Join(want, "\n"))
	}
}

func TestDelivererNeverMovesTheUploadedFile(t *testing.T) {
	f, c := newFakeDropbox(t, existing)
	if _, err := NewDropboxDeliverer(c, "/F", testTitle, "/A", 0).Deliver(context.Background(), testName, []byte("b")); err != nil {
		t.Fatal(err)
	}
	if string(f.uploaded) != "b" {
		t.Errorf("uploaded %q", f.uploaded)
	}
	for _, r := range f.requests {
		if strings.HasPrefix(r, "move /F/"+testName) || strings.HasPrefix(r, "delete") {
			t.Errorf("touched the file just uploaded: %s", r)
		}
	}
}

func TestDelivererFailedUploadMovesNothing(t *testing.T) {
	f, c := newFakeDropbox(t, existing)
	f.uploadCode, f.uploadBody = 403, `{"error_summary":"insufficient_space"}`
	warns, err := NewDropboxDeliverer(c, "/F", testTitle, "/A", 0).Deliver(context.Background(), testName, []byte("b"))
	if err == nil {
		t.Fatal("expected error")
	}
	if len(warns) != 0 {
		t.Errorf("warnings %v", warns)
	}
	for _, r := range f.requests {
		if strings.HasPrefix(r, "delete") || strings.HasPrefix(r, "move") {
			t.Errorf("move or delete after failed upload: %v", f.requests)
		}
	}
}

func TestDelivererConflictCountsAsDelivered(t *testing.T) {
	f, c := newFakeDropbox(t, existing)
	f.uploadCode, f.uploadBody = 409, `{"error_summary":"path/conflict/file/.."}`
	warns, err := NewDropboxDeliverer(c, "/F", testTitle, "/A", 0).Deliver(context.Background(), "The Daily Brief 2026-11-25 (2).epub", []byte("b"))
	if err != nil || len(warns) != 0 {
		t.Fatalf("warns %v err %v", warns, err)
	}
	n := 0
	for _, r := range f.requests {
		if strings.HasPrefix(r, "move") {
			n++
		}
	}
	if n != 3 {
		t.Errorf("stale archiving must still run after a conflict: %v", f.requests)
	}
}

func TestDelivererMoveFailureIsNotFatal(t *testing.T) {
	f, c := newFakeDropbox(t, existing)
	f.moveCode = 500
	warns, err := NewDropboxDeliverer(c, "/F", testTitle, "/A", 0).Deliver(context.Background(), "The Daily Brief 2026-11-25 (2).epub", []byte("b"))
	if err != nil {
		t.Fatalf("delete failure must not fail the delivery: %v", err)
	}
	if len(warns) != 3 {
		t.Fatalf("warnings %v, want one per stale file", warns)
	}
	if len(warns) == 0 {
		t.Fatal("expected a warning")
	}
	if !strings.Contains(warns[0], "2026-11-23") {
		t.Errorf("warning should name the file: %q", warns[0])
	}
}

func TestDelivererListFailureIsAWarning(t *testing.T) {
	_, c := newFakeDropbox(t, nil)
	c.APIBase = c.APIBase + "/nope"
	warns, err := NewDropboxDeliverer(c, "/F", testTitle, "/A", 0).Deliver(context.Background(), testName, []byte("b"))
	if err != nil || len(warns) == 0 {
		t.Errorf("warns %v err %v", warns, err)
	}
}

func TestDelivererMoveNotFoundIsSuccess(t *testing.T) {
	f, c := newFakeDropbox(t, existing)
	f.moveCode, f.moveBody = 409, `{"error_summary":"from_lookup/not_found/.."}`
	warns, err := NewDropboxDeliverer(c, "/F", testTitle, "/A", 0).Deliver(context.Background(), "The Daily Brief 2026-11-25 (2).epub", []byte("b"))
	if err != nil || len(warns) != 0 {
		t.Errorf("already-gone file must not warn: warns %v err %v", warns, err)
	}
}

func fixedClock(d *dropboxDeliverer) {
	d.now = func() time.Time { return time.Date(2026, 11, 25, 7, 0, 0, 0, time.Local) }
}

func requestsWithPrefix(f *fakeDropbox, prefix string) []string {
	var out []string
	for _, r := range f.requests {
		if strings.HasPrefix(r, prefix) {
			out = append(out, r)
		}
	}
	return out
}

func TestDelivererRetentionDeletesOnlyOldMatchingFiles(t *testing.T) {
	f, c := newFakeDropbox(t, existing)
	f.archive = []string{
		"The Daily Brief 2026-10-01.epub",
		"The Daily Brief 2026-10-01 (1).epub",
		"The Daily Brief 2026-11-20.epub",
		"My Book.epub",
		"The Daily Brief 2026-01-01.pdf",
		"Other 2026-01-01.epub",
	}
	d := NewDropboxDeliverer(c, "/F", testTitle, "/A", 30).(*dropboxDeliverer)
	fixedClock(d)
	warns, err := d.Deliver(context.Background(), "The Daily Brief 2026-11-25 (2).epub", []byte("b"))
	if err != nil || len(warns) != 0 {
		t.Fatalf("warns %v err %v", warns, err)
	}
	got := requestsWithPrefix(f, "delete")
	want := []string{
		"delete /A/The Daily Brief 2026-10-01.epub",
		"delete /A/The Daily Brief 2026-10-01 (1).epub",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("deletes %v, want %v", got, want)
	}
	// moves happen before retention deletes
	lastMove, firstDelete := -1, -1
	for i, r := range f.requests {
		if strings.HasPrefix(r, "move") {
			lastMove = i
		}
		if strings.HasPrefix(r, "delete") && firstDelete < 0 {
			firstDelete = i
		}
	}
	if lastMove > firstDelete {
		t.Errorf("order: %v", f.requests)
	}
}

func TestDelivererArchiveDaysZeroDeletesNothing(t *testing.T) {
	f, c := newFakeDropbox(t, existing)
	f.archive = []string{"The Daily Brief 2020-01-01.epub"}
	d := NewDropboxDeliverer(c, "/F", testTitle, "/A", 0).(*dropboxDeliverer)
	fixedClock(d)
	if _, err := d.Deliver(context.Background(), testName, []byte("b")); err != nil {
		t.Fatal(err)
	}
	if got := requestsWithPrefix(f, "delete"); len(got) != 0 {
		t.Errorf("deleted %v", got)
	}
}

func TestDelivererMissingArchiveFolderIsNotAWarning(t *testing.T) {
	f, c := newFakeDropbox(t, existing)
	f.archiveErr = 409
	d := NewDropboxDeliverer(c, "/F", testTitle, "/A", 30).(*dropboxDeliverer)
	fixedClock(d)
	warns, err := d.Deliver(context.Background(), testName, []byte("b"))
	if err != nil || len(warns) != 0 {
		t.Errorf("warns %v err %v", warns, err)
	}
}

func TestDelivererRetentionFailuresAreWarnings(t *testing.T) {
	f, c := newFakeDropbox(t, nil)
	f.archive = []string{"The Daily Brief 2026-01-01.epub"}
	f.deleteCode = 500
	d := NewDropboxDeliverer(c, "/F", testTitle, "/A", 30).(*dropboxDeliverer)
	fixedClock(d)
	warns, err := d.Deliver(context.Background(), testName, []byte("b"))
	if err != nil || len(warns) != 1 || !strings.Contains(warns[0], "2026-01-01") {
		t.Errorf("warns %v err %v", warns, err)
	}
	f2, c2 := newFakeDropbox(t, nil)
	f2.archiveErr = 500
	d2 := NewDropboxDeliverer(c2, "/F", testTitle, "/A", 30).(*dropboxDeliverer)
	fixedClock(d2)
	warns, err = d2.Deliver(context.Background(), testName, []byte("b"))
	if err != nil || len(warns) != 1 {
		t.Errorf("list failure: warns %v err %v", warns, err)
	}
}
