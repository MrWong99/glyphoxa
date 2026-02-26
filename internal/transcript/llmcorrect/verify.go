package llmcorrect

import "strings"

// indexPair maps a token index in the original sequence to the corresponding
// index in the corrected sequence.
type indexPair struct {
	origIdx int
	corrIdx int
}

// changeSpan represents a contiguous region that differs between the original
// and corrected token sequences.
type changeSpan struct {
	origTokens []string
	corrTokens []string
}

// tokenLCS computes the longest common subsequence of two token slices and
// returns anchor pairs (indices into a and b) representing common tokens in
// order. Standard O(m×n) DP — token counts are small (transcript sentences).
func tokenLCS(a, b []string) []indexPair {
	m, n := len(a), len(b)
	if m == 0 || n == 0 {
		return nil
	}

	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}

	lcsLen := dp[m][n]
	if lcsLen == 0 {
		return nil
	}

	anchors := make([]indexPair, lcsLen)
	i, j, k := m, n, lcsLen-1
	for i > 0 && j > 0 {
		if a[i-1] == b[j-1] {
			anchors[k] = indexPair{origIdx: i - 1, corrIdx: j - 1}
			i--
			j--
			k--
		} else if dp[i-1][j] >= dp[i][j-1] {
			i--
		} else {
			j--
		}
	}
	return anchors
}

// extractChangeSpans walks the anchor list and collects gaps between anchored
// (unchanged) tokens. Each gap is a changeSpan representing a region that
// differs between the two token sequences.
func extractChangeSpans(orig, corr []string, anchors []indexPair) []changeSpan {
	var spans []changeSpan
	oi, ci := 0, 0
	for _, a := range anchors {
		if oi < a.origIdx || ci < a.corrIdx {
			spans = append(spans, changeSpan{
				origTokens: orig[oi:a.origIdx],
				corrTokens: corr[ci:a.corrIdx],
			})
		}
		oi = a.origIdx + 1
		ci = a.corrIdx + 1
	}
	if oi < len(orig) || ci < len(corr) {
		spans = append(spans, changeSpan{
			origTokens: orig[oi:],
			corrTokens: corr[ci:],
		})
	}
	return spans
}

// normalizeForLookup lowercases s and strips common trailing punctuation so
// that token spans like "Wispers." match corrections declared as "Wispers".
func normalizeForLookup(s string) string {
	return strings.ToLower(strings.TrimRight(s, ".,;:!?\"')"))
}

// verifyCorrectedText cross-references actual token-level changes between
// original and corrected against the reported corrections list. Any change
// span that does not correspond to a declared correction is reverted to the
// original tokens. Returns the verified text and only the confirmed
// corrections.
func verifyCorrectedText(original, corrected string, corrections []Correction) (string, []Correction) {
	if original == corrected {
		return original, corrections
	}

	origTokens := strings.Fields(original)
	corrTokens := strings.Fields(corrected)

	anchors := tokenLCS(origTokens, corrTokens)
	spans := extractChangeSpans(origTokens, corrTokens, anchors)

	type corrKey struct{ orig, corr string }
	lookup := make(map[corrKey]Correction, len(corrections))
	for _, c := range corrections {
		lookup[corrKey{normalizeForLookup(c.Original), normalizeForLookup(c.Corrected)}] = c
	}

	var result []string
	var verified []Correction
	oi, ci, spanIdx := 0, 0, 0

	for _, a := range anchors {
		if oi < a.origIdx || ci < a.corrIdx {
			span := spans[spanIdx]
			spanIdx++
			key := corrKey{
				normalizeForLookup(strings.Join(span.origTokens, " ")),
				normalizeForLookup(strings.Join(span.corrTokens, " ")),
			}
			if c, ok := lookup[key]; ok {
				result = append(result, span.corrTokens...)
				verified = append(verified, c)
			} else {
				result = append(result, span.origTokens...)
			}
		}
		result = append(result, origTokens[a.origIdx])
		oi = a.origIdx + 1
		ci = a.corrIdx + 1
	}

	if oi < len(origTokens) || ci < len(corrTokens) {
		span := spans[spanIdx]
		key := corrKey{
			normalizeForLookup(strings.Join(span.origTokens, " ")),
			normalizeForLookup(strings.Join(span.corrTokens, " ")),
		}
		if c, ok := lookup[key]; ok {
			result = append(result, span.corrTokens...)
			verified = append(verified, c)
		} else {
			result = append(result, span.origTokens...)
		}
	}

	return strings.Join(result, " "), verified
}
