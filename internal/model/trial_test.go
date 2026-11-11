package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	regNCT     = "NCT01234567"
	regNCT1    = "NCT00000001"
	regNCT2    = "NCT00000002"
	regISRCTN  = "ISRCTN15517081"
	regACTRN   = "ACTRN12616000123456"
	regUMIN    = "UMIN000012345"
	regChiCTR  = "CHICTR13003456"
	regPROSPER = "PROSPERO CRD42018012345"
)

func TestTrialRegistrations(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want []string
	}{
		{"empty", "", nil},
		{"no registration", "A randomised trial of 357 participants in 21 hospitals.", nil},
		{"clinicaltrials.gov", "Trial registration: ClinicalTrials.gov NCT01234567.", []string{regNCT}},
		{"nct with a space, lower case", "registered as nct 01234567", []string{regNCT}},
		{"nct in a url", "https://clinicaltrials.gov/ct2/show/NCT00012345?term=x", []string{"NCT00012345"}},
		{"nct with nine digits is not an id", "NCT012345678", nil},
		{"isrctn", "ISRCTN15517081.", []string{regISRCTN}},
		{"isrctn with a space", "ISRCTN 15517081", []string{regISRCTN}},
		{"actrn", regACTRN, []string{regACTRN}},
		{"actrn with a suffix letter", "ACTRN12616000123456p", []string{regACTRN}},
		{"chictr with a category", "ChiCTR-TRC-13003456", []string{regChiCTR}},
		{"chictr with a long category", "Registered at ChiCTR-IOR-16008123.", []string{"CHICTR16008123"}},
		{"chictr digits only", "ChiCTR2000030000", []string{"CHICTR2000030000"}},
		{"both chictr forms are one id", "ChiCTR-TRC-13003456 and ChiCTR13003456", []string{regChiCTR}},
		{"eudract", "EudraCT 2012-001234-56", []string{"2012-001234-56"}},
		{"drks", "DRKS00012345", []string{"DRKS00012345"}},
		{"ctri", "CTRI/2018/05/013456", []string{"CTRI/2018/05/013456"}},
		{"ctri with spaces", "CTRI / 2012 / 05 / 002678", []string{"CTRI/2012/05/002678"}},
		{"umin", regUMIN, []string{regUMIN}},
		{"umin ctr", "UMIN-CTR ID 000012345", []string{regUMIN}},
		{"jprn umin", "JPRN-UMIN000012345", []string{regUMIN}},
		{"irct", "IRCT20150303021315N1", []string{"IRCT20150303021315N1"}},
		{"irct old", "irct2013011012345n1", []string{"IRCT2013011012345N1"}},
		{"ntr", "Netherlands Trial Register NTR1234", []string{"NTR1234"}},
		{"nl", "NL7254", []string{"NL7254"}},
		{"nl in lower case is a word", "nl7254", nil},
		{"nl4-3 is a virus strain", "the NL4-3 strain", nil},
		{"pactr", "PACTR201409000890128", []string{"PACTR201409000890128"}},
		{"prospero is a review registration", regPROSPER, nil},
		{
			"duplicates collapse, sorted", "NCT00000002, NCT00000001 and again NCT00000002",
			[]string{regNCT1, regNCT2},
		},
		{
			"several registries", "ISRCTN15517081; ClinicalTrials.gov NCT01234567",
			[]string{regISRCTN, regNCT},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, TrialRegistrations(tc.text))
		})
	}
}

func TestNormalizeRegistrations(t *testing.T) {
	assert.Equal(t, []string{regISRCTN, regNCT},
		NormalizeRegistrations([]string{" nct 01234567", "", "isrctn15517081", "NCT 01234567"}))
	assert.Empty(t, NormalizeRegistrations(nil))
}

func TestTrialIDs(t *testing.T) {
	t.Run("the stored field wins", func(t *testing.T) {
		var e EnrichedPaper
		require.NoError(t, json.Unmarshal(
			[]byte(`{"abstract":"NCT00000001","trial_registrations":["ISRCTN15517081"],"flags":{}}`), &e))

		assert.Equal(t, []string{regISRCTN}, e.TrialIDs())
	})

	t.Run("an artifact without the field falls back to the abstract", func(t *testing.T) {
		var e EnrichedPaper
		require.NoError(t, json.Unmarshal([]byte(`{"title":"T","abstract":"Registered: NCT00000001.","flags":{}}`), &e))
		assert.Empty(t, e.TrialRegistrations)
		assert.Equal(t, []string{regNCT1}, e.TrialIDs())
	})

	t.Run("nothing named", func(t *testing.T) {
		assert.Empty(t, (&EnrichedPaper{}).TrialIDs())
	})
}

func TestTrialRegistrationsSerialize(t *testing.T) {
	b, err := json.Marshal(EnrichedPaper{TrialRegistrations: []string{regNCT1}})
	require.NoError(t, err)
	assert.Contains(t, string(b), `"trial_registrations":["NCT00000001"]`)

	b, err = json.Marshal(EnrichedPaper{})
	require.NoError(t, err)
	assert.NotContains(t, string(b), "trial_registrations", "an empty list stays out of the artifact")
}

func FuzzTrialRegistrations(f *testing.F) {
	f.Add("ISRCTN15517081 NCT01234567 CTRI/2018/05/013456")
	f.Add("")

	f.Fuzz(func(t *testing.T, text string) {
		got := TrialRegistrations(text)
		assert.Equal(t, got, NormalizeRegistrations(got), "the result is already normalized")
	})
}
