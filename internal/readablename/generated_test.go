package readablename

import "testing"

func TestGeneratedLabelNamespaceIncludesHyphenatedVocabularyWords(t *testing.T) {
	for _, name := range []string{FromID(Host, "host"), "abacus-abacus-abacus-abacus", "drop-down-felt-tip-t-shirt-yo-yo"} {
		if !LooksGeneratedLabel(name) {
			t.Fatal("generated grammar missed", name)
		}
	}
	for _, name := range []string{"", "abacus-abacus-abacus", "abacus-abacus-abacus-abacus-abacus", "invented-not-vocabulary-word"} {
		if LooksGeneratedLabel(name) {
			t.Fatal("unrelated name reserved", name)
		}
	}
}
