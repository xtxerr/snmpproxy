// Package scheduler implements the polling scheduler.
package scheduler

import (
	"container/heap"
	"fmt"
	"math/rand"
	"sync"
	"time"
)

// PollerKey uniquely identifies a poller.
type PollerKey struct {
	Namespace string
	Target    string
	Poller    string
}

// String returns the string representation.
func (k PollerKey) String() string {
	return fmt.Sprintf("%s/%s/%s", k.Namespace, k.Target, k.Poller)
}

// ParsePollerKey parses a key string.
func ParsePollerKey(s string) (PollerKey, error) {
	var k PollerKey
	n, err := fmt.Sscanf(s, "%s/%s/%s", &k.Namespace, &k.Target, &k.Poller)
	if err != nil || n != 3 {
		return k, fmt.Errorf("invalid poller key: %s", s)
	}
	return k, nil
}

// PollItem represents a poller in the scheduler heap.
type PollItem struct {
	Key        PollerKey
	NextPollMs int64
	IntervalMs int64
	Polling    bool // Currently being polled
	index      int  // Heap index
}

// PollHeap implements heap.Interface for scheduling.
type PollHeap []*PollItem

func (h PollHeap) Len() int            { return len(h) }
func (h PollHeap) Less(i, j int) bool  { return h[i].NextPollMs < h[j].NextPollMs }
func (h PollHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i]; h[i].index = i; h[j].index = j }
func (h *PollHeap) Push(x any)         { item := x.(*PollItem); item.index = len(*h); *h = append(*h, item) }
func (h *PollHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	item.index = -1
	*h = old[:n-1]
	return item
}

// Peek returns the next item without removing it.
func (h PollHeap) Peek() *PollItem {
	if len(h) == 0 {
		return nil
	}
	return h[0]
}

// PollJob represents a job to execute.
type PollJob struct {
	Key PollerKey
}

// PollResult represents the result of a poll.
type PollResult struct {
	Key         PollerKey
	TimestampMs int64
	Success     bool
	Timeout     bool
	Error       string
	PollMs      int
	// Value data
	Counter *uint64
	Text    *string
	Gauge   *float64
}

// Scheduler manages polling schedules.
type Scheduler struct {
	mu      sync.Mutex
	heap    PollHeap
	heapIdx map[string]*PollItem // key string -> item
	wakeup  chan struct{}

	jobs     chan PollJob
	results  chan PollResult
	shutdown chan struct{}

	workers   int
	queueSize int

	// Callback for executing polls
	pollFunc func(key PollerKey) PollResult
}

// Config holds scheduler configuration.
type Config struct {
	Workers   int
	QueueSize int
}

// DefaultConfig returns default configuration.
func DefaultConfig() *Config {
	return &Config{
		Workers:   100,
		QueueSize: 10000,
	}
}

// New creates a new scheduler.
func New(cfg *Config) *Scheduler {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	s := &Scheduler{
		heap:      make(PollHeap, 0),
		heapIdx:   make(map[string]*PollItem),
		wakeup:    make(chan struct{}, 1),
		jobs:      make(chan PollJob, cfg.QueueSize),
		results:   make(chan PollResult, cfg.QueueSize),
		shutdown:  make(chan struct{}),
		workers:   cfg.Workers,
		queueSize: cfg.QueueSize,
	}
	heap.Init(&s.heap)
	return s
}

// SetPollFunc sets the callback for executing polls.
func (s *Scheduler) SetPollFunc(fn func(key PollerKey) PollResult) {
	s.pollFunc = fn
}

// Results returns the results channel.
func (s *Scheduler) Results() <-chan PollResult {
	return s.results
}

// Start starts the scheduler.
func (s *Scheduler) Start() {
	// Start workers
	for i := 0; i < s.workers; i++ {
		go s.worker()
	}
	// Start scheduler loop
	go s.schedulerLoop()
}

// Stop stops the scheduler.
func (s *Scheduler) Stop() {
	close(s.shutdown)
}

// Add adds a poller to the schedule.
func (s *Scheduler) Add(key PollerKey, intervalMs uint32) {
	keyStr := key.String()

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.heapIdx[keyStr]; exists {
		return
	}

	// Add jitter to spread out initial polls
	interval := int64(intervalMs)
	jitter := rand.Int63n(interval)

	item := &PollItem{
		Key:        key,
		NextPollMs: time.Now().UnixMilli() + jitter,
		IntervalMs: interval,
		Polling:    false,
	}

	heap.Push(&s.heap, item)
	s.heapIdx[keyStr] = item
	s.signalWakeup()
}

// Remove removes a poller from the schedule.
func (s *Scheduler) Remove(key PollerKey) {
	keyStr := key.String()

	s.mu.Lock()
	defer s.mu.Unlock()

	item, ok := s.heapIdx[keyStr]
	if !ok {
		return
	}

	if item.index >= 0 {
		heap.Remove(&s.heap, item.index)
	}
	delete(s.heapIdx, keyStr)
}

// UpdateInterval updates the interval for a poller.
func (s *Scheduler) UpdateInterval(key PollerKey, intervalMs uint32) {
	keyStr := key.String()

	s.mu.Lock()
	defer s.mu.Unlock()

	item, ok := s.heapIdx[keyStr]
	if !ok {
		return
	}

	item.IntervalMs = int64(intervalMs)
}

// Pause pauses a poller (removes from heap but keeps in index).
func (s *Scheduler) Pause(key PollerKey) {
	keyStr := key.String()

	s.mu.Lock()
	defer s.mu.Unlock()

	item, ok := s.heapIdx[keyStr]
	if !ok {
		return
	}

	if item.index >= 0 {
		heap.Remove(&s.heap, item.index)
		item.index = -1
	}
}

// Resume resumes a paused poller.
func (s *Scheduler) Resume(key PollerKey) {
	keyStr := key.String()

	s.mu.Lock()
	defer s.mu.Unlock()

	item, ok := s.heapIdx[keyStr]
	if !ok {
		return
	}

	if item.index < 0 {
		item.NextPollMs = time.Now().UnixMilli()
		item.Polling = false
		heap.Push(&s.heap, item)
		s.signalWakeup()
	}
}

// Contains checks if a poller is scheduled.
func (s *Scheduler) Contains(key PollerKey) bool {
	keyStr := key.String()

	s.mu.Lock()
	defer s.mu.Unlock()

	_, ok := s.heapIdx[keyStr]
	return ok
}

// Stats returns scheduler statistics.
func (s *Scheduler) Stats() (heapSize, indexSize, queueUsed int) {
	s.mu.Lock()
	heapSize = s.heap.Len()
	indexSize = len(s.heapIdx)
	s.mu.Unlock()
	queueUsed = len(s.jobs)
	return
}

func (s *Scheduler) signalWakeup() {
	select {
	case s.wakeup <- struct{}{}:
	default:
	}
}

func (s *Scheduler) schedulerLoop() {
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()

	for {
		s.mu.Lock()
		next := s.heap.Peek()

		if next == nil {
			s.mu.Unlock()
			select {
			case <-s.wakeup:
				continue
			case <-s.shutdown:
				return
			}
		}

		now := time.Now().UnixMilli()
		sleepMs := next.NextPollMs - now
		s.mu.Unlock()

		if sleepMs > 0 {
			timer.Reset(time.Duration(sleepMs) * time.Millisecond)
			select {
			case <-timer.C:
			case <-s.wakeup:
				continue
			case <-s.shutdown:
				return
			}
		}

		s.processDueItems()
	}
}

func (s *Scheduler) processDueItems() {
	now := time.Now().UnixMilli()

	s.mu.Lock()
	defer s.mu.Unlock()

	for s.heap.Len() > 0 {
		next := s.heap.Peek()

		if next.NextPollMs > now {
			break
		}

		item := heap.Pop(&s.heap).(*PollItem)

		// Skip if already polling
		if item.Polling {
			continue
		}

		item.Polling = true

		// Try to queue the job
		select {
		case s.jobs <- PollJob{Key: item.Key}:
			// Success
		default:
			// Queue full - reschedule with short delay
			item.Polling = false
			item.NextPollMs = now + 10
			heap.Push(&s.heap, item)
		}
	}
}

// MarkComplete marks a poll as complete and reschedules.
func (s *Scheduler) MarkComplete(key PollerKey) {
	keyStr := key.String()

	s.mu.Lock()
	defer s.mu.Unlock()

	item, ok := s.heapIdx[keyStr]
	if !ok {
		return
	}

	now := time.Now().UnixMilli()
	item.NextPollMs = now + item.IntervalMs
	item.Polling = false

	if item.index < 0 {
		heap.Push(&s.heap, item)
	}
	s.signalWakeup()
}

func (s *Scheduler) worker() {
	for {
		select {
		case job := <-s.jobs:
			result := s.executePoll(job.Key)
			s.MarkComplete(job.Key)

			select {
			case s.results <- result:
			case <-s.shutdown:
				return
			}

		case <-s.shutdown:
			return
		}
	}
}

func (s *Scheduler) executePoll(key PollerKey) PollResult {
	if s.pollFunc == nil {
		return PollResult{
			Key:         key,
			TimestampMs: time.Now().UnixMilli(),
			Success:     false,
			Error:       "no poll function configured",
		}
	}

	return s.pollFunc(key)
}

// GetScheduledPollers returns all scheduled poller keys.
func (s *Scheduler) GetScheduledPollers() []PollerKey {
	s.mu.Lock()
	defer s.mu.Unlock()

	keys := make([]PollerKey, 0, len(s.heapIdx))
	for _, item := range s.heapIdx {
		keys = append(keys, item.Key)
	}
	return keys
}

// GetNextPollTime returns the next poll time for a poller.
func (s *Scheduler) GetNextPollTime(key PollerKey) (time.Time, bool) {
	keyStr := key.String()

	s.mu.Lock()
	defer s.mu.Unlock()

	item, ok := s.heapIdx[keyStr]
	if !ok {
		return time.Time{}, false
	}

	return time.UnixMilli(item.NextPollMs), true
}
