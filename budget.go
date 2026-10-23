package lua

import "errors"

type Budget struct {
	limit int64
	used  int64
}

func NewBudget(limit int64) *Budget { return &Budget{limit: limit} }
func (b *Budget) Used() int64       { return b.used }

func (b *Budget) reserve(size int64) {
	if b == nil {
		return
	}
	if size < 0 || (b.limit > 0 && size > b.limit-b.used) {
		err := errors.New("script exceeded the memory limit")
		panic(&Error{Value: String(err.Error()), Message: err.Error(), Abort: err})
	}
	b.used += size
}
