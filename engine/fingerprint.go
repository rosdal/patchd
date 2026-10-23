package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/patchd/patchd/engine/taint"
)

// Fingerprints identify a finding across commits. They hash what the
// finding is about, never where it is: the rule, the file, the enclosing
// function's name (without the line:column of anonymous functions), the
// sink call's text and the source access's text, with whitespace removed.
// Moving code or reformatting keeps them; renaming the function or editing
// the sink call changes them.

var anonPosition = regexp.MustCompile(`@\d+:\d+`)

// fingerprintBase hashes a finding's identity.
func fingerprintBase(rule, path, funcName, sinkText, sourceText string) string {
	h := sha256.New()
	for _, part := range []string{
		"patchd/v1", rule, path,
		anonPosition.ReplaceAllString(funcName, ""),
		normaliseCode(sinkText), normaliseCode(sourceText),
	} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// normaliseCode removes whitespace, so reformatting doesn't change the
// text.
func normaliseCode(s string) string {
	return strings.Join(strings.Fields(s), "")
}

// assignFingerprints resolves collisions within one function: findings that
// hash the same (the same sink and source written twice) get an occurrence
// index in source order.
func assignFingerprints(fs []Finding, _ *taint.FuncResult) {
	byBase := map[string][]int{}
	for i := range fs {
		byBase[fs[i].Fingerprint] = append(byBase[fs[i].Fingerprint], i)
	}
	for base, idx := range byBase {
		if len(idx) < 2 {
			continue
		}
		slices.SortFunc(idx, func(a, b int) int {
			la, lb := fs[a].Location, fs[b].Location
			if la.StartLine != lb.StartLine {
				return la.StartLine - lb.StartLine
			}
			return la.StartColumn - lb.StartColumn
		})
		for n, i := range idx[1:] {
			sum := sha256.Sum256([]byte(base + "#" + strconv.Itoa(n+2)))
			fs[i].Fingerprint = hex.EncodeToString(sum[:])[:32]
		}
	}
}
