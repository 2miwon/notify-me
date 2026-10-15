package site

import (
	"errors"

	"github.com/2miwon/notify-me/internal/job"
)

// Multi combines several adapters for the same company — one per region
// when the source's own search only takes a single location — into one
// site. Results are merged and deduplicated by URL (a role posted to two
// regions is one posting). Errors are joined but never discard what the
// other parts fetched, matching the Adapter contract.
func Multi(name string, parts ...Adapter) Adapter {
	if len(parts) == 1 {
		return parts[0]
	}
	return &multi{name: name, parts: parts}
}

type multi struct {
	name  string
	parts []Adapter
}

func (m *multi) Name() string { return m.name }

func (m *multi) Fetch() ([]job.Posting, error) {
	var (
		out  []job.Posting
		errs []error
		seen = map[string]bool{}
	)
	for _, part := range m.parts {
		postings, err := part.Fetch()
		if err != nil {
			errs = append(errs, err)
		}
		for _, p := range postings {
			if seen[p.URL] {
				continue
			}
			seen[p.URL] = true
			p.Site = m.name
			out = append(out, p)
		}
	}
	return out, errors.Join(errs...)
}
