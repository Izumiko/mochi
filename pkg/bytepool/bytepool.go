// Package bytepool defines pool for storing and reusing raw bytes
package bytepool

import "sync"

// BytePool is a cached pool of reusable byte slices.
type BytePool struct {
	sync.Pool
}

// NewBytePool allocates a new BytePool with slices of equal length and capacity.
func NewBytePool(length int) *BytePool {
	var bp BytePool
	bp.New = func() any {
		// This avoids allocations for the slice metadata, see:
		// https://staticcheck.io/docs/checks#SA6002
		b := make([]byte, length)
		return &b
	}
	return &bp
}

// Get returns a byte slice from the pool.
func (bp *BytePool) Get() *[]byte {
	return bp.Pool.Get().(*[]byte)
}

// Put returns a byte slice to the pool.
func (bp *BytePool) Put(b *[]byte) {
	*b = (*b)[:cap(*b)]

	// Zero out the bytes.
	// This specific expression is optimized by the compiler:
	// https://github.com/golang/go/issues/5373.
	for i := range *b {
		(*b)[i] = 0
	}

	bp.Pool.Put(b)
}
