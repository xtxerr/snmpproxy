package server

import (
	"sort"
	"strings"
	"sync"
)

// TagIndex provides efficient indexing for hierarchical tags.
// Tags are paths like "type/network/router" and support prefix queries.
//
// Example:
//
//	idx.Add("router-wan", []string{"type/network/router", "location/dc1"})
//	idx.Query("type/network")       // Returns all targets under type/network
//	idx.ListChildren("type")        // Returns ["network"] and any direct targets
type TagIndex struct {
	mu sync.RWMutex

	// tag -> set of targetIDs
	// Contains ALL prefixes: "type", "type/network", "type/network/router"
	tagToTargets map[string]map[string]struct{}

	// targetID -> explicit tags (not prefixes, just what was set)
	targetToTags map[string][]string
}

// NewTagIndex creates a new tag index.
func NewTagIndex() *TagIndex {
	return &TagIndex{
		tagToTargets: make(map[string]map[string]struct{}),
		targetToTags: make(map[string][]string),
	}
}

// Add registers a target with the given tags.
// Replaces any existing tags for this target.
func (idx *TagIndex) Add(targetID string, tags []string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	// Remove existing tags first
	idx.removeLocked(targetID)

	if len(tags) == 0 {
		return
	}

	// Store explicit tags
	idx.targetToTags[targetID] = make([]string, len(tags))
	copy(idx.targetToTags[targetID], tags)

	// Index all prefixes
	for _, tag := range tags {
		idx.addTagLocked(targetID, tag)
	}
}

// addTagLocked adds a single tag and all its prefixes to the index.
// Caller must hold the write lock.
func (idx *TagIndex) addTagLocked(targetID, tag string) {
	parts := strings.Split(tag, "/")
	for i := 1; i <= len(parts); i++ {
		prefix := strings.Join(parts[:i], "/")
		if idx.tagToTargets[prefix] == nil {
			idx.tagToTargets[prefix] = make(map[string]struct{})
		}
		idx.tagToTargets[prefix][targetID] = struct{}{}
	}
}

// Remove removes a target from the index.
func (idx *TagIndex) Remove(targetID string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.removeLocked(targetID)
}

// removeLocked removes a target from the index.
// Caller must hold the write lock.
func (idx *TagIndex) removeLocked(targetID string) {
	tags, ok := idx.targetToTags[targetID]
	if !ok {
		return
	}

	delete(idx.targetToTags, targetID)

	for _, tag := range tags {
		parts := strings.Split(tag, "/")
		for i := 1; i <= len(parts); i++ {
			prefix := strings.Join(parts[:i], "/")
			delete(idx.tagToTargets[prefix], targetID)
			// Clean up empty maps
			if len(idx.tagToTargets[prefix]) == 0 {
				delete(idx.tagToTargets, prefix)
			}
		}
	}
}

// UpdateTags updates tags for a target.
// - add: tags to add
// - remove: tags to remove
// - set: if non-empty, replaces all tags (add/remove ignored)
// Returns the new tag list.
func (idx *TagIndex) UpdateTags(targetID string, add, remove, set []string) []string {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	var newTags []string

	if len(set) > 0 {
		// Replace all tags
		newTags = make([]string, len(set))
		copy(newTags, set)
	} else {
		// Build from current + add - remove
		currentTags := idx.targetToTags[targetID]
		tagSet := make(map[string]bool)

		for _, t := range currentTags {
			tagSet[t] = true
		}
		for _, t := range remove {
			delete(tagSet, t)
		}
		for _, t := range add {
			if t != "" {
				tagSet[t] = true
			}
		}

		newTags = make([]string, 0, len(tagSet))
		for t := range tagSet {
			newTags = append(newTags, t)
		}
	}

	// Remove old index entries
	idx.removeLocked(targetID)

	// Add new entries
	if len(newTags) > 0 {
		sort.Strings(newTags)
		idx.targetToTags[targetID] = newTags
		for _, tag := range newTags {
			idx.addTagLocked(targetID, tag)
		}
	}

	return newTags
}

// GetTags returns the explicit tags for a target.
func (idx *TagIndex) GetTags(targetID string) []string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	tags := idx.targetToTags[targetID]
	if tags == nil {
		return nil
	}

	result := make([]string, len(tags))
	copy(result, tags)
	return result
}

// Query returns all target IDs matching the tag (prefix match).
// Example: Query("type/network") returns all targets tagged with
// "type/network", "type/network/router", "type/network/switch", etc.
func (idx *TagIndex) Query(tag string) []string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	targets := idx.tagToTargets[tag]
	result := make([]string, 0, len(targets))
	for id := range targets {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

// QueryAND returns targets matching ALL given tags.
func (idx *TagIndex) QueryAND(tags []string) []string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if len(tags) == 0 {
		return nil
	}

	// Start with smallest set for efficiency
	smallest := tags[0]
	smallestSize := len(idx.tagToTargets[tags[0]])
	for _, tag := range tags[1:] {
		if size := len(idx.tagToTargets[tag]); size < smallestSize {
			smallest = tag
			smallestSize = size
		}
	}

	if smallestSize == 0 {
		return nil
	}

	// Filter by all other tags
	var result []string
	for id := range idx.tagToTargets[smallest] {
		matchAll := true
		for _, tag := range tags {
			if _, ok := idx.tagToTargets[tag][id]; !ok {
				matchAll = false
				break
			}
		}
		if matchAll {
			result = append(result, id)
		}
	}

	sort.Strings(result)
	return result
}

// QueryOR returns targets matching ANY of the given tags.
func (idx *TagIndex) QueryOR(tags []string) []string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	seen := make(map[string]struct{})
	for _, tag := range tags {
		for id := range idx.tagToTargets[tag] {
			seen[id] = struct{}{}
		}
	}

	result := make([]string, 0, len(seen))
	for id := range seen {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

// Count returns the number of targets for a tag prefix.
func (idx *TagIndex) Count(tag string) int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.tagToTargets[tag])
}

// HasTag returns true if the target has the specified tag (or a child of it).
func (idx *TagIndex) HasTag(targetID, tag string) bool {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	targets := idx.tagToTargets[tag]
	_, ok := targets[targetID]
	return ok
}

// ListChildren returns child directories and targets directly at a path.
// For path "type/network", returns:
// - dirs: ["router", "switch"] (subdirectories)
// - targetIDs: targets tagged exactly with "type/network"
func (idx *TagIndex) ListChildren(prefix string) (dirs []string, targetIDs []string) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	dirSet := make(map[string]struct{})

	// Determine prefix for matching
	var prefixWithSlash string
	if prefix == "" {
		prefixWithSlash = ""
	} else {
		prefixWithSlash = prefix + "/"
	}

	// Find all unique next-level segments
	for tag := range idx.tagToTargets {
		var segment string

		if prefix == "" {
			// Root level: get first segment of each tag
			if slashIdx := strings.Index(tag, "/"); slashIdx > 0 {
				segment = tag[:slashIdx]
			} else {
				segment = tag
			}
		} else if strings.HasPrefix(tag, prefixWithSlash) {
			// Under this prefix: get next segment
			rest := tag[len(prefixWithSlash):]
			if rest == "" {
				continue
			}
			if slashIdx := strings.Index(rest, "/"); slashIdx > 0 {
				segment = rest[:slashIdx]
			} else {
				segment = rest
			}
		} else {
			continue
		}

		if segment != "" {
			dirSet[segment] = struct{}{}
		}
	}

	// Convert to sorted slice
	dirs = make([]string, 0, len(dirSet))
	for dir := range dirSet {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)

	// Find targets tagged exactly with this prefix
	for targetID, tags := range idx.targetToTags {
		for _, tag := range tags {
			if tag == prefix {
				targetIDs = append(targetIDs, targetID)
				break
			}
		}
	}
	sort.Strings(targetIDs)

	return dirs, targetIDs
}

// GetRootCategories returns all top-level tag categories.
func (idx *TagIndex) GetRootCategories() []string {
	dirs, _ := idx.ListChildren("")
	return dirs
}

// AllTargets returns all target IDs in the index.
func (idx *TagIndex) AllTargets() []string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	result := make([]string, 0, len(idx.targetToTags))
	for id := range idx.targetToTags {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

// Size returns the number of targets in the index.
func (idx *TagIndex) Size() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.targetToTags)
}
