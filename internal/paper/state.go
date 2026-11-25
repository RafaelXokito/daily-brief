package paper

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/RafaelXokito/daily-brief/internal/config"
)

const stateFile = "paper-state.json"

// State is what the paper run remembers between mornings.
type State struct {
	Issue     int    `json:"issue"`     // editions delivered so far
	Date      string `json:"date"`      // last edition built, YYYY-MM-DD
	File      string `json:"file"`      // its file name
	Rebuild   int    `json:"rebuild"`   // 0 for the first build of Date
	Delivered bool   `json:"delivered"` // whether that edition reached Dropbox
}

// ErrCorruptState is wrapped by the error LoadState returns when the state
// file did not parse. The file is moved aside to <name>.corrupt and the
// returned State is the zero one, so the caller can warn and carry on.
var ErrCorruptState = errors.New("paper: corrupt state")

// LoadState reads the state in dir. A missing file is the zero State.
func LoadState(dir string) (State, error) {
	var s State
	raw, err := os.ReadFile(filepath.Join(dir, stateFile))
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, fmt.Errorf("paper: read state: %w", err)
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		bad := filepath.Join(dir, stateFile)
		if rerr := os.Rename(bad, bad+".corrupt"); rerr != nil {
			return State{}, fmt.Errorf("paper: parse state: %w; could not set it aside: %v", err, rerr)
		}
		return State{}, fmt.Errorf("%w: moved to %s.corrupt: %v", ErrCorruptState, stateFile, err)
	}
	return s, nil
}

// SaveState writes the state to dir atomically (temp file, then rename).
func SaveState(dir string, s State) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("paper: save state: %w", err)
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, stateFile+".*.tmp")
	if err != nil {
		return fmt.Errorf("paper: save state: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("paper: save state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("paper: save state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("paper: save state: %w", err)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, stateFile)); err != nil {
		return fmt.Errorf("paper: save state: %w", err)
	}
	return nil
}

// Action is what a paper run does this morning.
type Action int

const (
	Build        Action = iota // research, edit, render, deliver a new edition
	Skip                       // today's edition is already delivered
	Redeliver                  // today's book is built but not delivered: resend it
	RebuildToday               // -refresh: a new edition for today under a new name
)

// Decide applies the delivery rule. localExists says whether the book
// named by s.File is still on disk.
func Decide(s State, today string, refresh, localExists bool) Action {
	switch {
	case s.Date != today:
		return Build
	case refresh:
		return RebuildToday
	case s.Delivered:
		return Skip
	case localExists:
		return Redeliver
	default:
		return Build
	}
}

func fileTitle(title string) string {
	if t := strings.TrimSpace(title); t != "" {
		return t
	}
	return config.DefaultPaperTitle
}

// FileName is the edition's file name: "<title> YYYY-MM-DD.epub", or
// "<title> YYYY-MM-DD (N).epub" for a rebuild N >= 1.
func FileName(title, date string, rebuild int) string {
	name := fileTitle(title) + " " + date
	if rebuild > 0 {
		name += " (" + strconv.Itoa(rebuild) + ")"
	}
	return name + ".epub"
}

// Stale picks, out of the names in the Dropbox folder, the older editions
// to delete: those matching daily-brief's own pattern for title, except
// keep. Anything else in the folder is never returned.
func Stale(names []string, title, keep string) []string {
	re := regexp.MustCompile(`^` + regexp.QuoteMeta(fileTitle(title)) + ` \d{4}-\d{2}-\d{2}( \([1-9]\d*\))?\.epub$`)
	var out []string
	for _, n := range names {
		if n != keep && re.MatchString(n) {
			out = append(out, n)
		}
	}
	return out
}

// Expired picks, out of the names in the archive folder, the editions older
// than days before today: those matching daily-brief's own pattern for title
// (including Dropbox's " (N)" autorename suffix) whose date in the name is
// before today minus days. days <= 0 keeps everything.
func Expired(names []string, title string, today time.Time, days int) []string {
	if days <= 0 {
		return nil
	}
	re := regexp.MustCompile(`^` + regexp.QuoteMeta(fileTitle(title)) + ` (\d{4}-\d{2}-\d{2})( \([1-9]\d*\))?\.epub$`)
	y, m, d := today.Date()
	cutoff := time.Date(y, m, d, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -days)
	var out []string
	for _, n := range names {
		match := re.FindStringSubmatch(n)
		if match == nil {
			continue
		}
		date, err := time.Parse("2006-01-02", match[1])
		if err == nil && date.Before(cutoff) {
			out = append(out, n)
		}
	}
	return out
}
