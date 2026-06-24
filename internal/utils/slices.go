package utils

// DedupePreserveOrder returns items with duplicates removed, keeping the first
// occurrence of each value in its original position.
func DedupePreserveOrder[T comparable](items []T) []T {
	seen := make(map[T]struct{}, len(items))
	out := make([]T, 0, len(items))
	for _, it := range items {
		if _, ok := seen[it]; ok {
			continue
		}
		seen[it] = struct{}{}
		out = append(out, it)
	}
	return out
}
