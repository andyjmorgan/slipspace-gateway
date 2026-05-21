package livefeed

import (
	"errors"
	"sync"
)

// ErrBodyCapacityNonPositive is returned by NewBodyStore when the
// caller passes a non-positive total capacity. Callers should gate
// store construction on the env-derived byte budget being positive
// and skip wiring otherwise.
var ErrBodyCapacityNonPositive = errors.New("livefeed: body store capacity must be > 0")

// BodyEnvelope is the per-request capture stored alongside an Entry.
// All fields are value-typed so the envelope can be returned to
// handlers without aliasing store storage. Either Request or Response
// may be nil when the corresponding side was empty (e.g. a GET request
// with no body, or a transport-failed response).
type BodyEnvelope struct {
	// Request is the raw inbound request body bytes captured by the
	// bodycapture middleware. Cap at the per-body byte ceiling — see
	// RequestTruncated.
	Request []byte

	// RequestTotalBytes is the size of the request body as the client
	// sent it. Equal to len(Request) when not truncated; larger when
	// truncated.
	RequestTotalBytes int64

	// RequestTruncated is true when the request body exceeded the
	// per-body cap and Request holds only the head bytes.
	RequestTruncated bool

	// Response is the raw outbound response bytes captured by the
	// response-tee writer. For streaming responses, these are the raw
	// SSE event bytes pre-accumulator; for non-streaming, the full
	// JSON (subject to the per-body cap).
	Response []byte

	// ResponseTotalBytes is the size of the response body as it left
	// the gateway. Equal to len(Response) when not truncated; larger
	// when truncated.
	ResponseTotalBytes int64

	// ResponseTruncated is true when the response body exceeded the
	// per-body cap and Response holds only the head bytes.
	ResponseTruncated bool

	// ResponseAssembled is the JSON-encoded reconstruction of the
	// response the provider would have returned non-streaming, built
	// by the per-provider accumulator from the streamed chunks. Empty
	// for non-streaming responses and for streams the accumulator
	// could not parse.
	ResponseAssembled string

	// AssemblyPartial is true when the accumulator hit a malformed
	// chunk or unknown delta type mid-stream and could not complete
	// reassembly. ResponseAssembled holds whatever was parseable up
	// to that point.
	AssemblyPartial bool
}

// Bytes returns the total bytes this envelope contributes to the
// store's byte budget. Used by the LRU to track its capacity.
func (e *BodyEnvelope) Bytes() int {
	return len(e.Request) + len(e.Response) + len(e.ResponseAssembled)
}

// BodyStore is a byte-bounded LRU of BodyEnvelopes keyed by EventID.
// Eviction is oldest-first when a new Put would exceed the configured
// total byte budget.
//
// Goroutine-safe: concurrent Put/Get/Delete take the write lock; reads
// don't help here because Get also bumps recency.
type BodyStore struct {
	capacity int

	mu      sync.Mutex
	bytes   int
	entries map[string]*bodyNode // O(1) lookup
	head    *bodyNode            // most recently used
	tail    *bodyNode            // oldest, evicted first
}

// bodyNode is a doubly-linked-list node carrying the per-event
// envelope plus its position in the recency list.
type bodyNode struct {
	eventID string
	env     BodyEnvelope
	bytes   int
	prev    *bodyNode
	next    *bodyNode
}

// NewBodyStore constructs a store with the given byte budget.
func NewBodyStore(capacityBytes int) (*BodyStore, error) {
	if capacityBytes <= 0 {
		return nil, ErrBodyCapacityNonPositive
	}
	return &BodyStore{
		capacity: capacityBytes,
		entries:  map[string]*bodyNode{},
	}, nil
}

// Capacity returns the configured byte budget.
func (s *BodyStore) Capacity() int { return s.capacity }

// Bytes returns the bytes currently stored. Useful for tests and
// admin debug surfaces.
func (s *BodyStore) Bytes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bytes
}

// Len returns the number of envelopes currently stored.
func (s *BodyStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

// Put inserts (or replaces) the envelope for eventID, evicting the
// oldest entries as needed to fit the new envelope under the byte
// budget. Envelopes larger than the total budget are silently dropped
// — the LRU still serves smaller entries.
func (s *BodyStore) Put(eventID string, env BodyEnvelope) {
	size := env.Bytes()
	if size > s.capacity {
		// Single envelope larger than the whole budget: refuse rather
		// than evicting everything else. Operators get nothing for
		// this event; bigger budget required.
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.entries[eventID]; ok {
		s.bytes -= existing.bytes
		s.remove(existing)
		delete(s.entries, eventID)
	}

	for s.bytes+size > s.capacity && s.tail != nil {
		s.evictOldestLocked()
	}

	node := &bodyNode{eventID: eventID, env: env, bytes: size}
	s.entries[eventID] = node
	s.bytes += size
	s.pushFront(node)
}

// Get returns the envelope for eventID and bumps it to most-recently-
// used. Returns the zero envelope and false when absent.
func (s *BodyStore) Get(eventID string) (BodyEnvelope, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	node, ok := s.entries[eventID]
	if !ok {
		return BodyEnvelope{}, false
	}
	s.remove(node)
	s.pushFront(node)
	return node.env, true
}

// Delete removes the envelope for eventID. No-op when absent.
func (s *BodyStore) Delete(eventID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	node, ok := s.entries[eventID]
	if !ok {
		return
	}
	s.bytes -= node.bytes
	s.remove(node)
	delete(s.entries, eventID)
}

// pushFront / remove / evictOldestLocked manage the recency list.

func (s *BodyStore) pushFront(node *bodyNode) {
	node.prev = nil
	node.next = s.head
	if s.head != nil {
		s.head.prev = node
	}
	s.head = node
	if s.tail == nil {
		s.tail = node
	}
}

func (s *BodyStore) remove(node *bodyNode) {
	if node.prev != nil {
		node.prev.next = node.next
	} else {
		s.head = node.next
	}
	if node.next != nil {
		node.next.prev = node.prev
	} else {
		s.tail = node.prev
	}
	node.prev = nil
	node.next = nil
}

func (s *BodyStore) evictOldestLocked() {
	if s.tail == nil {
		return
	}
	victim := s.tail
	s.bytes -= victim.bytes
	s.remove(victim)
	delete(s.entries, victim.eventID)
}
