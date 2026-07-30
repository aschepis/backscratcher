package main

import "strings"

// searchConvos returns conversations whose phone, display name, or any message
// text contains query (case-insensitive). Results are ordered: convos with more
// matching messages first, ties broken by most-recent date.
func searchConvos(query string, convos []Convo) []Convo {
	if query == "" {
		return nil
	}
	q := strings.ToLower(query)

	type scored struct {
		convo Convo
		hits  int
	}
	var matches []scored

	for _, c := range convos {
		hits := 0
		if strings.Contains(strings.ToLower(c.Phone), q) {
			hits++
		}
		if strings.Contains(strings.ToLower(c.DisplayName), q) {
			hits++
		}
		if strings.Contains(strings.ToLower(c.Sample), q) {
			hits++
		}
		for _, m := range c.Messages {
			if strings.Contains(strings.ToLower(m.Text), q) {
				hits++
			}
		}
		if hits > 0 {
			matches = append(matches, scored{c, hits})
		}
	}

	// Sort: more hits first, then newer date first.
	for i := 1; i < len(matches); i++ {
		for j := i; j > 0; j-- {
			a, b := matches[j-1], matches[j]
			if b.hits > a.hits || (b.hits == a.hits && b.convo.LastDate.After(a.convo.LastDate)) {
				matches[j-1], matches[j] = matches[j], matches[j-1]
			}
		}
	}

	out := make([]Convo, len(matches))
	for i, s := range matches {
		out[i] = s.convo
	}
	return out
}
