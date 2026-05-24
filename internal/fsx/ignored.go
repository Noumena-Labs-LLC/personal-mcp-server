package fsx

import "os"

type ignoredItems struct {
	counts      map[string]int
	samples     []map[string]any
	sampleLimit int
}

func newIgnoredItems(sampleLimit int) *ignoredItems {
	if sampleLimit <= 0 {
		sampleLimit = 20
	}
	return &ignoredItems{
		counts:      map[string]int{},
		samples:     make([]map[string]any, 0, sampleLimit),
		sampleLimit: sampleLimit,
	}
}

func (i *ignoredItems) add(reason string, sample map[string]any) {
	if i == nil || reason == "" {
		return
	}
	i.counts[reason]++
	if sample == nil || len(i.samples) >= i.sampleLimit {
		return
	}
	entry := map[string]any{"reason": reason}
	for k, v := range sample {
		entry[k] = v
	}
	i.samples = append(i.samples, entry)
}

func (i *ignoredItems) total() int {
	if i == nil {
		return 0
	}
	total := 0
	for _, count := range i.counts {
		total += count
	}
	return total
}

func (i *ignoredItems) export(out map[string]any) {
	if i == nil || out == nil {
		return
	}
	out["ignored_count"] = i.total()
	out["ignored_counts"] = i.counts
	out["ignored_samples"] = i.samples
}

func entryType(d os.DirEntry) string {
	if d.IsDir() {
		return "dir"
	}
	if d.Type()&os.ModeSymlink != 0 {
		return "symlink"
	}
	return "file"
}
