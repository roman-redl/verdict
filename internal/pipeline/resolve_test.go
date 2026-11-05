package pipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

type fakeTitles struct {
	calls   []string
	byTitle map[string]*model.Work
	failOn  string
}

func (f *fakeTitles) SearchTitle(_ context.Context, title string) (*model.Work, error) {
	f.calls = append(f.calls, title)

	if title == f.failOn {
		return nil, errors.New("openalex down")
	}

	return f.byTitle[title], nil
}

func TestResolveTitles(t *testing.T) {
	finder := &fakeTitles{
		failOn: "A title whose lookup fails",
		byTitle: map[string]*model.Work{
			"Phototherapy for atopic dermatitis": {
				DOI: "https://doi.org/10.1/Match", Title: "Phototherapy for Atopic Dermatitis", Year: 2016,
			},
			"Laser therapy, sham controlled": {
				DOI: "https://doi.org/10.1/close", Title: "Laser therapy: a sham-controlled trial", Year: 2020,
			},
		},
	}
	text := "- Phototherapy for atopic dermatitis\n" +
		"2) Laser therapy, sham controlled\n" +
		"https://doi.org/10.1000/already-here\n" +
		"* Phototherapy for atopic dermatitis\n" + // a repeat
		"short\n" + // junk
		"\n" +
		"A title nobody knows\n" +
		"A title whose lookup fails\n"

	got := ResolveTitles(t.Context(), finder, text)

	require.Len(t, got, 5)
	assert.Equal(t, TitleResolution{
		Line: "Phototherapy for atopic dermatitis", Kind: ResolvedExact,
		DOI: "10.1/match", Year: 2016, Title: "Phototherapy for Atopic Dermatitis",
	}, got[0], "a case-insensitive title match is exact, the DOI is normalized")
	assert.Equal(t, ResolvedApproximate, got[1].Kind)
	assert.Equal(t, "10.1/close", got[1].DOI)
	assert.Equal(t, TitleResolution{Line: "https://doi.org/10.1000/already-here", Kind: ResolvedPassthrough}, got[2])
	assert.Equal(t, Unresolved, got[3].Kind)
	require.NoError(t, got[3].Err)
	assert.Equal(t, Unresolved, got[4].Kind)
	require.EqualError(t, got[4].Err, "openalex down")

	assert.Len(t, finder.calls, 4, "no lookup for the passthrough, the repeat or the junk line")
}

func TestResolveTitlesStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	finder := &fakeTitles{}
	assert.Empty(t, ResolveTitles(ctx, finder, "A reasonably long title\nAnother reasonably long title\n"))
	assert.Empty(t, finder.calls)
}

func TestStripBullet(t *testing.T) {
	const title = "a title"

	for in, want := range map[string]string{
		"- " + title:    title,
		"  * " + title:  title,
		"3. " + title:   title,
		"12) " + title:  title,
		"• " + title:    title,
		"  " + title:    title,
		"- - " + title:  "- " + title, // only the leading bullet goes
		"1999 " + title: "1999 " + title,
		"1)" + title:    "1)" + title, // a bullet needs a space after it
		"-" + title:     "-" + title,
	} {
		assert.Equal(t, want, stripBullet(in), in)
	}
}
