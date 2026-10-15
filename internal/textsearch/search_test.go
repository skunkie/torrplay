// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package textsearch

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type release struct {
	name  string
	title string
}

func newRelease(name string, title string) release {
	return release{name: name, title: title}
}

func releaseFields(r release) []string {
	return []string{r.title, r.name}
}

func releaseNames(rs []release) []string {
	names := make([]string, 0, len(rs))
	for _, r := range rs {
		names = append(names, r.name)
	}

	return names
}

func TestSearch(t *testing.T) {
	library := []release{
		newRelease("Sintel.2010.1080p.mkv", ""),
		newRelease("Big_Buck_Bunny_720p", ""),
		newRelease("Tears.of.Steel.2012.BDRip", "Стальные слёзы"),
		newRelease("Caminandes.Llamigos.2016", ""),
		newRelease("Élan.Vital.2001", ""),
		newRelease("Llama's.Journey.2019", ""),
		newRelease("Wing’s.Flight.2021", ""),
		newRelease("Zo.2009", ""),
		newRelease("Hauptstraße.Tour.1998", ""),
		newRelease("ΚΑΛΟΣ.ΔΡΟΜΟΣ.2008", ""),
	}

	tests := []struct {
		name  string
		query string
		fuzzy bool
		want  []string
	}{
		{name: "matches words of dotted release names", query: "sintel 2010", want: []string{"Sintel.2010.1080p.mkv"}},
		{name: "ignores case and underscores", query: "BUCK bunny", want: []string{"Big_Buck_Bunny_720p"}},
		{name: "requires every term", query: "sintel 2014", want: []string{}},
		{name: "matches any field", query: "стальные", want: []string{"Tears.of.Steel.2012.BDRip"}},
		{name: "folds yo into ye", query: "слезы", want: []string{"Tears.of.Steel.2012.BDRip"}},
		{name: "strips diacritics", query: "elan", want: []string{"Élan.Vital.2001"}},
		{name: "drops apostrophes", query: "llamas journey", want: []string{"Llama's.Journey.2019"}},
		{name: "drops typographic apostrophes", query: "wings flight", want: []string{"Wing’s.Flight.2021"}},
		{name: "matches the start of words", query: "1080", want: []string{"Sintel.2010.1080p.mkv"}},
		{name: "matches inside words", query: "ntel", want: []string{"Sintel.2010.1080p.mkv"}},
		{name: "folds full-width characters", query: "ｓｉｎｔｅｌ", want: []string{"Sintel.2010.1080p.mkv"}},
		{name: "folds sharp s", query: "hauptstrasse", want: []string{"Hauptstraße.Tour.1998"}},
		{name: "folds final sigma", query: "καλος δρομος", want: []string{"ΚΑΛΟΣ.ΔΡΟΜΟΣ.2008"}},
		{name: "leaves typos unmatched without fuzzy", query: "sintl", want: []string{}},
		{name: "tolerates one typo in a mid-length term", query: "sintl", fuzzy: true, want: []string{"Sintel.2010.1080p.mkv"}},
		{name: "tolerates two typos in a long term", query: "camnandse", fuzzy: true, want: []string{"Caminandes.Llamigos.2016"}},
		{name: "allows only one typo in an eight-character term", query: "lalmigso", fuzzy: true, want: []string{}},
		{name: "keeps short terms exact", query: "zoo", fuzzy: true, want: []string{}},
		{name: "rejects terms beyond the edit distance", query: "sntle", fuzzy: true, want: []string{}},
		{name: "keeps terms with digits exact", query: "1080i", fuzzy: true, want: []string{}},
		{name: "leaves the listing untouched for a blank query", query: " \t ", want: releaseNames(library)},
		{name: "matches punctuation alone as text", query: " _ ", want: []string{"Big_Buck_Bunny_720p"}},
		{name: "keeps listing order for punctuation alone", query: ".", want: []string{
			"Sintel.2010.1080p.mkv", "Tears.of.Steel.2012.BDRip", "Caminandes.Llamigos.2016", "Élan.Vital.2001",
			"Llama's.Journey.2019", "Wing’s.Flight.2021", "Zo.2009", "Hauptstraße.Tour.1998", "ΚΑΛΟΣ.ΔΡΟΜΟΣ.2008",
		}},
		{name: "finds nothing for punctuation no field contains", query: " .,! ", want: []string{}},
		{name: "requires every punctuation part", query: "' .", want: []string{"Llama's.Journey.2019", "Wing’s.Flight.2021"}},
		{name: "rejects punctuation parts no single item has", query: "_ .", want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, releaseNames(Search(library, tt.query, tt.fuzzy, releaseFields)))
		})
	}

	t.Run("ranks fuzzy matches below exact ones", func(t *testing.T) {
		releases := []release{
			newRelease("Sintle.Trailer", ""),
			newRelease("Xsintel.Xtrailer", ""),
		}
		assert.Equal(t, []string{"Xsintel.Xtrailer", "Sintle.Trailer"}, releaseNames(Search(releases, "sintel trailer", true, releaseFields)))
	})

	t.Run("ranks whole words above word starts", func(t *testing.T) {
		releases := []release{
			newRelease("Springtime.Walk", ""),
			newRelease("Spring.2019", ""),
		}
		assert.Equal(t, []string{"Spring.2019", "Springtime.Walk"}, releaseNames(Search(releases, "spring", false, releaseFields)))
	})

	t.Run("ranks word starts above matches inside words", func(t *testing.T) {
		releases := []release{
			newRelease("Bigllama.Demo", ""),
			newRelease("Llamaland.Demo", ""),
		}
		assert.Equal(t, []string{"Llamaland.Demo", "Bigllama.Demo"}, releaseNames(Search(releases, "llama", false, releaseFields)))
	})

	t.Run("does not mistake neighboring episodes for typos", func(t *testing.T) {
		releases := []release{
			newRelease("Show.S01E02", ""),
			newRelease("Show.S01E03", ""),
		}
		assert.Equal(t, []string{"Show.S01E02"}, releaseNames(Search(releases, "show s01e02", true, releaseFields)))
	})

	t.Run("matches punctuation parts across fields but not across their join", func(t *testing.T) {
		releases := []release{newRelease(".2010", "Sintel!")}
		assert.Len(t, Search(releases, "! .", false, releaseFields), 1)
		assert.Empty(t, Search(releases, "! .", false, func(r release) []string { return []string{r.title} }))
		assert.Empty(t, Search(releases, "!.", false, releaseFields))
	})

	t.Run("keeps listing order among equal matches", func(t *testing.T) {
		// Long enough that an unstable sort would reorder ties.
		releases := make([]release, 0, 80)
		wholeWords := make([]string, 0, 40)
		insideWords := make([]string, 0, 40)
		for i := range 40 {
			wholeWord := fmt.Sprintf("Sintel.%02d", 39-i)
			insideWord := fmt.Sprintf("Xsintel.%02d", 39-i)
			releases = append(releases, newRelease(wholeWord, ""), newRelease(insideWord, ""))
			wholeWords = append(wholeWords, wholeWord)
			insideWords = append(insideWords, insideWord)
		}
		assert.Equal(t, append(wholeWords, insideWords...), releaseNames(Search(releases, "sintel", false, releaseFields)))
	})

	// The web client runs these cases against its local search too, so a
	// change on either side that alters these results fails until both agree.
	t.Run("matches the cases shared with the web client", func(t *testing.T) {
		data, err := os.ReadFile("testdata/shared-cases.json")
		require.NoError(t, err)
		var shared struct {
			Library []struct {
				Name  string `json:"name"`
				Title string `json:"title"`
			} `json:"library"`
			Cases []struct {
				Name  string   `json:"name"`
				Query string   `json:"query"`
				Want  []string `json:"want"`
			} `json:"cases"`
		}
		require.NoError(t, json.Unmarshal(data, &shared))
		library := make([]release, 0, len(shared.Library))
		for _, r := range shared.Library {
			library = append(library, newRelease(r.Name, r.Title))
		}
		require.NotEmpty(t, shared.Cases)

		for _, tt := range shared.Cases {
			t.Run(tt.Name, func(t *testing.T) {
				assert.Equal(t, tt.Want, releaseNames(Search(library, tt.Query, false, releaseFields)))
			})
		}
	})
}

func TestQuery_Matches(t *testing.T) {
	tests := []struct {
		name   string
		query  string
		fuzzy  bool
		fields []string
		want   bool
	}{
		{name: "matches every term across fields", query: "sintel 2010", fields: []string{"Sintel", "2010.1080p"}, want: true},
		{name: "requires every term", query: "sintel 2014", fields: []string{"Sintel.2010"}, want: false},
		{name: "leaves typos unmatched without fuzzy", query: "sintl", fields: []string{"Sintel"}, want: false},
		{name: "tolerates typos with fuzzy", query: "sintl", fuzzy: true, fields: []string{"Sintel"}, want: true},
		{name: "matches anything for a blank query", query: "  ", fields: []string{"Sintel"}, want: true},
		{name: "matches punctuation alone as text", query: "...", fields: []string{"Sintel..."}, want: true},
		{name: "rejects punctuation the fields lack", query: "...", fields: []string{"Sintel.2010"}, want: false},
		{name: "folds full-width punctuation", query: "！", fields: []string{"Sintel!"}, want: true},
		{name: "matches each punctuation part in any field", query: ". .", fields: []string{"Sintel.", ".2010"}, want: true},
		{name: "requires every punctuation part", query: "- .", fields: []string{"Sintel-2010"}, want: false},
		{name: "never matches punctuation across fields", query: "..", fields: []string{"Sintel.", ".2010"}, want: false},
		{name: "reads a typographic apostrophe as a plain one", query: "'", fields: []string{"Wing’s"}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, NewQuery(tt.query, tt.fuzzy).Matches(tt.fields...))
		})
	}
}

func TestWithinEditDistance(t *testing.T) {
	tests := []struct {
		name        string
		a, b        string
		maxDistance int
		want        bool
	}{
		{name: "identical words need no edits", a: "sintel", b: "sintel", maxDistance: 0, want: true},
		{name: "a missing character is one edit", a: "sintl", b: "sintel", maxDistance: 1, want: true},
		{name: "an adjacent swap is one edit", a: "sintle", b: "sintel", maxDistance: 1, want: true},
		{name: "two swaps exceed one edit", a: "snitle", b: "sintel", maxDistance: 1, want: false},
		{name: "two swaps fit within two edits", a: "snitle", b: "sintel", maxDistance: 2, want: true},
		{name: "a length gap beyond the limit fails early", a: "sin", b: "sintel", maxDistance: 2, want: false},
		{name: "an empty word is as many edits as the other is long", a: "", b: "ab", maxDistance: 2, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, withinEditDistance([]rune(tt.a), []rune(tt.b), tt.maxDistance))
		})
	}
}
