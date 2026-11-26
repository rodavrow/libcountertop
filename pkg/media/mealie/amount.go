package mealie

import (
	"math"
	"strconv"
	"strings"
)

const maxDenominator = 32

var vulgar = map[[2]int]string{
	{1, 2}: "½", {1, 3}: "⅓", {2, 3}: "⅔", {1, 4}: "¼", {3, 4}: "¾",
	{1, 5}: "⅕", {2, 5}: "⅖", {3, 5}: "⅗", {4, 5}: "⅘", {1, 6}: "⅙", {5, 6}: "⅚",
	{1, 8}: "⅛", {3, 8}: "⅜", {5, 8}: "⅝", {7, 8}: "⅞",
}

func (i Ingredient) Scaled(by float64) string {
	if i.Quantity == nil || *i.Quantity == 0 || i.Unit == nil && i.Food == nil {
		return i.Text()
	}
	q := *i.Quantity * by
	parts := []string{quantity(q, i.Unit == nil || i.Unit.Fraction == nil || *i.Unit.Fraction)}
	if u := i.unit(q); u != "" {
		parts = append(parts, u)
	}
	if i.Food != nil && i.Food.Name != "" {
		food := i.Food.Name
		if q > 1 && i.Unit == nil && i.Food.PluralName != "" {
			food = i.Food.PluralName
		}
		parts = append(parts, food)
	}
	if i.Note != "" {
		parts = append(parts, i.Note)
	}
	return strings.Join(parts, " ")
}

func (i Ingredient) Text() string {
	for _, s := range []string{i.Display, i.Original, i.Note} {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	if i.Food != nil {
		return i.Food.Name
	}
	return ""
}

func (i Ingredient) Name() string {
	if i.Food != nil && i.Food.Name != "" {
		return i.Food.Name
	}
	return i.Text()
}

func (i Ingredient) unit(q float64) string {
	if i.Unit == nil {
		return ""
	}
	plural := q > 1
	if i.Unit.UseAbbrevation {
		if plural && i.Unit.PluralAbbrev != "" {
			return i.Unit.PluralAbbrev
		}
		if i.Unit.Abbreviation != "" {
			return i.Unit.Abbreviation
		}
	}
	if plural && i.Unit.PluralName != "" {
		return i.Unit.PluralName
	}
	return i.Unit.Name
}

func quantity(q float64, fraction bool) string {
	if !fraction {
		return strconv.FormatFloat(math.Round(q*1000)/1000, 'f', -1, 64)
	}
	whole := math.Floor(q)
	num, den := nearest(q - whole)
	if num == den {
		whole, num = whole+1, 0
	}
	var out string
	if whole > 0 || num == 0 {
		out = strconv.FormatFloat(whole, 'f', 0, 64)
	}
	if num == 0 {
		return out
	}
	frac, ok := vulgar[[2]int{num, den}]
	if !ok {
		frac = strconv.Itoa(num) + "/" + strconv.Itoa(den)
	}
	if out == "" {
		return frac
	}
	if ok {
		return out + frac
	}
	return out + " " + frac
}

func nearest(x float64) (int, int) {
	bestNum, bestDen, bestErr := 0, 1, math.Abs(x)
	for den := 2; den <= maxDenominator; den++ {
		num := int(math.Round(x * float64(den)))
		if err := math.Abs(x - float64(num)/float64(den)); err < bestErr-1e-9 {
			bestNum, bestDen, bestErr = num, den, err
		}
	}
	g := gcd(bestNum, bestDen)
	if g > 1 {
		bestNum, bestDen = bestNum/g, bestDen/g
	}
	return bestNum, bestDen
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	if a == 0 {
		return 1
	}
	return a
}
