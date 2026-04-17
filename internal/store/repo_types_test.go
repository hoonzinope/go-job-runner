package store

import "testing"

func TestPageNormalize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		page       Page
		wantLimit  int
		wantOffset int
	}{
		{name: "defaults", page: Page{}, wantLimit: 20, wantOffset: 0},
		{name: "zero page", page: Page{Page: 0, Size: 5}, wantLimit: 5, wantOffset: 0},
		{name: "zero size", page: Page{Page: 3, Size: 0}, wantLimit: 20, wantOffset: 40},
		{name: "custom", page: Page{Page: 4, Size: 7}, wantLimit: 7, wantOffset: 21},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			limit, offset := tt.page.normalize()
			if limit != tt.wantLimit || offset != tt.wantOffset {
				t.Fatalf("normalize mismatch: got (%d, %d) want (%d, %d)", limit, offset, tt.wantLimit, tt.wantOffset)
			}
		})
	}
}
