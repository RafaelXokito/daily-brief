package paper

import (
	"crypto/sha256"
	"fmt"
	"slices"
	"sync"

	"github.com/RafaelXokito/daily-brief/internal/editor"
	"github.com/RafaelXokito/daily-brief/internal/epub"
	"github.com/RafaelXokito/daily-brief/internal/fetch"
)

const (
	// maxPictureBytes caps the pictures of one book together: 1.5 MiB.
	maxPictureBytes = 3 << 19
	// maxStoryPictures caps the pictures of stories the editor says need
	// one, beyond the chapter leads.
	maxStoryPictures = 6
)

// pictureJob is one story's picture: its file name, its preview image,
// and where it is shown.
type pictureJob struct {
	name, url      string
	front          int // index among the front page leads, or -1
	chapter, story int // the story in Paper.Chapters, or -1 when shown only on the front page
}

// pictureJobs lists, in priority order, the stories whose source has a
// preview image: the front page leads, the chapter leads, then the first
// maxStoryPictures stories that need a picture, in chapter order. A story
// is listed once, its picture named for its place in its chapter: chN.jpg
// for the lead of chapter N (numbered as Render numbers them), chN-K.jpg
// for that chapter's Kth story that needs one, and fN.jpg for front lead
// N whose story has no picture in its chapter.
func pictureJobs(p editor.Paper, images map[string]string) []pictureJob {
	inChapter, order, storyURL := chapterPictureJobs(p, images)
	var jobs []pictureJob
	listed := map[string]bool{}
	for fi, l := range p.Front.Leads {
		j, ok := inChapter[l.ID]
		if !ok {
			j = pictureJob{name: fmt.Sprintf("f%d.jpg", fi+1), url: images[storyURL[l.ID]], chapter: -1, story: -1}
		}
		if listed[l.ID] || j.url == "" {
			continue
		}
		listed[l.ID] = true
		j.front = fi
		jobs = append(jobs, j)
	}
	for _, id := range order {
		if !listed[id] {
			jobs = append(jobs, inChapter[id])
		}
	}
	return jobs
}

// chapterPictureJobs returns the pictures the chapters would show, by
// story id, their ids with the leads first and then the stories that need
// a picture, and every story's source URL by id.
func chapterPictureJobs(p editor.Paper, images map[string]string) (map[string]pictureJob, []string, map[string]string) {
	jobs, storyURL := map[string]pictureJob{}, map[string]string{}
	var leads, needs []string
	n := 0 // only chapters with stories are numbered
	for ci, c := range p.Chapters {
		if len(c.Stories) == 0 {
			continue
		}
		n++
		k := 0 // the chapter's stories that need a picture so far
		for si, s := range c.Stories {
			if _, dup := storyURL[s.ID]; dup {
				continue
			}
			storyURL[s.ID] = s.URL
			j := pictureJob{url: images[s.URL], front: -1, chapter: ci, story: si}
			switch {
			case j.url == "":
				continue
			case s.Placement == "lead":
				j.name = fmt.Sprintf("ch%d.jpg", n)
				leads = append(leads, s.ID)
			case s.NeedsPicture && len(needs) < maxStoryPictures:
				k++
				j.name = fmt.Sprintf("ch%d-%d.jpg", n, k)
				needs = append(needs, s.ID)
			default:
				continue
			}
			jobs[s.ID] = j
		}
	}
	return jobs, slices.Concat(leads, needs), storyURL
}

// pictures fetches the picture of each story pictureJobs lists, once per
// preview image and at most fetch.DefaultConcurrency at a time, and keeps
// them in priority order until one would take the book's pictures over
// maxPictureBytes; that one and the rest are left out. A picture that
// fails, or whose bytes repeat one already kept (a site's generic logo),
// is skipped. It returns a copy of p with the kept pictures' stories and
// front leads given their Image, the pictures, and how many stories had a
// preview image.
func (x *run) pictures(p editor.Paper, images map[string]string) (editor.Paper, []epub.Image, int) {
	jobs := pictureJobs(p, images)
	if len(jobs) == 0 {
		return p, nil, 0
	}
	data := x.downloadPictures(jobs)
	out := p
	out.Chapters = slices.Clone(p.Chapters)
	out.Front.Leads = slices.Clone(p.Front.Leads)
	var pics []epub.Image
	total := 0
	kept := map[[sha256.Size]byte]bool{}
	for _, j := range jobs {
		d := data[j.url]
		if len(d) == 0 {
			continue
		}
		sum := sha256.Sum256(d)
		if kept[sum] {
			continue
		}
		if total+len(d) > maxPictureBytes {
			break
		}
		kept[sum] = true
		total += len(d)
		showPicture(&out, j)
		pics = append(pics, epub.Image{Name: j.name, Data: d})
	}
	return out, pics, len(jobs)
}

// showPicture sets j's picture on its story and front lead in p, whose
// Chapters and Front.Leads are already copies; a story list is copied
// before it changes.
func showPicture(p *editor.Paper, j pictureJob) {
	if j.chapter >= 0 {
		stories := slices.Clone(p.Chapters[j.chapter].Stories)
		stories[j.story].Image = j.name
		p.Chapters[j.chapter].Stories = stories
	}
	if j.front >= 0 {
		p.Front.Leads[j.front].Image = j.name
	}
}

// downloadPictures fetches each distinct preview image of jobs once and
// returns the prepared pictures by URL; failures are absent.
func (x *run) downloadPictures(jobs []pictureJob) map[string][]byte {
	var urls []string
	for _, j := range jobs {
		if !slices.Contains(urls, j.url) {
			urls = append(urls, j.url)
		}
	}
	x.logf("fetching %d pictures", len(urls))
	var mu sync.Mutex
	out := map[string][]byte{}
	var wg sync.WaitGroup
	sem := make(chan struct{}, fetch.DefaultConcurrency)
	for _, u := range urls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if data, err := x.d.Picture(x.ctx, u); err == nil {
				mu.Lock()
				out[u] = data
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return out
}
