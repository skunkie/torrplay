// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

// Package textsearch matches free-text queries against item fields and ranks
// the matches, tolerating typos on request.
package textsearch

import (
	"cmp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

type matchQuality int

const (
	matchNone matchQuality = iota
	matchFuzzy
	matchSubstring
	matchPrefix
	matchWord
)

// fuzzyMinTermLength keeps short terms from matching approximately: a single
// edit already turns most short words into other common words.
const fuzzyMinTermLength = 5

type matchScore struct {
	fuzzyTerms int
	total      int
}

// Search returns the items whose fields match every term of query, ordered by
// match quality, best first: whole words before word starts before matches
// inside words, and typo matches, allowed only when fuzzy is set, last. Items
// of equal quality keep their order. A blank query returns items unchanged,
// and a query of punctuation alone, having no words, keeps the items whose
// fields contain each of its whitespace-separated parts as text, in their
// order.
func Search[T any](items []T, query string, fuzzy bool, fields func(T) []string) []T {
	terms := splitWords(query)
	if len(terms) == 0 {
		return matchText(items, strings.Fields(foldText(query)), fields)
	}

	type scoredItem struct {
		item  T
		score matchScore
	}
	scored := make([]scoredItem, 0, len(items))
	for _, item := range items {
		if score, ok := scoreTerms(terms, splitWords(strings.Join(fields(item), " ")), fuzzy); ok {
			scored = append(scored, scoredItem{item: item, score: score})
		}
	}

	slices.SortStableFunc(scored, func(a, b scoredItem) int {
		return cmp.Or(
			cmp.Compare(a.score.fuzzyTerms, b.score.fuzzyTerms),
			cmp.Compare(b.score.total, a.score.total),
		)
	})

	matches := make([]T, 0, len(scored))
	for _, s := range scored {
		matches = append(matches, s.item)
	}

	return matches
}

// matchText keeps the items whose fields contain every text part, in their
// order, and every item when there are no parts.
func matchText[T any](items []T, parts []string, fields func(T) []string) []T {
	if len(parts) == 0 {
		return items
	}
	matches := make([]T, 0, len(items))
	for _, item := range items {
		if containsTextParts(fields(item), parts) {
			matches = append(matches, item)
		}
	}

	return matches
}

// containsTextParts reports whether every part occurs in one of fields. A
// part holds no whitespace, so it never spans two fields.
func containsTextParts(fields, parts []string) bool {
	folded := make([]string, 0, len(fields))
	for _, field := range fields {
		folded = append(folded, foldText(field))
	}
	for _, part := range parts {
		if !slices.ContainsFunc(folded, func(field string) bool { return strings.Contains(field, part) }) {
			return false
		}
	}

	return true
}

func scoreTerms(terms, words []string, fuzzy bool) (matchScore, bool) {
	joined := strings.Join(words, " ")
	var score matchScore
	for _, term := range terms {
		match := matchTerm(term, words, joined, fuzzy)
		if match == matchNone {
			return matchScore{}, false
		}
		if match == matchFuzzy {
			score.fuzzyTerms++
		}
		score.total += int(match)
	}

	return score, true
}

func matchTerm(term string, words []string, joined string, fuzzy bool) matchQuality {
	best := matchNone
	for _, word := range words {
		switch {
		case word == term:
			return matchWord
		case strings.HasPrefix(word, term):
			best = matchPrefix
		}
	}
	if best != matchNone {
		return best
	}
	if strings.Contains(joined, term) {
		return matchSubstring
	}
	if !fuzzy {
		return matchNone
	}

	// Terms carrying digits name years, resolutions and episodes, where a
	// one-character difference is a different release, not a typo.
	termLength := utf8.RuneCountInString(term)
	if termLength < fuzzyMinTermLength || strings.IndexFunc(term, unicode.IsDigit) >= 0 {
		return matchNone
	}
	maxDistance := 1
	if termLength > 8 {
		maxDistance = 2
	}
	termRunes := []rune(term)
	for _, word := range words {
		if withinEditDistance(termRunes, []rune(word), maxDistance) {
			return matchFuzzy
		}
	}

	return matchNone
}

// foldText case-folds s and replaces compatibility forms such as full-width
// punctuation with their plain equivalents, reading a typographic apostrophe
// as a plain one, as splitWords does.
func foldText(s string) string {
	return strings.ReplaceAll(norm.NFKC.String(cases.Fold().String(s)), "’", "'")
}

// splitWords case-folds s into words free of diacritics and compatibility
// forms such as full-width letters or ligatures, so "Straße" reads "strasse"
// and a final sigma matches a capital one. It splits on anything that is
// neither a letter nor a digit, so release names such as "Sintel.2010.1080p"
// yield the same words as "sintel 2010 1080p".
// Apostrophes are dropped rather than split on, so "Llama's" reads "llamas".
func splitWords(s string) []string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range norm.NFKD.String(cases.Fold().String(s)) {
		switch {
		case unicode.Is(unicode.Mn, r), r == '\'', r == '’':
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(r)
		default:
			b.WriteByte(' ')
		}
	}

	return strings.Fields(b.String())
}

// withinEditDistance reports whether a and b are at most maxDistance edits
// apart, counting an insertion, deletion, substitution or swap of two
// adjacent characters as one edit each (optimal string alignment distance).
func withinEditDistance(a, b []rune, maxDistance int) bool {
	if lengthGap := len(a) - len(b); lengthGap > maxDistance || -lengthGap > maxDistance {
		return false
	}

	beforePrevious := make([]int, len(b)+1)
	previous := make([]int, len(b)+1)
	current := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(a); i++ {
		current[0] = i
		rowMin := current[0]
		for j := 1; j <= len(b); j++ {
			substitution := previous[j-1]
			if a[i-1] != b[j-1] {
				substitution++
			}
			current[j] = min(previous[j]+1, current[j-1]+1, substitution)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				current[j] = min(current[j], beforePrevious[j-2]+1)
			}
			rowMin = min(rowMin, current[j])
		}
		if rowMin > maxDistance {
			return false
		}
		beforePrevious, previous, current = previous, current, beforePrevious
	}

	return previous[len(b)] <= maxDistance
}
