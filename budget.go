package lua

import "errors"

type Budget struct {
	limit int64
	used  int64
}

var ErrMemoryLimit = errors.New("script exceeded the memory limit")

func NewBudget(limit int64) *Budget { return &Budget{limit: limit} }
func (b *Budget) Used() int64       { return b.used }

func (b *Budget) reserve(size int64) {
	if b == nil {
		return
	}
	if size < 0 || (b.limit > 0 && size > b.limit-b.used) {
		panic(&Error{Value: String(ErrMemoryLimit.Error()), Message: ErrMemoryLimit.Error(), Abort: ErrMemoryLimit})
	}
	b.used += size
}
