package analyze

import (
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// cleanOpts say which comment and string syntax a language has.
type cleanOpts struct {
	slash    bool // // line comments
	block    bool // /* */ comments
	hash     bool // # line comments (not #[ attributes)
	dash     bool // -- line comments (SQL)
	backtick bool // `template strings`
	charLit  bool // ' starts a char literal only when it closes within a few bytes (Rust lifetimes)
	blankStr bool // blank string contents too (for brace counting)
}

var (
	jsClean   = cleanOpts{slash: true, block: true, backtick: true}
	jsBare    = cleanOpts{slash: true, block: true, backtick: true, blankStr: true}
	cClean    = cleanOpts{slash: true, block: true, charLit: true}
	cBare     = cleanOpts{slash: true, block: true, charLit: true, blankStr: true}
	phpClean  = cleanOpts{slash: true, block: true, hash: true}
	phpBare   = cleanOpts{slash: true, block: true, hash: true, blankStr: true}
	rubyClean = cleanOpts{hash: true}
)

// cleanCode blanks comments (and optionally string contents) with spaces,
// keeping every newline and byte offset so line numbers stay right.
func cleanCode(src string, o cleanOpts) string {
	b := []byte(src)
	n := len(b)
	for i := 0; i < n; {
		c := b[i]
		switch {
		case o.slash && c == '/' && i+1 < n && b[i+1] == '/':
			for i < n && b[i] != '\n' {
				b[i] = ' '
				i++
			}
		case o.hash && c == '#' && !(i+1 < n && b[i+1] == '['):
			for i < n && b[i] != '\n' {
				b[i] = ' '
				i++
			}
		case o.dash && c == '-' && i+1 < n && b[i+1] == '-':
			for i < n && b[i] != '\n' {
				b[i] = ' '
				i++
			}
		case o.block && c == '/' && i+1 < n && b[i+1] == '*':
			b[i], b[i+1] = ' ', ' '
			i += 2
			for i < n && !(b[i] == '*' && i+1 < n && b[i+1] == '/') {
				if b[i] != '\n' {
					b[i] = ' '
				}
				i++
			}
			if i < n {
				b[i] = ' '
				if i+1 < n {
					b[i+1] = ' '
				}
				i += 2
			}
		case c == '"' || c == '\'' || (o.backtick && c == '`'):
			if c == '\'' && o.charLit {
				j := i + 1
				if j < n && b[j] == '\\' {
					j += 2
				} else {
					_, sz := utf8.DecodeRune(b[j:])
					j += sz
				}
				if j < n && b[j] == '\'' {
					if o.blankStr {
						for k := i + 1; k < j; k++ {
							b[k] = ' '
						}
					}
					i = j + 1
				} else {
					i++
				}
				continue
			}
			q := c
			j := i + 1
			for j < n {
				if b[j] == '\\' {
					j += 2
					continue
				}
				if b[j] == q || (b[j] == '\n' && q != '`') {
					break
				}
				j++
			}
			if j > n {
				j = n
			}
			if o.blankStr {
				for k := i + 1; k < j; k++ {
					if b[k] != '\n' {
						b[k] = ' '
					}
				}
			}
			i = j + 1
		default:
			i++
		}
	}
	return string(b)
}

// braceDepths returns the { } nesting depth at the start of every line of
// bare (comment- and string-free) text; len = lines+1.
func braceDepths(lines []string) []int {
	d := make([]int, len(lines)+1)
	depth := 0
	for i, l := range lines {
		d[i] = depth
		for j := 0; j < len(l); j++ {
			switch l[j] {
			case '{':
				depth++
			case '}':
				if depth > 0 {
					depth--
				}
			}
		}
	}
	d[len(lines)] = depth
	return d
}

// blockEnd finds the last line of a brace block that starts on line i
// (0-based) of bare text: the line where the depth drops back. A header that
// continues over a few lines ("function x(\n a,\n) {") is followed.
func blockEnd(lines []string, depth []int, i int) int {
	base := depth[i]
	last := len(lines) - 1
	j := i
	for ; j <= last && j < i+6; j++ {
		if depth[j+1] > base {
			break
		}
		if strings.ContainsAny(lines[j], "{;}") {
			return i
		}
	}
	if j > last || j >= i+6 {
		return i
	}
	for k := j + 1; k <= last; k++ {
		if depth[k+1] <= base {
			return k
		}
	}
	return last
}

// lineIndex converts byte offsets into 1-based line numbers.
type lineIndex []int

func newLineIndex(s string) lineIndex {
	idx := lineIndex{0}
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			idx = append(idx, i+1)
		}
	}
	return idx
}

func (li lineIndex) line(off int) int {
	return sort.Search(len(li), func(i int) bool { return li[i] > off })
}

// docAbove reads the comment block right above line i (0-based) and returns
// its first sentence. Decorator/annotation lines in between are skipped.
func docAbove(raw []string, i int, hash bool) string {
	j := i - 1
	for j >= 0 {
		t := strings.TrimSpace(raw[j])
		if strings.HasPrefix(t, "@") || strings.HasPrefix(t, "#[") || (strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]")) {
			j--
			continue
		}
		break
	}
	if j < 0 {
		return ""
	}
	t := strings.TrimSpace(raw[j])
	var parts []string
	switch {
	case strings.HasSuffix(t, "*/"):
		for k := j; k >= 0 && j-k < 60; k-- {
			parts = append(parts, raw[k])
			if strings.Contains(raw[k], "/*") {
				break
			}
		}
	case strings.HasPrefix(t, "//"):
		for k := j; k >= 0 && j-k < 60 && strings.HasPrefix(strings.TrimSpace(raw[k]), "//"); k-- {
			parts = append(parts, raw[k])
		}
	case hash && strings.HasPrefix(t, "#") && !strings.HasPrefix(t, "#!") && !strings.HasPrefix(t, "#["):
		for k := j; k >= 0 && j-k < 60 && strings.HasPrefix(strings.TrimSpace(raw[k]), "#"); k-- {
			parts = append(parts, raw[k])
		}
	default:
		return ""
	}
	for l, r := 0, len(parts)-1; l < r; l, r = l+1, r-1 {
		parts[l], parts[r] = parts[r], parts[l]
	}
	return firstSentence(strings.Join(parts, "\n"))
}

// firstSentence turns a comment into its first sentence (≤ 200 chars).
func firstSentence(s string) string {
	var words []string
	for _, l := range strings.Split(s, "\n") {
		t := strings.TrimSpace(l)
		for _, p := range []string{"/**", "/*", "*/", "///", "//", "#", "*", `"""`, `'''`} {
			t = strings.TrimSpace(strings.TrimPrefix(t, p))
		}
		t = strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(t, "*/"), `"""`))
		if strings.HasPrefix(t, "@") || strings.HasPrefix(t, ":param") || strings.HasPrefix(t, "Args:") {
			break
		}
		if t == "" {
			if len(words) > 0 {
				break
			}
			continue
		}
		words = append(words, t)
	}
	out := strings.Join(words, " ")
	if i := strings.Index(out, ". "); i >= 0 {
		out = out[:i+1]
	}
	return clip(out, 200)
}

// clip shortens s to n runes with an ellipsis, collapsing whitespace.
func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "?")
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// --- HTTP paths ---

var (
	colonParamRe = regexp.MustCompile(`(^|/):([A-Za-z_][A-Za-z0-9_]*)(\([^)/]*\))?\??`)
	braceParamRe = regexp.MustCompile(`\{\*?([A-Za-z_][A-Za-z0-9_\-]*)(\.\.\.)?(?:[:=?][^{}]*(?:\{[^{}]*\}[^{}]*)*)?\}`)
	angleParamRe = regexp.MustCompile(`<(?:[A-Za-z_]+:)?([A-Za-z_][A-Za-z0-9_]*)>`)
	pathParamRe  = regexp.MustCompile(`\{([^{}]+)\}`)
	envNameRe    = regexp.MustCompile(`^[A-Z][A-Z0-9_]{2,}$`)
)

// normPath normalises a route path: leading slash, params as {name}.
func normPath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.ReplaceAll(p, "{$}", "")
	if p == "" {
		return "/"
	}
	p = colonParamRe.ReplaceAllString(p, "$1{$2}")
	p = braceParamRe.ReplaceAllString(p, "{$1}")
	p = angleParamRe.ReplaceAllString(p, "{$1}")
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}
	return p
}

// joinPath joins a route prefix and a path with exactly one slash. A bare
// "/" child adds nothing ("/api" + "/" = "/api").
func joinPath(a, b string) string {
	if b == "" || b == "/" {
		if a == "" {
			return b
		}
		return a
	}
	if a == "" {
		return b
	}
	return strings.TrimRight(a, "/") + "/" + strings.TrimLeft(b, "/")
}

// pathParams lists the {name} params of a normalised path.
func pathParams(p string) []string {
	var out []string
	for _, m := range pathParamRe.FindAllStringSubmatch(p, -1) {
		out = append(out, m[1])
	}
	return out
}

var httpMethods = map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "HEAD": true, "OPTIONS": true, "CONNECT": true, "TRACE": true}

// splitMethodPattern splits a Go 1.22 / "GET /x" pattern.
func splitMethodPattern(p string) (method, rest string) {
	p = strings.TrimSpace(p)
	if i := strings.IndexAny(p, " \t"); i > 0 && httpMethods[strings.ToUpper(p[:i])] {
		method, p = strings.ToUpper(p[:i]), strings.TrimSpace(p[i:])
	}
	if !strings.HasPrefix(p, "/") {
		// A host part ("example.com/x"): keep the path.
		if j := strings.IndexByte(p, '/'); j > 0 && !strings.ContainsAny(p[:j], "{:") {
			p = p[j:]
		}
	}
	return method, p
}

// strLit returns the contents of a quoted string literal token.
func strLit(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if len(s) < 2 {
		return "", false
	}
	q := s[0]
	if (q == '"' || q == '\'' || q == '`') && s[len(s)-1] == q {
		return s[1 : len(s)-1], true
	}
	return "", false
}

// isWordAt reports whether name appears in s as a whole identifier.
func isIdentByte(c byte) bool {
	return c == '_' || c == '$' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// identSet collects the identifiers in s (for the refs heuristic).
func identSet(s string) map[string]struct{} {
	set := map[string]struct{}{}
	for i := 0; i < len(s); {
		if isIdentByte(s[i]) && !(s[i] >= '0' && s[i] <= '9') {
			j := i + 1
			for j < len(s) && isIdentByte(s[j]) {
				j++
			}
			if j-i > 1 {
				set[s[i:j]] = struct{}{}
			}
			i = j
			continue
		}
		i++
	}
	return set
}

// matchParen returns the index just after the bracket that closes the one at
// s[open], counting () [] {} and skipping quoted strings; -1 if unbalanced.
func matchParen(s string, open int, limit int) int {
	depth := 0
	end := len(s)
	if limit > 0 && open+limit < end {
		end = open + limit
	}
	for i := open; i < end; i++ {
		switch c := s[i]; c {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
			if depth == 0 {
				return i + 1
			}
		case '"', '\'', '`':
			j := i + 1
			for j < end && s[j] != c {
				if s[j] == '\\' {
					j++
				}
				j++
			}
			i = j
		}
	}
	return -1
}

// splitTop splits s on sep at bracket depth 0, outside quotes.
func splitTop(s string, sep byte) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case '"', '\'', '`':
			j := i + 1
			for j < len(s) && s[j] != c {
				if s[j] == '\\' {
					j++
				}
				j++
			}
			i = j
		default:
			if c == sep && depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}

// needleAll returns the offsets of needle in s that start a word.
func needleAll(s, needle string) []int {
	var out []int
	for i := 0; ; {
		j := strings.Index(s[i:], needle)
		if j < 0 {
			return out
		}
		p := i + j
		i = p + len(needle)
		if p > 0 && isIdentByte(s[p-1]) {
			continue
		}
		out = append(out, p)
	}
}

// anchoredAll runs re (which must start with ^) at every word-start
// occurrence of needle in s, within window bytes, and returns absolute
// submatch offsets like FindAllStringSubmatchIndex.
func anchoredAll(s, needle string, re *regexp.Regexp, window int) [][]int {
	var out [][]int
	next := 0
	for _, p := range needleAll(s, needle) {
		if p < next {
			continue
		}
		m := re.FindStringSubmatchIndex(s[p:min(len(s), p+window)])
		if m == nil {
			continue
		}
		for k := range m {
			if m[k] >= 0 {
				m[k] += p
			}
		}
		out = append(out, m)
		next = m[1]
	}
	return out
}
