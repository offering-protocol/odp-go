package odp

import (
	"context"
	"errors"
	"fmt"
	"iter"
)

const MaxTraversalPages = 16

var ErrPaginationLoop = errors.New("ODP pagination loop detected")

type PageLoader[Item any] func(context.Context, string) (Page[Item], error)

func IteratePages[Item any](ctx context.Context, first Page[Item], load PageLoader[Item]) iter.Seq2[Page[Item], error] {
	return func(yield func(Page[Item], error) bool) {
		visited := make(map[string]struct{})
		page := first
		for count := 0; count < MaxTraversalPages; count++ {
			if !yield(page, nil) {
				return
			}
			if page.Next == "" {
				return
			}
			if _, exists := visited[page.Next]; exists {
				yield(Page[Item]{}, ErrPaginationLoop)
				return
			}
			visited[page.Next] = struct{}{}
			if err := ctx.Err(); err != nil {
				yield(Page[Item]{}, err)
				return
			}
			next, err := load(ctx, page.Next)
			if err != nil {
				yield(Page[Item]{}, err)
				return
			}
			page = next
		}
		if page.Next != "" {
			yield(Page[Item]{}, fmt.Errorf("ODP pagination exceeded the %d-page traversal limit", MaxTraversalPages))
		}
	}
}

func IterateItems[Item any](ctx context.Context, first Page[Item], load PageLoader[Item]) iter.Seq2[Item, error] {
	return func(yield func(Item, error) bool) {
		for page, err := range IteratePages(ctx, first, load) {
			if err != nil {
				var zero Item
				yield(zero, err)
				return
			}
			for _, item := range page.Items {
				if !yield(item, nil) {
					return
				}
			}
		}
	}
}
