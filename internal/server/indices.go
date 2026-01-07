package server

import (
	"path"
	"sort"
	"strings"
	"sync"
)

// StringSet is a set of strings.
type StringSet map[string]struct{}

// NewStringSet creates a new StringSet.
func NewStringSet() StringSet {
	return make(StringSet)
}

// Add adds a string to the set.
func (s StringSet) Add(val string) {
	s[val] = struct{}{}
}

// Remove removes a string from the set.
func (s StringSet) Remove(val string) {
	delete(s, val)
}

// Contains checks if the set contains a string.
func (s StringSet) Contains(val string) bool {
	_, ok := s[val]
	return ok
}

// Len returns the size of the set.
func (s StringSet) Len() int {
	return len(s)
}

// ToSlice returns the set as a sorted slice.
func (s StringSet) ToSlice() []string {
	result := make([]string, 0, len(s))
	for v := range s {
		result = append(result, v)
	}
	sort.Strings(result)
	return result
}

// Clone creates a copy of the set.
func (s StringSet) Clone() StringSet {
	result := make(StringSet, len(s))
	for v := range s {
		result[v] = struct{}{}
	}
	return result
}

// Intersect returns the intersection of two sets.
func (s StringSet) Intersect(other StringSet) StringSet {
	// Iterate over smaller set
	small, large := s, other
	if len(small) > len(large) {
		small, large = large, small
	}

	result := make(StringSet)
	for v := range small {
		if large.Contains(v) {
			result.Add(v)
		}
	}
	return result
}

// Union returns the union of two sets.
func (s StringSet) Union(other StringSet) StringSet {
	result := s.Clone()
	for v := range other {
		result.Add(v)
	}
	return result
}

// ============================================================================
// Indices - Manages all target indices
// ============================================================================

// Indices manages secondary indices for fast target lookups.
type Indices struct {
	mu sync.RWMutex

	// Tag index (hierarchical)
	tags *TagIndex

	// State index: state -> targetIDs
	byState map[string]StringSet

	// Protocol index: protocol -> targetIDs
	byProtocol map[string]StringSet

	// Host index: host -> targetIDs (for glob matching)
	byHost map[string]StringSet
}

// NewIndices creates a new Indices instance.
func NewIndices() *Indices {
	return &Indices{
		tags:       NewTagIndex(),
		byState:    make(map[string]StringSet),
		byProtocol: make(map[string]StringSet),
		byHost:     make(map[string]StringSet),
	}
}

// Add adds a target to all indices.
func (idx *Indices) Add(id, state, protocol, host string, tags []string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	// State index
	if idx.byState[state] == nil {
		idx.byState[state] = NewStringSet()
	}
	idx.byState[state].Add(id)

	// Protocol index
	if idx.byProtocol[protocol] == nil {
		idx.byProtocol[protocol] = NewStringSet()
	}
	idx.byProtocol[protocol].Add(id)

	// Host index
	if idx.byHost[host] == nil {
		idx.byHost[host] = NewStringSet()
	}
	idx.byHost[host].Add(id)

	// Tag index (has its own lock)
	idx.tags.AddLocked(id, tags)
}

// Remove removes a target from all indices.
func (idx *Indices) Remove(id, state, protocol, host string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	// State index
	if set, ok := idx.byState[state]; ok {
		set.Remove(id)
		if set.Len() == 0 {
			delete(idx.byState, state)
		}
	}

	// Protocol index
	if set, ok := idx.byProtocol[protocol]; ok {
		set.Remove(id)
		if set.Len() == 0 {
			delete(idx.byProtocol, protocol)
		}
	}

	// Host index
	if set, ok := idx.byHost[host]; ok {
		set.Remove(id)
		if set.Len() == 0 {
			delete(idx.byHost, host)
		}
	}

	// Tag index
	idx.tags.RemoveLocked(id)
}

// UpdateState updates the state index when a target's state changes.
func (idx *Indices) UpdateState(id, oldState, newState string) {
	if oldState == newState {
		return
	}

	idx.mu.Lock()
	defer idx.mu.Unlock()

	// Remove from old state
	if set, ok := idx.byState[oldState]; ok {
		set.Remove(id)
		if set.Len() == 0 {
			delete(idx.byState, oldState)
		}
	}

	// Add to new state
	if idx.byState[newState] == nil {
		idx.byState[newState] = NewStringSet()
	}
	idx.byState[newState].Add(id)
}

// UpdateTags updates tags for a target.
func (idx *Indices) UpdateTags(id string, addTags, removeTags, setTags []string) []string {
	return idx.tags.UpdateTags(id, addTags, removeTags, setTags)
}

// ============================================================================
// Query Methods
// ============================================================================

// QueryByState returns all target IDs with the given state.
func (idx *Indices) QueryByState(state string) StringSet {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if set, ok := idx.byState[state]; ok {
		return set.Clone()
	}
	return NewStringSet()
}

// QueryByProtocol returns all target IDs with the given protocol.
func (idx *Indices) QueryByProtocol(protocol string) StringSet {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if set, ok := idx.byProtocol[protocol]; ok {
		return set.Clone()
	}
	return NewStringSet()
}

// QueryByHost returns all target IDs matching the host glob pattern.
func (idx *Indices) QueryByHost(pattern string) StringSet {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	result := NewStringSet()

	// If no wildcard, direct lookup
	if !strings.Contains(pattern, "*") && !strings.Contains(pattern, "?") {
		if set, ok := idx.byHost[pattern]; ok {
			return set.Clone()
		}
		return result
	}

	// Glob matching
	for host, set := range idx.byHost {
		if matched, _ := path.Match(pattern, host); matched {
			for id := range set {
				result.Add(id)
			}
		}
	}
	return result
}

// QueryByTag returns all target IDs with the given tag (prefix match).
func (idx *Indices) QueryByTag(tag string) StringSet {
	targets := idx.tags.Query(tag)
	result := NewStringSet()
	for _, id := range targets {
		result.Add(id)
	}
	return result
}

// QueryByTags returns target IDs matching ALL tags (AND).
func (idx *Indices) QueryByTags(tags []string) StringSet {
	if len(tags) == 0 {
		return NewStringSet()
	}

	targets := idx.tags.QueryAND(tags)
	result := NewStringSet()
	for _, id := range targets {
		result.Add(id)
	}
	return result
}

// ============================================================================
// Filter - Combined Query
// ============================================================================

// Filter holds filter criteria for target queries.
type Filter struct {
	Tags     []string // AND-combined
	State    string
	Protocol string
	Host     string // Glob pattern
}

// IsEmpty returns true if no filters are set.
func (f *Filter) IsEmpty() bool {
	return len(f.Tags) == 0 && f.State == "" && f.Protocol == "" && f.Host == ""
}

// Query returns target IDs matching all filter criteria.
// Uses index intersection starting with smallest result set.
func (idx *Indices) Query(f *Filter) StringSet {
	if f.IsEmpty() {
		return nil // Caller should use all targets
	}

	var result StringSet
	var initialized bool

	// Helper to intersect or initialize
	apply := func(set StringSet) {
		if set == nil || set.Len() == 0 {
			if initialized {
				result = NewStringSet() // Empty intersection
			}
			return
		}
		if !initialized {
			result = set
			initialized = true
		} else {
			result = result.Intersect(set)
		}
	}

	// Apply filters in order of expected selectivity
	if f.State != "" {
		apply(idx.QueryByState(f.State))
	}
	if f.Protocol != "" {
		apply(idx.QueryByProtocol(f.Protocol))
	}
	if len(f.Tags) > 0 {
		apply(idx.QueryByTags(f.Tags))
	}
	if f.Host != "" {
		apply(idx.QueryByHost(f.Host))
	}

	if !initialized {
		return nil
	}
	return result
}

// ============================================================================
// Tag Index Access
// ============================================================================

// TagIndex returns the underlying TagIndex for direct access.
func (idx *Indices) TagIndex() *TagIndex {
	return idx.tags
}

// ListTagChildren returns sub-tags and direct targets at the given tag path.
func (idx *Indices) ListTagChildren(prefix string) (subTags []string, directTargets []string) {
	return idx.tags.ListChildren(prefix)
}

// GetTagRoots returns root tag categories.
func (idx *Indices) GetTagRoots() []string {
	return idx.tags.GetRootCategories()
}

// CountByTag returns the number of targets under a tag prefix.
func (idx *Indices) CountByTag(tag string) int {
	return idx.tags.Count(tag)
}

// HasTag checks if a target has a specific tag.
func (idx *Indices) HasTag(targetID, tag string) bool {
	return idx.tags.HasTag(targetID, tag)
}

// GetTargetTags returns all tags for a target.
func (idx *Indices) GetTargetTags(targetID string) []string {
	return idx.tags.GetTags(targetID)
}

// ============================================================================
// Statistics
// ============================================================================

// Stats returns index statistics.
func (idx *Indices) Stats() map[string]int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	stats := map[string]int{
		"states":    len(idx.byState),
		"protocols": len(idx.byProtocol),
		"hosts":     len(idx.byHost),
	}

	// Count targets per state
	for state, set := range idx.byState {
		stats["state_"+state] = set.Len()
	}

	// Count targets per protocol
	for proto, set := range idx.byProtocol {
		stats["protocol_"+proto] = set.Len()
	}

	return stats
}
