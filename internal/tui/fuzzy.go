package tui

import "strings"

// fuzzyMatchScore ranks exact prefixes first, then substrings, subsequences,
// and finally a one-edit prefix match for small typing errors.
func fuzzyMatchScore(candidate, query string) int {
	candidate = strings.ToLower(strings.TrimSpace(candidate))
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return 0
	}
	if strings.HasPrefix(candidate, query) {
		return 10000 - len([]rune(candidate))
	}
	if index := strings.Index(candidate, query); index >= 0 {
		return 8000 - index*10 - len([]rune(candidate))
	}

	candidateRunes := []rune(candidate)
	queryRunes := []rune(query)
	position := 0
	first := -1
	previous := -2
	consecutive := 0
	gaps := 0
	for _, want := range queryRunes {
		found := -1
		for position < len(candidateRunes) {
			if candidateRunes[position] == want {
				found = position
				break
			}
			position++
		}
		if found < 0 {
			first = -1
			break
		}
		if first < 0 {
			first = found
		}
		if found == previous+1 {
			consecutive++
		} else if previous >= 0 {
			gaps += found - previous - 1
		}
		previous = found
		position = found + 1
	}
	if first >= 0 {
		return 5000 + consecutive*20 - gaps*8 - first*3 - len(candidateRunes)
	}

	if len(queryRunes) < 4 {
		return -1
	}
	bestDistance := 4
	bestLengthDelta := 4
	minPrefix := max(1, len(queryRunes)-1)
	maxPrefix := min(len(candidateRunes), len(queryRunes)+1)
	for length := minPrefix; length <= maxPrefix; length++ {
		prefix := candidateRunes[:length]
		distance := keyboardEditDistance(queryRunes, prefix)
		if isSingleAdjacentTransposition(queryRunes, prefix) {
			distance = 1
		}
		delta := absInt(len(queryRunes) - length)
		if distance < bestDistance || (distance == bestDistance && delta < bestLengthDelta) {
			bestDistance = distance
			bestLengthDelta = delta
		}
	}
	if bestDistance <= 2 {
		return 2000 - bestDistance*100 - bestLengthDelta*10 - len(candidateRunes)
	}
	return -1
}

func keyboardEditDistance(a, b []rune) int {
	const insertionCost = 2
	const substitutionCost = 3
	previous := make([]int, len(b)+1)
	current := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j * insertionCost
	}
	for i, ar := range a {
		current[0] = (i + 1) * insertionCost
		for j, br := range b {
			cost := 0
			if ar != br {
				cost = substitutionCost
				if keyboardAdjacent(ar, br) {
					cost = 1
				}
			}
			current[j+1] = min(
				current[j]+insertionCost,
				min(previous[j+1]+insertionCost, previous[j]+cost),
			)
		}
		previous, current = current, previous
	}
	return previous[len(b)]
}

func keyboardAdjacent(a, b rune) bool {
	type point struct {
		x, y float64
	}
	position := func(key rune) (point, bool) {
		rows := []string{"qwertyuiop", "asdfghjkl", "zxcvbnm"}
		offsets := []float64{0, 0.25, 0.75}
		for rowIndex, row := range rows {
			for column, value := range row {
				if rune(value) == key {
					return point{x: float64(column) + offsets[rowIndex], y: float64(rowIndex)}, true
				}
			}
		}
		return point{}, false
	}
	first, okFirst := position(a)
	second, okSecond := position(b)
	if !okFirst || !okSecond {
		return false
	}
	dx := first.x - second.x
	dy := first.y - second.y
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	return dx <= 1.1 && dy <= 1
}

func isSingleAdjacentTransposition(a, b []rune) bool {
	if len(a) != len(b) {
		return false
	}
	first, second := -1, -1
	for index := range a {
		if a[index] == b[index] {
			continue
		}
		if first < 0 {
			first = index
			continue
		}
		if second < 0 {
			second = index
			continue
		}
		return false
	}
	return first >= 0 && second == first+1 && a[first] == b[second] && a[second] == b[first]
}

func runeEditDistance(a, b []rune) int {
	if len(a) == len(b) {
		first, second := -1, -1
		for i := range a {
			if a[i] != b[i] {
				if first < 0 {
					first = i
				} else {
					second = i
					break
				}
			}
		}
		if first >= 0 && second == first+1 && a[first] == b[second] && a[second] == b[first] {
			moreDifferences := false
			for i := second + 1; i < len(a); i++ {
				if a[i] != b[i] {
					moreDifferences = true
					break
				}
			}
			if !moreDifferences {
				return 1
			}
		}
	}
	previous := make([]int, len(b)+1)
	current := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i, ar := range a {
		current[0] = i + 1
		for j, br := range b {
			cost := 0
			if ar != br {
				cost = 1
			}
			current[j+1] = min(
				current[j]+1,
				min(previous[j+1]+1, previous[j]+cost),
			)
		}
		previous, current = current, previous
	}
	return previous[len(b)]
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func fuzzyRank(values []string, query string) []int {
	type result struct {
		index int
		score int
	}
	ranked := make([]result, 0, len(values))
	for index, value := range values {
		if score := fuzzyMatchScore(value, query); score >= 0 {
			ranked = append(ranked, result{index: index, score: score})
		}
	}
	for i := 1; i < len(ranked); i++ {
		for j := i; j > 0 && ranked[j].score > ranked[j-1].score; j-- {
			ranked[j], ranked[j-1] = ranked[j-1], ranked[j]
		}
	}
	indices := make([]int, 0, len(ranked))
	for _, item := range ranked {
		indices = append(indices, item.index)
	}
	return indices
}
