package dep

import (
	"errors"
	"fmt"
)

var ErrNotFound = errors.New("dep: dependency not found")

func Inject[T any](deps []any) (T, error) {
	for _, dep := range deps {
		if d, ok := dep.(T); ok {
			return d, nil
		}
	}

	var result T

	return result, fmt.Errorf("%w: %T", ErrNotFound, result)
}

func MustInject[T any](deps []any) T {
	dep, err := Inject[T](deps)
	if err != nil {
		panic(err)
	}

	return dep
}

func Provide[T any](dep T) []any {
	return []any{dep}
}

func ProvideMany(deps ...any) []any {
	return deps
}
