package readablename

import "strings"

var generatedWords = func() map[string]bool {
	set := make(map[string]bool, len(words))
	for _, word := range words {
		set[word] = true
	}
	return set
}()

// LooksGeneratedLabel recognizes exactly four words from the immutable v1
// vocabulary, including source words that themselves contain a hyphen. It
// reserves the generated namespace without copying or changing the dictionary.
func LooksGeneratedLabel(label string) bool {
	if len(label) > 128 {
		return false
	}
	parts := strings.Split(label, "-")
	if len(parts) < 4 || len(parts) > 8 {
		return false
	}
	var match func(int, int) bool
	match = func(start, count int) bool {
		if count == 4 {
			return start == len(parts)
		}
		for end := start + 1; end <= len(parts); end++ {
			if generatedWords[strings.Join(parts[start:end], "-")] && match(end, count+1) {
				return true
			}
		}
		return false
	}
	return match(0, 0)
}
