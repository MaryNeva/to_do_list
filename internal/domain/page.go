package domain

type PageRequest struct {
	Limit  int
	Offset int
}

type Page[T any] struct {
	Items  []T
	Total  int
	Limit  int
	Offset int
}

func NewPage[T any](items []T, total int, req PageRequest) Page[T] {
	if items == nil {
		items = []T{}
	}
	return Page[T]{Items: items, Total: total, Limit: req.Limit, Offset: req.Offset}
}
