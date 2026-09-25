package shopify

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var enqueuePattern = regexp.MustCompile(`streamController\.enqueue\(("(?:[^"\\]|\\.)*")\)`)

// decodeLoaderData extracts React Router's server-rendered loader data
// from a page. React Router streams it as "turbo-stream": a flat JSON
// array where every value is stored once and referenced by index —
// objects are {"_<keyIndex>": <valueIndex>}, arrays hold indices, and
// small negative numbers are sentinels (undefined/null/NaN/...). This
// rebuilds the plain tree and re-marshals it as ordinary JSON.
func decodeLoaderData(page string) (json.RawMessage, error) {
	var stream strings.Builder
	for _, m := range enqueuePattern.FindAllStringSubmatch(page, -1) {
		var chunk string
		if err := json.Unmarshal([]byte(m[1]), &chunk); err != nil {
			return nil, fmt.Errorf("decode stream chunk: %w", err)
		}
		stream.WriteString(chunk)
	}
	first, _, _ := strings.Cut(strings.TrimSpace(stream.String()), "\n")
	if first == "" {
		return nil, fmt.Errorf("no turbo-stream data in page")
	}

	var values []json.RawMessage
	if err := json.Unmarshal([]byte(first), &values); err != nil {
		return nil, fmt.Errorf("decode turbo-stream: %w", err)
	}
	d := &decoder{values: values, done: map[int]any{}}
	root, err := d.decode(0, 0)
	if err != nil {
		return nil, err
	}
	obj, ok := root.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("turbo-stream root is not an object")
	}
	return json.Marshal(obj["loaderData"])
}

type decoder struct {
	values []json.RawMessage
	done   map[int]any
}

func (d *decoder) decode(i, depth int) (any, error) {
	if i < 0 {
		return nil, nil // undefined/null/NaN/Infinity sentinels
	}
	if i >= len(d.values) {
		return nil, fmt.Errorf("turbo-stream index %d out of range", i)
	}
	if depth > 64 {
		return nil, nil // cycle guard; job data is nowhere near this deep
	}
	if v, ok := d.done[i]; ok {
		return v, nil
	}

	raw := d.values[i]
	switch {
	case len(raw) > 0 && raw[0] == '{':
		var refs map[string]int
		if err := json.Unmarshal(raw, &refs); err != nil {
			return nil, nil // not a reference object — ignore
		}
		out := make(map[string]any, len(refs))
		d.done[i] = out
		for k, vi := range refs {
			ki := 0
			if _, err := fmt.Sscanf(k, "_%d", &ki); err != nil {
				continue
			}
			var key string
			if err := json.Unmarshal(d.values[ki], &key); err != nil {
				continue
			}
			v, err := d.decode(vi, depth+1)
			if err != nil {
				return nil, err
			}
			out[key] = v
		}
		return out, nil
	case len(raw) > 0 && raw[0] == '[':
		var elems []json.RawMessage
		if err := json.Unmarshal(raw, &elems); err != nil {
			return nil, err
		}
		// Typed values (dates, promises, ...) start with a string tag.
		if len(elems) > 0 && len(elems[0]) > 0 && elems[0][0] == '"' {
			return nil, nil
		}
		out := make([]any, 0, len(elems))
		d.done[i] = out
		for _, e := range elems {
			var vi int
			if err := json.Unmarshal(e, &vi); err != nil {
				continue
			}
			v, err := d.decode(vi, depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		d.done[i] = out
		return out, nil
	default:
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		d.done[i] = v
		return v, nil
	}
}
