package management

import (
	"errors"
	"math"
	"regexp"
	"slices"
)

var slideColor = regexp.MustCompile(`^#[a-fA-F0-9]{6}$`)

func validateSlideStyle(s HomeSlide) error {
	bad := errors.New("轮播文字位置、字体或颜色无效")
	if !slices.Contains([]string{"", "flow", "free"}, s.Placement) || s.Placement == "free" && s.Layout != "overlay" || !slices.Contains([]string{"", "sans", "serif"}, s.FontFamily) {
		return bad
	}
	for _, color := range []string{s.TitleColor, s.DescriptionColor} {
		if color != "" && !slideColor.MatchString(color) {
			return bad
		}
	}
	for _, n := range []struct {
		value    *float64
		min, max float64
	}{{s.TextPositionX, 0, 100}, {s.TextPositionY, 0, 100}, {s.TextWidth, 25, 85}, {s.TitleSize, 24, 72}, {s.DescriptionSize, 12, 24}} {
		if n.value != nil && (*n.value < n.min || *n.value > n.max || math.Trunc(*n.value) != *n.value) {
			return bad
		}
	}
	if math.Trunc(s.ImagePositionX) != s.ImagePositionX || math.Trunc(s.ImagePositionY) != s.ImagePositionY {
		return bad
	}
	return nil
}
