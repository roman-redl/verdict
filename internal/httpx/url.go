package httpx

import (
	"fmt"
	"net/url"
	"strings"
)

// URL builds a request URL: base, the path segments joined (a segment may
// itself contain '/', as DOIs do) and the query encoded. Parameters are never
// spliced into the string by hand — an unescaped '+' or '&' in a value would
// silently change the request.
func URL(base string, query url.Values, segments ...string) (string, error) {
	// JoinPath reads its elements as already escaped, so a literal '%' (rare,
	// but legal in a DOI) has to be doubled up front or the join fails.
	escaped := make([]string, len(segments))
	for i, s := range segments {
		escaped[i] = strings.ReplaceAll(s, "%", "%25")
	}

	u, err := url.JoinPath(base, escaped...)
	if err != nil {
		return "", fmt.Errorf("building a URL under %s: %w", base, err)
	}

	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	return u, nil
}
