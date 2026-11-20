package http

import "net/http"

// ReplayEntry is one problem type of the service, as the external replay-class tests read it.
type ReplayEntry struct {
	Slug   string
	Status int
	Class  string
}

// ReplayCatalogue lists every problem type of the adapter (the registry newProblem fills), one
// entry per slug and status.
func ReplayCatalogue() []ReplayEntry {
	var out []ReplayEntry
	seen := map[string]bool{}
	for _, p := range registry {
		id := p.slug + http.StatusText(p.status)
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, ReplayEntry{Slug: p.slug, Status: p.status, Class: string(p.class)})
	}
	return out
}

// RefuseForTest answers the problem type (slug, status) through the production error writer: the
// typed error of the catalogue through writeError, any other problem through writeProblem, so the
// class marking the middleware reads is the production one.
func RefuseForTest(w http.ResponseWriter, r *http.Request, slug string, status int) {
	for _, e := range problemCatalogue {
		if e.p.slug == slug && e.p.status == status {
			writeError(w, r, e.err)
			return
		}
	}
	for _, p := range registry {
		if p.slug == slug && p.status == status {
			writeProblem(w, r, p, "refused for the test")
			return
		}
	}
	panic("no problem type " + slug)
}
