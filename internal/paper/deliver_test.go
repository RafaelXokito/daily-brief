package paper

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/RafaelXokito/daily-brief/internal/dropbox"
)

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

type fakeDropbox struct {
	mu          sync.Mutex
	requests    []string // "upload <path>", "delete <path>"
	files       []string
	uploadCode  int
	uploadBody  string
	deleteCode  int
	uploadedLen int
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
		f.uploadedLen = len(data)
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
		var es []entry
		for _, n := range f.files {
			es = append(es, entry{"file", n})
		}
		json.NewEncoder(w).Encode(map[string]any{"entries": es, "has_more": false})
	})
	mux.HandleFunc("/api/files/delete_v2", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Path string }
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.requests = append(f.requests, "delete "+body.Path)
		if f.deleteCode != 0 {
			w.WriteHeader(f.deleteCode)
			io.WriteString(w, `{"error_summary":"boom"}`)
			return
		}
		io.WriteString(w, `{}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return f, &dropbox.Client{
		AppKey: "k", RefreshToken: "r", HTTP: srv.Client(),
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

func TestDelivererUploadsThenDeletes(t *testing.T) {
	f, c := newFakeDropbox(t, existing)
	warns, err := NewDropboxDeliverer(c, "/Apps/Rakuten Kobo/", testTitle).Deliver(context.Background(), "The Daily Brief 2026-11-25 (2).epub", []byte("book"))
	if err != nil || len(warns) != 0 {
		t.Fatalf("warns %v err %v", warns, err)
	}
	want := []string{
		"upload /Apps/Rakuten Kobo/The Daily Brief 2026-11-25 (2).epub",
		"delete /Apps/Rakuten Kobo/The Daily Brief 2026-11-23.epub",
		"delete /Apps/Rakuten Kobo/The Daily Brief 2026-11-24.epub",
		"delete /Apps/Rakuten Kobo/The Daily Brief 2026-11-25.epub",
	}
	if strings.Join(f.requests, "\n") != strings.Join(want, "\n") {
		t.Errorf("requests:\n%s\nwant:\n%s", strings.Join(f.requests, "\n"), strings.Join(want, "\n"))
	}
}

func TestDelivererNeverDeletesTheUploadedFile(t *testing.T) {
	f, c := newFakeDropbox(t, existing)
	if _, err := NewDropboxDeliverer(c, "/F", testTitle).Deliver(context.Background(), testName, []byte("b")); err != nil {
		t.Fatal(err)
	}
	for _, r := range f.requests {
		if r == "delete /F/"+testName {
			t.Errorf("deleted the file just uploaded")
		}
	}
}

func TestDelivererFailedUploadMakesNoDelete(t *testing.T) {
	f, c := newFakeDropbox(t, existing)
	f.uploadCode, f.uploadBody = 403, `{"error_summary":"insufficient_space"}`
	warns, err := NewDropboxDeliverer(c, "/F", testTitle).Deliver(context.Background(), testName, []byte("b"))
	if err == nil {
		t.Fatal("expected error")
	}
	if len(warns) != 0 {
		t.Errorf("warnings %v", warns)
	}
	for _, r := range f.requests {
		if strings.HasPrefix(r, "delete") {
			t.Errorf("delete after failed upload: %v", f.requests)
		}
	}
}

func TestDelivererConflictCountsAsDelivered(t *testing.T) {
	f, c := newFakeDropbox(t, existing)
	f.uploadCode, f.uploadBody = 409, `{"error_summary":"path/conflict/file/.."}`
	warns, err := NewDropboxDeliverer(c, "/F", testTitle).Deliver(context.Background(), "The Daily Brief 2026-11-25 (2).epub", []byte("b"))
	if err != nil || len(warns) != 0 {
		t.Fatalf("warns %v err %v", warns, err)
	}
	n := 0
	for _, r := range f.requests {
		if strings.HasPrefix(r, "delete") {
			n++
		}
	}
	if n != 3 {
		t.Errorf("stale deletion must still run after a conflict: %v", f.requests)
	}
}

func TestDelivererDeleteFailureIsNotFatal(t *testing.T) {
	f, c := newFakeDropbox(t, existing)
	f.deleteCode = 500
	warns, err := NewDropboxDeliverer(c, "/F", testTitle).Deliver(context.Background(), "The Daily Brief 2026-11-25 (2).epub", []byte("b"))
	if err != nil {
		t.Fatalf("delete failure must not fail the delivery: %v", err)
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
	warns, err := NewDropboxDeliverer(c, "/F", testTitle).Deliver(context.Background(), testName, []byte("b"))
	if err != nil || len(warns) == 0 {
		t.Errorf("warns %v err %v", warns, err)
	}
}
