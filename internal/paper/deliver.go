package paper

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/RafaelXokito/daily-brief/internal/dropbox"
)

// Deliverer puts a finished book where the reader's device finds it.
// A non-fatal problem after the book arrived is a warning, not an error.
type Deliverer interface {
	Deliver(ctx context.Context, name string, data []byte) (warnings []string, err error)
}

type dropboxDeliverer struct {
	c      *dropbox.Client
	folder string
	title  string
}

// NewDropboxDeliverer uploads the book to folder, then deletes the older
// editions of title it finds there. A failed delete is a warning: the new
// paper is already on Dropbox.
func NewDropboxDeliverer(c *dropbox.Client, folder, title string) Deliverer {
	return &dropboxDeliverer{c: c, folder: strings.TrimRight(folder, "/"), title: title}
}

func (d *dropboxDeliverer) path(name string) string { return d.folder + "/" + name }

func (d *dropboxDeliverer) Deliver(ctx context.Context, name string, data []byte) ([]string, error) {
	// A conflict means the file is already there, for instance after a
	// crash between upload and saving state: it counts as delivered.
	if err := d.c.Upload(ctx, d.path(name), data); err != nil && !errors.Is(err, dropbox.ErrConflict) {
		return nil, fmt.Errorf("paper: upload %s: %w", name, err)
	}
	names, err := d.c.List(ctx, d.folder)
	if err != nil {
		return []string{fmt.Sprintf("could not list %s to remove older editions: %v", d.folder, err)}, nil
	}
	var warnings []string
	for _, stale := range Stale(names, d.title, name) {
		if err := d.c.Delete(ctx, d.path(stale)); err != nil {
			warnings = append(warnings, fmt.Sprintf("could not delete older edition %s: %v", stale, err))
		}
	}
	return warnings, nil
}
