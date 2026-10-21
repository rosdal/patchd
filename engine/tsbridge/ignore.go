package tsbridge

import (
	"path"
	"strings"
)

// ignoreRules is a gitignore-style matcher. Each .gitignore applies to the
// directory it sits in; later rules win, and "!" re-includes. It supports the
// parts of gitignore real repos use: "*", "?", "**", character classes,
// anchoring with a leading or inner "/", and directory-only patterns with a
// trailing "/".
type ignoreRules struct {
	rules []ignoreRule
}

type ignoreRule struct {
	base    string // directory the rule is relative to, relative to the root ("" for the root)
	pattern string // without leading "!" or "/" and trailing "/"
	negate  bool
	dirOnly bool
	rooted  bool // contains a "/" other than a trailing one: match against the full relative path
}

// add parses gitignore text found in directory base (relative to the root).
func (r *ignoreRules) add(base, text string) {
	for line := range strings.Lines(text) {
		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, "\\#") || strings.HasPrefix(line, "\\!") {
			line = line[1:]
		} else if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = trimTrailingSpaces(line)
		if line == "" {
			continue
		}
		rule := ignoreRule{base: base}
		if strings.HasPrefix(line, "!") {
			rule.negate = true
			line = line[1:]
		}
		if strings.HasSuffix(line, "/") {
			rule.dirOnly = true
			line = strings.TrimRight(line, "/")
		}
		if strings.Contains(line, "/") {
			rule.rooted = true
			line = strings.TrimPrefix(line, "/")
		}
		if line == "" {
			continue
		}
		rule.pattern = line
		r.rules = append(r.rules, rule)
	}
}

// addPatterns adds patterns that apply from the root (patchd.yaml typescript.ignore).
func (r *ignoreRules) addPatterns(patterns []string) {
	r.add("", strings.Join(patterns, "\n"))
}

func trimTrailingSpaces(s string) string {
	for strings.HasSuffix(s, " ") && !strings.HasSuffix(s, "\\ ") {
		s = s[:len(s)-1]
	}
	return s
}

// ignored reports whether rel (relative to the root, forward slashes) is ignored.
func (r *ignoreRules) ignored(rel string, isDir bool) bool {
	ignored := false
	for _, rule := range r.rules {
		if rule.negate == ignored {
			// Only a rule that could flip the current answer matters.
			if rule.matches(rel, isDir) {
				ignored = !rule.negate
			}
		}
	}
	return ignored
}

func (rule ignoreRule) matches(rel string, isDir bool) bool {
	if rule.dirOnly && !isDir {
		return false
	}
	if rule.base != "" {
		if !strings.HasPrefix(rel, rule.base+"/") {
			return false
		}
		rel = rel[len(rule.base)+1:]
	}
	if rule.rooted {
		return globMatch(rule.pattern, rel)
	}
	return globMatch(rule.pattern, path.Base(rel))
}

// globMatch matches a gitignore glob against a slash-separated path. "**"
// matches any number of path segments; other wildcards stay within a segment.
func globMatch(pattern, name string) bool {
	if !strings.Contains(pattern, "**") {
		ok, err := path.Match(pattern, name)
		return err == nil && ok
	}
	return matchSegments(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func matchSegments(pat, segs []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			pat = pat[1:]
			if len(pat) == 0 {
				return true
			}
			for i := 0; i <= len(segs); i++ {
				if matchSegments(pat, segs[i:]) {
					return true
				}
			}
			return false
		}
		if len(segs) == 0 {
			return false
		}
		ok, err := path.Match(pat[0], segs[0])
		if err != nil || !ok {
			return false
		}
		pat, segs = pat[1:], segs[1:]
	}
	return len(segs) == 0
}
