package ambient

import "testing"

func TestCategoryConstants(t *testing.T) {
	cats := []Category{CategoryLoudNoise, CategoryAlarmLike, CategoryMusicLike, CategorySilence}
	seen := map[Category]bool{}
	for _, c := range cats {
		if c == "" {
			t.Error("category constant must not be empty")
		}
		if seen[c] {
			t.Errorf("duplicate category %q", c)
		}
		seen[c] = true
	}
}
