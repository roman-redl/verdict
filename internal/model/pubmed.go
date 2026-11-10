package model

// PubMedHits is the answer of a PubMed search: the PMIDs of the best matches
// in relevance order, and how many records matched in all.
type PubMedHits struct {
	PMIDs []string
	Total int
}

// PubMedRecord is the summary of one PubMed record. PubMed's summaries carry
// no abstract: the abstract comes later, from the sources of the enrichment.
type PubMedRecord struct {
	PMID  string
	Title string
	Year  int // zero when the record gives no publication year
	// DOI and PMCID are empty when the record has none (a DOI-less record
	// cannot enter the DOI-keyed pipeline).
	DOI   string
	PMCID string
	// PubTypes are NLM's publication types ("Randomized Controlled Trial",
	// "Meta-Analysis", ...), as PubMed spells them.
	PubTypes []string
}
