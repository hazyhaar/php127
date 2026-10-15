package zone

// CleanupKind identifies an operation implemented by the request dispatcher.
type CleanupKind uint8

const (
	CleanupDestructor CleanupKind = iota
	CleanupStreamLock
	CleanupTransaction
)

// CleanupItem contains only scalar identifiers, never Go pointers.
type CleanupItem struct {
	Kind   CleanupKind
	Target uint32
	Extra  uint32
}

// CleanupRegistry is a dense LIFO stack. Its zero value is ready for use.
// It must not be copied after first use or accessed concurrently.
type CleanupRegistry struct {
	items   []CleanupItem
	running bool
}

// Len returns the number of pending items, excluding the item being dispatched.
func (r *CleanupRegistry) Len() int {
	return len(r.items)
}

// RegisterCleanup appends an operation, growing the backing vector if needed.
// Registration during dispatch is allowed; the new item is dispatched next.
func (r *CleanupRegistry) RegisterCleanup(item CleanupItem) {
	r.items = append(r.items, item)
}

func (r *CleanupRegistry) checkDispatch(dispatch func(CleanupItem)) {
	if r.running {
		panic("zone: recursive cleanup drain")
	}
	if len(r.items) != 0 && dispatch == nil {
		panic("zone: nil cleanup dispatcher")
	}
}

// RunCleanups pops each item before dispatch and retains the vector for reuse.
// A nil dispatcher is allowed only for an empty registry. Recursive drains are
// rejected. Dispatch panics do not skip remaining items: the first panic is
// rethrown after draining. Dispatch must return or panic, not call Goexit.
func (r *CleanupRegistry) RunCleanups(dispatch func(CleanupItem)) {
	r.checkDispatch(dispatch)
	r.running = true
	defer func() { r.running = false }()
	var firstPanic any
	for len(r.items) != 0 {
		last := len(r.items) - 1
		item := r.items[last]
		r.items = r.items[:last]
		func() {
			defer func() {
				if value := recover(); value != nil && firstPanic == nil {
					firstPanic = value
				}
			}()
			dispatch(item)
		}()
	}
	if firstPanic != nil {
		panic(firstPanic)
	}
}
