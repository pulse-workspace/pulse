package message

import (
	"container/list"
	"sync"
)

// recentMessagesCap bounds the write-through cache used for
// read-after-write semantics. Sized so the worst-case memory
// footprint is ~2.5 MiB (avg Message ~256 bytes × 10 000 entries).
// Chosen empirically: 10k entries covers recent reads while keeping
// the LRU's amortised eviction cost under a microsecond per send.
const recentMessagesCap = 10_000

// recentCache is a bounded LRU keyed by msg_id. The cache backs
// read-after-write semantics so the projection runner's eventual
// consistency never surfaces a "message not found" to the sender
// when they immediately re-read.
//
// Concurrency: sync.RWMutex — multiple readers (GetMessage) can
// run in parallel; only one writer (SendMessage, which adds an
// entry) at a time. The LRU list is mutated under write-lock only.
//
// Eviction: LRU on insertion — every Add moves the entry to the
// front of the list; when the list exceeds recentMessagesCap, the
// tail is removed.
type recentCache struct {
	mu   sync.RWMutex
	cap  int
	ll   *list.List               // front = most-recently-used
	keys map[string]*list.Element // msg_id → *list.Element{value: *Message}
}

// newRecentCache constructs an empty cache. cap==0 falls back to
// the package default (recentMessagesCap) so callers don't have to
// remember the constant.
func newRecentCache(cap int) *recentCache {
	if cap <= 0 {
		cap = recentMessagesCap
	}
	return &recentCache{
		cap:  cap,
		ll:   list.New(),
		keys: make(map[string]*list.Element, cap),
	}
}

// Add inserts (or refreshes) a Message in the cache. If the cache
// is at capacity, the least-recently-used entry is evicted first.
func (c *recentCache) Add(m *Message) {
	if m == nil || m.MsgID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if el, ok := c.keys[m.MsgID]; ok {
		// Refresh — move to front, replace value (content may have
		// changed for a re-send on retry).
		c.ll.MoveToFront(el)
		el.Value = m
		return
	}
	el := c.ll.PushFront(m)
	c.keys[m.MsgID] = el

	// Evict tail if over capacity.
	if c.ll.Len() > c.cap {
		oldest := c.ll.Back()
		if oldest != nil {
			c.ll.Remove(oldest)
			oldMsg, _ := oldest.Value.(*Message)
			if oldMsg != nil {
				delete(c.keys, oldMsg.MsgID)
			}
		}
	}
}

// Get returns the cached Message for msgID, or (nil, false) if
// absent. The lookup promotes the hit entry to the front so the
// LRU stays coherent — read-side access must count as "recently
// used" or the read-after-write pattern breaks the eviction
// ordering under sustained reads.
func (c *recentCache) Get(msgID string) (*Message, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.keys[msgID]
	if !ok {
		return nil, false
	}
	c.ll.MoveToFront(el)
	return el.Value.(*Message), true
}

// Len returns the current size (test-only).
func (c *recentCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ll.Len()
}

// drain returns every cached Message as a slice (most-recent first).
// Read-locked; safe to call from any goroutine. Used by the
// idempotency lookup in SendMessage which needs to scan recent
// messages for a matching clientMsgID.
//
// Cost: O(cap) per call. Acceptable for the idempotency path at human typing rates.
func (c *recentCache) drain() []*Message {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]*Message, 0, c.ll.Len())
	for e := c.ll.Front(); e != nil; e = e.Next() {
		out = append(out, e.Value.(*Message))
	}
	return out
}
