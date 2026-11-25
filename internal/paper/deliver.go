package paper

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/RafaelXokito/daily-brief/internal/dropbox"
)

// Deliverer puts a finished book where the reader's device finds it.
// A non-fatal problem after the book arrived is a warning, not an error.
type Deliverer interface {
	Deliver(ctx context.Context, name string, data []byte) (warnings []string, err error)
}

type dropboxDeliverer struct {
	c           *dropbox.Client
	folder      string
	title       string
	archive     string
	archiveDays int
	now         func() time.Time // test seam; time.Now when nil
}

// NewDropboxDeliverer uploads the book to folder, then moves the older
// editions of title it finds there to the archive folder (Dropbox renames on
// a name clash). With archiveDays > 0 it then deletes archived editions
// whose date is more than that many days old. A failed move or delete is a
// warning: the new paper is already on Dropbox.
func NewDropboxDeliverer(c *dropbox.Client, folder, title, archive string, archiveDays int) Deliverer {
	return &dropboxDeliverer{
		c: c, folder: strings.TrimRight(folder, "/"), title: title,
		archive: strings.TrimRight(archive, "/"), archiveDays: archiveDays,
	}
}

func (d *dropboxDeliverer) path(name string) string        { return d.folder + "/" + name }
func (d *dropboxDeliverer) archivePath(name string) string { return d.archive + "/" + name }

func (d *dropboxDeliverer) today() time.Time {
	if d.now != nil {
		return d.now()
	}
	return time.Now()
}

func (d *dropboxDeliverer) Deliver(ctx context.Context, name string, data []byte) ([]string, error) {
	// A conflict means the file is already there, for instance after a
	// crash between upload and saving state: it counts as delivered.
	if err := d.c.Upload(ctx, d.path(name), data); err != nil && !errors.Is(err, dropbox.ErrConflict) {
		return nil, fmt.Errorf("paper: upload %s: %w", name, err)
	}
	warnings := d.archiveStale(ctx, name)
	return append(warnings, d.prune(ctx)...), nil
}

func (d *dropboxDeliverer) archiveStale(ctx context.Context, keep string) []string {
	names, err := d.c.List(ctx, d.folder)
	if err != nil {
		return []string{fmt.Sprintf("could not list %s to archive older editions: %v", d.folder, err)}
	}
	var warnings []string
	for _, stale := range Stale(names, d.title, keep) {
		if err := d.c.Move(ctx, d.path(stale), d.archivePath(stale), true); err != nil && !errors.Is(err, dropbox.ErrNotFound) {
			warnings = append(warnings, fmt.Sprintf("could not archive older edition %s: %v", stale, err))
		}
	}
	return warnings
}

// prune deletes archived editions past the retention. Only daily-brief's own
// file names are ever deleted.
func (d *dropboxDeliverer) prune(ctx context.Context) []string {
	if d.archiveDays <= 0 {
		return nil
	}
	names, err := d.c.List(ctx, d.archive)
	if errors.Is(err, dropbox.ErrNotFound) {
		return nil
	}
	if err != nil {
		return []string{fmt.Sprintf("could not list %s to remove old archived editions: %v", d.archive, err)}
	}
	var warnings []string
	for _, old := range Expired(names, d.title, d.today(), d.archiveDays) {
		if err := d.c.Delete(ctx, d.archivePath(old)); err != nil && !errors.Is(err, dropbox.ErrNotFound) {
			warnings = append(warnings, fmt.Sprintf("could not delete archived edition %s: %v", old, err))
		}
	}
	return warnings
}
