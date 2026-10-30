package analyze

import (
	"regexp"
	"strings"
)

const sqlIdent = "(?:[\\w$]+|\"[^\"]+\"|`[^`]+`|\\[[^\\]]+\\])"
const sqlQName = sqlIdent + `(?:\s*\.\s*` + sqlIdent + `)*`

var (
	sqlDownMarkRe = regexp.MustCompile(`(?im)^\s*--\s*(\+goose\s+down|migrate:down|\+migrate\s+down|//\s*@UNDO)`)
	sqlCreateRe   = regexp.MustCompile(`(?is)^CREATE\s+(?:OR\s+REPLACE\s+)?(?:(?:GLOBAL|LOCAL)\s+)?(?:TEMP(?:ORARY)?\s+|UNLOGGED\s+)?(VIRTUAL\s+)?TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?(` + sqlQName + `)\s*(.*)$`)
	sqlAlterRe    = regexp.MustCompile(`(?is)^ALTER\s+TABLE\s+(?:IF\s+EXISTS\s+)?(?:ONLY\s+)?(` + sqlQName + `)\s+(.*)$`)
	sqlDropRe     = regexp.MustCompile(`(?is)^DROP\s+TABLE\s+(?:IF\s+EXISTS\s+)?(.+?)(?:\s+(?:CASCADE|RESTRICT))?\s*$`)
	sqlRenameRe   = regexp.MustCompile(`(?is)^RENAME\s+TABLE\s+(` + sqlQName + `)\s+TO\s+(` + sqlQName + `)`)
	sqlUniqIdxRe  = regexp.MustCompile(`(?is)^CREATE\s+UNIQUE\s+INDEX\b.*?\bON\s+(?:ONLY\s+)?(` + sqlQName + `)\s*(?:USING\s+\w+\s*)?\(([^()]*)\)`)
	sqlRefRe      = regexp.MustCompile(`(?is)\bREFERENCES\s+(` + sqlQName + `)\s*(?:\(\s*([^)]*)\))?`)
	sqlUsingRe    = regexp.MustCompile(`(?is)^USING\s+\w+\s*\(`)
	sqlFKRe       = regexp.MustCompile(`(?is)^FOREIGN\s+KEY\s*\(([^)]*)\)\s*REFERENCES\s+(` + sqlQName + `)\s*(?:\(([^)]*)\))?`)
	sqlPKRe       = regexp.MustCompile(`(?is)^PRIMARY\s+KEY\s*(?:\w+\s*)?\(([^)]*)\)`)
	sqlUniqRe     = regexp.MustCompile(`(?is)^UNIQUE\s*(?:KEY|INDEX)?\s*(?:` + sqlIdent + `\s*)?\(([^)]*)\)`)
	sqlIdxRe      = regexp.MustCompile(`(?is)^(?:KEY|INDEX|FULLTEXT|SPATIAL)\b\s*(?:` + sqlIdent + `\s*)?\(`)
	sqlUniqueWord = regexp.MustCompile(`(?i)\bUNIQUE\b`)
	sqlPKWords    = regexp.MustCompile(`(?i)\bPRIMARY\s+KEY\b`)
	sqlNotNull    = regexp.MustCompile(`(?i)\bNOT\s+NULL\b`)
	sqlActRename  = regexp.MustCompile(`(?is)^RENAME\s+TO\s+(` + sqlQName + `)$`)
	sqlActRenCol  = regexp.MustCompile(`(?is)^RENAME\s+(?:COLUMN\s+)?(` + sqlIdent + `)\s+TO\s+(` + sqlIdent + `)$`)
	sqlActAdd     = regexp.MustCompile(`(?is)^ADD\s+(?:COLUMN\s+)?(?:IF\s+NOT\s+EXISTS\s+)?(.+)$`)
	sqlActDrop    = regexp.MustCompile(`(?is)^DROP\s+(?:COLUMN\s+)?(?:IF\s+EXISTS\s+)?(` + sqlIdent + `)`)
	sqlActType    = regexp.MustCompile(`(?is)^ALTER\s+(?:COLUMN\s+)?(` + sqlIdent + `)\s+(?:SET\s+DATA\s+)?TYPE\s+(.+?)(?:\s+USING\s+.*)?$`)
	sqlActNull    = regexp.MustCompile(`(?is)^ALTER\s+(?:COLUMN\s+)?(` + sqlIdent + `)\s+(SET|DROP)\s+NOT\s+NULL`)
	sqlActModify  = regexp.MustCompile(`(?is)^MODIFY\s+(?:COLUMN\s+)?(.+)$`)
	sqlActChange  = regexp.MustCompile(`(?is)^CHANGE\s+(?:COLUMN\s+)?(` + sqlIdent + `)\s+(.+)$`)
)

var sqlStopWords = map[string]bool{"CONSTRAINT": true, "PRIMARY": true, "NOT": true, "NULL": true, "UNIQUE": true,
	"REFERENCES": true, "DEFAULT": true, "CHECK": true, "COLLATE": true, "GENERATED": true, "AUTO_INCREMENT": true,
	"AUTOINCREMENT": true, "IDENTITY": true, "ON": true, "COMMENT": true, "AS": true, "CHARSET": true}

// unquoteIdent strips "x" `x` [x] quoting and any schema prefix.
func unquoteIdent(s string) string {
	s = strings.TrimSpace(s)
	parts := splitIdentDots(s)
	last := strings.TrimSpace(parts[len(parts)-1])
	if len(last) >= 2 {
		switch {
		case last[0] == '"' && last[len(last)-1] == '"', last[0] == '`' && last[len(last)-1] == '`', last[0] == '[' && last[len(last)-1] == ']':
			return last[1 : len(last)-1]
		}
	}
	return last
}

func splitIdentDots(s string) []string {
	var out []string
	start := 0
	var q byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case q != 0:
			if c == q || (q == '[' && c == ']') {
				q = 0
			}
		case c == '"' || c == '`' || c == '[':
			q = c
		case c == '.':
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// sqlStatements splits SQL into statements at semicolons outside quotes and
// dollar-quoted bodies; offsets are kept for line numbers.
func sqlStatements(src string) []struct {
	text string
	off  int
} {
	var out []struct {
		text string
		off  int
	}
	start := 0
	n := len(src)
	emit := func(end int) {
		t := src[start:end]
		trimmed := strings.TrimLeft(t, " \t\r\n")
		if strings.TrimSpace(trimmed) != "" {
			out = append(out, struct {
				text string
				off  int
			}{strings.TrimSpace(trimmed), start + len(t) - len(trimmed)})
		}
	}
	for i := 0; i < n; i++ {
		c := src[i]
		switch c {
		case '\'', '"', '`':
			j := i + 1
			for j < n {
				if src[j] == c {
					if c == '\'' && j+1 < n && src[j+1] == '\'' {
						j += 2
						continue
					}
					break
				}
				j++
			}
			i = j
		case '$':
			j := i + 1
			for j < n && (isIdentByte(src[j]) && src[j] != '$') {
				j++
			}
			if j < n && src[j] == '$' {
				tag := src[i : j+1]
				if k := strings.Index(src[j+1:], tag); k >= 0 {
					i = j + 1 + k + len(tag) - 1
				}
			}
		case ';':
			emit(i)
			start = i + 1
		}
	}
	emit(n)
	return out
}

// isDownMigration skips rollback files (they would drop every table).
func isDownMigration(rel string) bool {
	l := strings.ToLower(rel)
	base := l[strings.LastIndex(l, "/")+1:]
	return strings.Contains(base, ".down.") || strings.Contains(base, "_down.") || strings.Contains(base, "-down.") ||
		strings.HasSuffix(base, "down.sql") || strings.Contains(l, "/down/") || strings.Contains(base, ".rollback.")
}

// sqlFiles reads every .sql file (path order: later migrations win) and
// every Prisma schema.
func (a *analyzer) sqlFiles() {
	for _, f := range a.files {
		if f.content == nil {
			continue
		}
		switch {
		case f.lang == "SQL":
			if isDownMigration(f.rel) {
				continue
			}
			src := string(f.content)
			if loc := sqlDownMarkRe.FindStringIndex(src); loc != nil {
				src = src[:loc[0]]
			}
			clean := cleanCode(src, cleanOpts{dash: true, block: true})
			li := newLineIndex(clean)
			for _, st := range sqlStatements(clean) {
				a.sqlStatement(f, st.text, li.line(st.off))
			}
		case f.lang == "Prisma":
			a.prisma(f)
		}
	}
	// Resolve "REFERENCES t" without a column to the table's primary key.
	for _, t := range a.tables {
		for i, c := range t.Columns {
			if c.Ref == "" || strings.Contains(c.Ref, ".") {
				continue
			}
			if rt, ok := a.tables[strings.ToLower(c.Ref)]; ok {
				for _, rc := range rt.Columns {
					if rc.PK {
						t.Columns[i].Ref = rt.Name + "." + rc.Name
						break
					}
				}
			}
		}
	}
}

func (a *analyzer) table(name string) *Table {
	return a.tables[strings.ToLower(name)]
}

func colIndex(t *Table, name string) int {
	for i, c := range t.Columns {
		if strings.EqualFold(c.Name, name) {
			return i
		}
	}
	return -1
}

func splitCols(s string) []string {
	var out []string
	for _, p := range splitTop(s, ',') {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// "col ASC" / "col(10)" in index definitions.
		fs := strings.Fields(p)
		name := fs[0]
		if i := strings.IndexByte(name, '('); i > 0 {
			name = name[:i]
		}
		out = append(out, unquoteIdent(name))
	}
	return out
}

func (a *analyzer) sqlStatement(f *fileInfo, stmt string, line int) {
	if len(stmt) < 10 {
		return
	}
	head := strings.ToUpper(stmt[:min(len(stmt), 12)])
	switch {
	case strings.HasPrefix(head, "CREATE"):
		if m := sqlCreateRe.FindStringSubmatch(stmt); m != nil {
			a.sqlCreate(f, m, line)
			return
		}
		if m := sqlUniqIdxRe.FindStringSubmatch(stmt); m != nil {
			if t := a.table(unquoteIdent(m[1])); t != nil {
				if cols := splitCols(m[2]); len(cols) == 1 {
					if i := colIndex(t, cols[0]); i >= 0 {
						t.Columns[i].Unique = true
					}
				}
			}
		}
	case strings.HasPrefix(head, "ALTER"):
		if m := sqlAlterRe.FindStringSubmatch(stmt); m != nil {
			a.sqlAlter(f, unquoteIdent(m[1]), m[2], line)
		}
	case strings.HasPrefix(head, "DROP"):
		if m := sqlDropRe.FindStringSubmatch(stmt); m != nil {
			for _, n := range splitTop(m[1], ',') {
				delete(a.tables, strings.ToLower(unquoteIdent(n)))
			}
		}
	case strings.HasPrefix(head, "RENAME"):
		if m := sqlRenameRe.FindStringSubmatch(stmt); m != nil {
			a.renameTable(unquoteIdent(m[1]), unquoteIdent(m[2]))
		}
	}
}

func (a *analyzer) renameTable(from, to string) {
	t := a.table(from)
	if t == nil {
		return
	}
	delete(a.tables, strings.ToLower(from))
	t.Name = to
	a.tables[strings.ToLower(to)] = t
}

func (a *analyzer) sqlCreate(f *fileInfo, m []string, line int) {
	name := unquoteIdent(m[2])
	rest := strings.TrimSpace(m[3])
	t := &Table{Name: name, File: f.rel, Line: line, Source: "sql", Columns: []Column{}}
	a.tables[strings.ToLower(name)] = t
	if m[1] != "" {
		// CREATE VIRTUAL TABLE x USING fts5(a, b, tokenize=…)
		if loc := sqlUsingRe.FindStringIndex(rest); loc != nil {
			end := matchParen(rest, loc[1]-1, 0)
			if end > 0 {
				for _, p := range splitTop(rest[loc[1]:end-1], ',') {
					p = strings.TrimSpace(p)
					if p == "" || strings.Contains(p, "=") {
						continue
					}
					t.Columns = append(t.Columns, Column{Name: unquoteIdent(strings.Fields(p)[0])})
				}
			}
		}
		return
	}
	if !strings.HasPrefix(rest, "(") {
		return // CREATE TABLE … AS SELECT
	}
	end := matchParen(rest, 0, 0)
	if end < 0 {
		end = len(rest)
	}
	body := rest[1:max(1, end-1)]
	for _, item := range splitTop(body, ',') {
		a.sqlItem(t, strings.Join(strings.Fields(item), " "))
	}
}

// sqlItem applies one column definition or table constraint.
func (a *analyzer) sqlItem(t *Table, item string) {
	if item == "" {
		return
	}
	up := strings.ToUpper(item)
	if strings.HasPrefix(up, "CONSTRAINT ") {
		fs := strings.SplitN(item, " ", 3)
		if len(fs) < 3 {
			return
		}
		item = fs[2]
		up = strings.ToUpper(item)
	}
	switch {
	case strings.HasPrefix(up, "PRIMARY KEY"):
		if m := sqlPKRe.FindStringSubmatch(item); m != nil {
			for _, c := range splitCols(m[1]) {
				if i := colIndex(t, c); i >= 0 {
					t.Columns[i].PK = true
				}
			}
		}
		return
	case strings.HasPrefix(up, "FOREIGN KEY"):
		if m := sqlFKRe.FindStringSubmatch(item); m != nil {
			from := splitCols(m[1])
			to := splitCols(m[3])
			rt := unquoteIdent(m[2])
			for k, c := range from {
				if i := colIndex(t, c); i >= 0 {
					if k < len(to) {
						t.Columns[i].Ref = rt + "." + to[k]
					} else {
						t.Columns[i].Ref = rt
					}
				}
			}
		}
		return
	case strings.HasPrefix(up, "UNIQUE"):
		if m := sqlUniqRe.FindStringSubmatch(item); m != nil {
			if cols := splitCols(m[1]); len(cols) == 1 {
				if i := colIndex(t, cols[0]); i >= 0 {
					t.Columns[i].Unique = true
				}
			}
			return
		}
	case strings.HasPrefix(up, "CHECK") && strings.Contains(up, "("), strings.HasPrefix(up, "EXCLUDE "),
		strings.HasPrefix(up, "LIKE "), strings.HasPrefix(up, "PERIOD "):
		return
	case sqlIdxRe.MatchString(item):
		return
	}
	col := parseColumn(item)
	if col.Name == "" {
		return
	}
	if i := colIndex(t, col.Name); i >= 0 {
		t.Columns[i] = col
	} else {
		t.Columns = append(t.Columns, col)
	}
}

// parseColumn reads "name TYPE(…) constraints…".
func parseColumn(item string) Column {
	item = strings.TrimSpace(item)
	var name, rest string
	if item == "" {
		return Column{}
	}
	switch item[0] {
	case '"', '`', '[':
		closeCh := item[0]
		if closeCh == '[' {
			closeCh = ']'
		}
		j := strings.IndexByte(item[1:], closeCh)
		if j < 0 {
			return Column{}
		}
		name, rest = item[1:j+1], item[j+2:]
	default:
		fs := strings.SplitN(item, " ", 2)
		name = fs[0]
		if len(fs) > 1 {
			rest = fs[1]
		}
	}
	c := Column{Name: name}
	// Type = words until a constraint keyword (at paren depth 0).
	var typ []string
	words := splitTop(strings.TrimSpace(rest), ' ')
	k := 0
	for ; k < len(words); k++ {
		w := strings.TrimSpace(words[k])
		if w == "" {
			continue
		}
		uw := strings.ToUpper(w)
		if i := strings.IndexByte(uw, '('); i > 0 {
			uw = uw[:i]
		}
		if sqlStopWords[uw] || (uw == "CHARACTER" && k+1 < len(words) && strings.EqualFold(words[k+1], "SET")) {
			break
		}
		typ = append(typ, w)
	}
	c.Type = strings.Join(typ, " ")
	tail := strings.Join(words[min(k, len(words)):], " ")
	c.PK = sqlPKWords.MatchString(tail)
	c.NotNull = sqlNotNull.MatchString(tail)
	c.Unique = sqlUniqueWord.MatchString(tail)
	if m := sqlRefRe.FindStringSubmatch(tail); m != nil {
		c.Ref = unquoteIdent(m[1])
		if cols := splitCols(m[2]); len(cols) > 0 {
			c.Ref += "." + cols[0]
		}
	}
	return c
}

func (a *analyzer) sqlAlter(f *fileInfo, name, rest string, line int) {
	t := a.table(name)
	for _, act := range splitTop(rest, ',') {
		act = strings.Join(strings.Fields(act), " ")
		if act == "" {
			continue
		}
		if m := sqlActRename.FindStringSubmatch(act); m != nil {
			a.renameTable(name, unquoteIdent(m[1]))
			name = unquoteIdent(m[1])
			t = a.table(name)
			continue
		}
		if t == nil {
			if !strings.HasPrefix(strings.ToUpper(act), "ADD") {
				continue
			}
			t = &Table{Name: name, File: f.rel, Line: line, Source: "sql", Columns: []Column{}}
			a.tables[strings.ToLower(name)] = t
		}
		switch {
		case sqlActRenCol.MatchString(act):
			m := sqlActRenCol.FindStringSubmatch(act)
			if i := colIndex(t, unquoteIdent(m[1])); i >= 0 {
				t.Columns[i].Name = unquoteIdent(m[2])
			}
		case sqlActAdd.MatchString(act):
			a.sqlItem(t, sqlActAdd.FindStringSubmatch(act)[1])
		case sqlActDrop.MatchString(act):
			m := sqlActDrop.FindStringSubmatch(act)
			col := unquoteIdent(m[1])
			switch strings.ToUpper(col) {
			case "CONSTRAINT", "INDEX", "PRIMARY", "FOREIGN", "KEY", "DEFAULT":
				continue
			}
			if i := colIndex(t, col); i >= 0 {
				t.Columns = append(t.Columns[:i], t.Columns[i+1:]...)
			}
		case sqlActType.MatchString(act):
			m := sqlActType.FindStringSubmatch(act)
			if i := colIndex(t, unquoteIdent(m[1])); i >= 0 {
				t.Columns[i].Type = strings.TrimSpace(m[2])
			}
		case sqlActNull.MatchString(act):
			m := sqlActNull.FindStringSubmatch(act)
			if i := colIndex(t, unquoteIdent(m[1])); i >= 0 {
				t.Columns[i].NotNull = strings.EqualFold(m[2], "SET")
			}
		case sqlActModify.MatchString(act):
			a.sqlItem(t, sqlActModify.FindStringSubmatch(act)[1])
		case sqlActChange.MatchString(act):
			m := sqlActChange.FindStringSubmatch(act)
			if i := colIndex(t, unquoteIdent(m[1])); i >= 0 {
				t.Columns = append(t.Columns[:i], t.Columns[i+1:]...)
			}
			a.sqlItem(t, m[2])
		}
	}
}

// --- Prisma ---

var (
	prismaBlockRe = regexp.MustCompile(`^\s*(model|enum|view|type)\s+(\w+)\s*\{`)
	prismaFieldRe = regexp.MustCompile(`^\s*(\w+)\s+(\w+(?:\([^)]*\))?)(\[\])?(\?)?\s*(.*)$`)
	prismaRelRe   = regexp.MustCompile(`@relation\(([^)]*)\)`)
	prismaListRe  = regexp.MustCompile(`(fields|references)\s*:\s*\[([^\]]*)\]`)
	prismaAttrRe  = regexp.MustCompile(`^\s*@@(id|unique)\s*\(\s*(?:fields\s*:\s*)?\[([^\]]*)\]`)
)

func (a *analyzer) prisma(f *fileInfo) {
	lines := strings.Split(string(f.content), "\n")
	models := map[string]bool{}
	for _, l := range lines {
		if m := prismaBlockRe.FindStringSubmatch(l); m != nil && (m[1] == "model" || m[1] == "view") {
			models[m[2]] = true
		}
	}
	type rel struct {
		t        *Table
		from, to []string
		target   string
	}
	var rels []rel
	f.parsed = true
	for i := 0; i < len(lines); i++ {
		m := prismaBlockRe.FindStringSubmatch(lines[i])
		if m == nil || !(m[1] == "model" || m[1] == "view") {
			continue
		}
		t := &Table{Name: m[2], File: f.rel, Line: i + 1, Source: "prisma", Columns: []Column{}}
		a.tables[strings.ToLower(m[2])] = t
		for i++; i < len(lines); i++ {
			l := strings.TrimSpace(lines[i])
			if l == "}" {
				break
			}
			if j := strings.Index(l, "//"); j >= 0 {
				l = strings.TrimSpace(l[:j])
			}
			if l == "" {
				continue
			}
			if am := prismaAttrRe.FindStringSubmatch(l); am != nil {
				cols := splitCols(am[2])
				for _, c := range cols {
					if k := colIndex(t, c); k >= 0 {
						if am[1] == "id" {
							t.Columns[k].PK = true
						} else if len(cols) == 1 {
							t.Columns[k].Unique = true
						}
					}
				}
				continue
			}
			if strings.HasPrefix(l, "@@") {
				continue
			}
			fm := prismaFieldRe.FindStringSubmatch(l)
			if fm == nil {
				continue
			}
			typ, attrs := fm[2], fm[5]
			if models[typ] {
				if rm := prismaRelRe.FindStringSubmatch(attrs); rm != nil {
					r := rel{t: t, target: typ}
					for _, lm := range prismaListRe.FindAllStringSubmatch(rm[1], -1) {
						if lm[1] == "fields" {
							r.from = splitCols(lm[2])
						} else {
							r.to = splitCols(lm[2])
						}
					}
					rels = append(rels, r)
				}
				continue
			}
			if fm[3] != "" {
				typ += "[]"
			}
			t.Columns = append(t.Columns, Column{Name: fm[1], Type: typ, NotNull: fm[4] == "" && fm[3] == "",
				PK: strings.Contains(attrs, "@id"), Unique: strings.Contains(attrs, "@unique")})
		}
	}
	for _, r := range rels {
		for k, c := range r.from {
			if i := colIndex(r.t, c); i >= 0 {
				to := "id"
				if k < len(r.to) {
					to = r.to[k]
				}
				r.t.Columns[i].Ref = r.target + "." + to
			}
		}
	}
}
