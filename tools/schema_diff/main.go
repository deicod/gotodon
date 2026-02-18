package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Schema struct {
	Tables       map[string]*Table
	IndexByName  map[string]string
	SchemaSource string
}

type Table struct {
	Name    string
	Columns map[string]Column
	Indexes map[string]Index
}

type Column struct {
	Name string
	Type string
}

type Index struct {
	Name    string
	Columns []string
	Unique  bool
	Where   string
	Using   string
}

func newSchema(source string) *Schema {
	return &Schema{
		Tables:       map[string]*Table{},
		IndexByName:  map[string]string{},
		SchemaSource: source,
	}
}

func (s *Schema) table(name string) *Table {
	key := normalizeIdent(name)
	if key == "" {
		return nil
	}
	if t, ok := s.Tables[key]; ok {
		return t
	}
	t := &Table{Name: key, Columns: map[string]Column{}, Indexes: map[string]Index{}}
	s.Tables[key] = t
	return t
}

func (s *Schema) addIndex(table string, idx Index) {
	if table == "" || idx.Name == "" {
		return
	}
	t := s.table(table)
	if t == nil {
		return
	}
	idx.Name = normalizeIdent(idx.Name)
	idx.Columns = normalizeIndexColumns(idx.Columns)
	idx.Where = normalizeWhere(idx.Where)
	idx.Using = strings.TrimSpace(strings.ToLower(idx.Using))
	t.Indexes[idx.Name] = idx
	s.IndexByName[idx.Name] = t.Name
}

func (s *Schema) dropIndex(name string) {
	key := normalizeIdent(name)
	if key == "" {
		return
	}
	if table, ok := s.IndexByName[key]; ok {
		if t, exists := s.Tables[table]; exists {
			delete(t.Indexes, key)
		}
		delete(s.IndexByName, key)
	}
}

func main() {
	mastodonSchema := flag.String("mastodon-schema", "mastodon/db/schema.rb", "Path to Mastodon schema.rb")
	migrationsDir := flag.String("migrations-dir", "db/migrations", "Path to Gotodon migrations directory")
	outPath := flag.String("out", "SCHEMA_DIFF.md", "Output file path")
	flag.Parse()

	if err := run(*mastodonSchema, *migrationsDir, *outPath); err != nil {
		fmt.Fprintln(os.Stderr, "schema diff error:", err)
		os.Exit(1)
	}
}

func run(mastodonSchemaPath, migrationsDir, outPath string) error {
	mastodon, err := parseRailsSchema(mastodonSchemaPath)
	if err != nil {
		return err
	}
	gotodon, err := parseSQLMigrations(migrationsDir)
	if err != nil {
		return err
	}

	output := renderDiff(mastodon, gotodon, mastodonSchemaPath, migrationsDir)
	return os.WriteFile(outPath, []byte(output), 0o644)
}

func parseRailsSchema(path string) (*Schema, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	schema := newSchema(path)
	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)

	createTableRe := regexp.MustCompile(`create_table\s+"([^"]+)"`)
	createViewRe := regexp.MustCompile(`create_view\s+"([^"]+)"`)
	columnRe := regexp.MustCompile(`^t\.([a-zA-Z_]+)\s+"([^"]+)"`)
	addIndexRe := regexp.MustCompile(`^add_index\s+"([^"]+)"`)

	currentTable := ""
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if currentTable == "" {
			if matches := createTableRe.FindStringSubmatch(line); len(matches) == 2 {
				currentTable = matches[1]
				schema.table(currentTable)
				continue
			}
			if matches := createViewRe.FindStringSubmatch(line); len(matches) == 2 {
				schema.table(matches[1])
				continue
			}
			if matches := addIndexRe.FindStringSubmatch(line); len(matches) == 2 {
				idx, err := parseRailsIndex(line)
				if err != nil {
					continue
				}
				schema.addIndex(matches[1], idx)
			}
			continue
		}

		if line == "end" {
			currentTable = ""
			continue
		}
		if strings.HasPrefix(line, "t.index") {
			idx, err := parseRailsIndex(line)
			if err != nil {
				continue
			}
			schema.addIndex(currentTable, idx)
			continue
		}
		if matches := columnRe.FindStringSubmatch(line); len(matches) == 3 {
			typeName := matches[1]
			colName := matches[2]
			if strings.Contains(line, "array: true") {
				typeName += "[]"
			}
			t := schema.table(currentTable)
			if t != nil {
				t.Columns[normalizeIdent(colName)] = Column{Name: colName, Type: typeName}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return schema, nil
}

func parseRailsIndex(line string) (Index, error) {
	cols := parseRailsColumns(line)
	if len(cols) == 0 {
		return Index{}, errors.New("no columns")
	}
	name := parseRailsOption(line, "name")
	if name == "" {
		return Index{}, errors.New("missing name")
	}
	unique := strings.Contains(line, "unique: true")
	where := parseRailsOption(line, "where")
	order := parseRailsOrder(line)
	opclass := parseRailsOpclass(line)
	using := parseRailsSymbolOption(line, "using")

	cols = applyIndexOrder(cols, order)
	cols = applyIndexOpclass(cols, opclass)

	return Index{
		Name:    name,
		Columns: cols,
		Unique:  unique,
		Where:   where,
		Using:   using,
	}, nil
}

func parseRailsColumns(line string) []string {
	open := strings.Index(line, "[")
	close := strings.Index(line, "]")
	if open == -1 || close == -1 || close <= open {
		matches := extractQuotedStrings(line)
		if len(matches) == 0 {
			return nil
		}
		start := 0
		if strings.HasPrefix(strings.TrimSpace(line), "add_index") {
			if len(matches) < 2 {
				return nil
			}
			start = 1
		}
		expr := matches[start]
		cols := splitCommaTopLevel(expr)
		if len(cols) == 0 {
			return nil
		}
		return cols
	}
	chunk := line[open : close+1]
	matches := extractQuotedStrings(chunk)
	cols := make([]string, 0, len(matches))
	for _, match := range matches {
		cols = append(cols, match)
	}
	return cols
}

func extractQuotedStrings(line string) []string {
	var out []string
	var buf strings.Builder
	inString := false
	escaped := false
	for _, ch := range line {
		if !inString {
			if ch == '"' {
				inString = true
				buf.Reset()
			}
			continue
		}
		if escaped {
			buf.WriteRune(ch)
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if ch == '"' {
			out = append(out, buf.String())
			inString = false
			continue
		}
		buf.WriteRune(ch)
	}
	return out
}

func parseRailsOption(line, key string) string {
	pattern := fmt.Sprintf(`%s:\s*"([^"]+)"`, regexp.QuoteMeta(key))
	re := regexp.MustCompile(pattern)
	if matches := re.FindStringSubmatch(line); len(matches) == 2 {
		return matches[1]
	}
	return ""
}

func parseRailsOrder(line string) map[string]string {
	order := map[string]string{}
	start := strings.Index(line, "order:")
	if start == -1 {
		return order
	}
	fragment := line[start:]
	open := strings.Index(fragment, "{")
	close := strings.Index(fragment, "}")
	if open == -1 || close == -1 || close <= open {
		return order
	}
	content := fragment[open+1 : close]
	parts := strings.Split(content, ",")
	for _, part := range parts {
		piece := strings.TrimSpace(part)
		if piece == "" {
			continue
		}
		kv := strings.SplitN(piece, ":", 2)
		if len(kv) != 2 {
			continue
		}
		col := strings.TrimSpace(kv[0])
		val := strings.TrimSpace(kv[1])
		col = strings.Trim(col, "\"")
		val = strings.Trim(val, "\"")
		val = strings.TrimPrefix(val, ":")
		val = strings.TrimSpace(strings.ToUpper(val))
		if val == "" {
			continue
		}
		order[col] = val
	}
	return order
}

func parseRailsOpclass(line string) string {
	if !strings.Contains(line, "opclass:") {
		return ""
	}
	if strings.Contains(line, "opclass: :") {
		parts := strings.Split(line, "opclass:")
		if len(parts) < 2 {
			return ""
		}
		tail := strings.TrimSpace(parts[1])
		fields := strings.Fields(tail)
		if len(fields) == 0 {
			return ""
		}
		value := strings.TrimPrefix(fields[0], ":")
		value = strings.TrimSuffix(value, ",")
		return value
	}
	return ""
}

func parseRailsSymbolOption(line, key string) string {
	pattern := fmt.Sprintf(`%s:\s*:(\w+)`, regexp.QuoteMeta(key))
	re := regexp.MustCompile(pattern)
	if matches := re.FindStringSubmatch(line); len(matches) == 2 {
		return matches[1]
	}
	return ""
}

func applyIndexOrder(cols []string, order map[string]string) []string {
	if len(order) == 0 {
		return cols
	}
	updated := make([]string, len(cols))
	for i, col := range cols {
		if ord, ok := order[col]; ok {
			updated[i] = fmt.Sprintf("%s %s", col, ord)
			continue
		}
		updated[i] = col
	}
	return updated
}

func applyIndexOpclass(cols []string, opclass string) []string {
	if opclass == "" || len(cols) == 0 {
		return cols
	}
	updated := make([]string, len(cols))
	for i, col := range cols {
		if i == 0 {
			updated[i] = fmt.Sprintf("%s %s", col, opclass)
		} else {
			updated[i] = col
		}
	}
	return updated
}

func parseSQLMigrations(dir string) (*Schema, error) {
	pattern := filepath.Join(dir, "*.sql")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no migrations found at %s", dir)
	}
	sort.Strings(files)

	schema := newSchema(dir)
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		up := extractGooseUp(string(content))
		stmts := splitSQLStatements(up)
		for _, stmt := range stmts {
			applySQLStatement(schema, stmt)
		}
	}

	return schema, nil
}

func extractGooseUp(content string) string {
	const upMarker = "-- +goose Up"
	const downMarker = "-- +goose Down"
	if strings.Contains(content, upMarker) {
		parts := strings.Split(content, upMarker)
		if len(parts) > 1 {
			content = parts[1]
			if strings.Contains(content, downMarker) {
				content = strings.Split(content, downMarker)[0]
			}
		}
	}
	return content
}

func applySQLStatement(schema *Schema, stmt string) {
	trimmed := strings.TrimSpace(stmt)
	if trimmed == "" {
		return
	}
	upper := strings.ToUpper(trimmed)

	switch {
	case strings.HasPrefix(upper, "CREATE TABLE"):
		applyCreateTable(schema, trimmed)
	case strings.HasPrefix(upper, "CREATE MATERIALIZED VIEW"):
		applyCreateView(schema, trimmed, "CREATE MATERIALIZED VIEW")
	case strings.HasPrefix(upper, "CREATE VIEW"):
		applyCreateView(schema, trimmed, "CREATE VIEW")
	case strings.HasPrefix(upper, "ALTER TABLE"):
		applyAlterTable(schema, trimmed)
	case strings.HasPrefix(upper, "DROP TABLE"):
		applyDropTable(schema, trimmed)
	case strings.HasPrefix(upper, "DROP MATERIALIZED VIEW"):
		applyDropView(schema, trimmed, "DROP MATERIALIZED VIEW")
	case strings.HasPrefix(upper, "DROP VIEW"):
		applyDropView(schema, trimmed, "DROP VIEW")
	case strings.HasPrefix(upper, "CREATE INDEX") || strings.HasPrefix(upper, "CREATE UNIQUE INDEX"):
		applyCreateIndex(schema, trimmed)
	case strings.HasPrefix(upper, "DROP INDEX"):
		applyDropIndex(schema, trimmed)
	}
}

func applyCreateTable(schema *Schema, stmt string) {
	upper := strings.ToUpper(stmt)
	start := strings.Index(upper, "CREATE TABLE")
	if start == -1 {
		return
	}
	rest := strings.TrimSpace(stmt[start+len("CREATE TABLE"):])
	if strings.HasPrefix(strings.ToUpper(rest), "IF NOT EXISTS") {
		rest = strings.TrimSpace(rest[len("IF NOT EXISTS"):])
	}
	name, after := readIdent(rest)
	if name == "" {
		return
	}
	openIdx := strings.Index(after, "(")
	if openIdx == -1 {
		return
	}
	content, ok := extractParenContent(after[openIdx:])
	if !ok {
		return
	}
	segments := splitCommaTopLevel(content)
	for _, segment := range segments {
		colName, colType := parseSQLColumnDefinition(segment)
		if colName == "" || colType == "" {
			continue
		}
		table := schema.table(name)
		if table != nil {
			table.Columns[normalizeIdent(colName)] = Column{Name: colName, Type: colType}
		}
	}
}

func applyCreateView(schema *Schema, stmt, prefix string) {
	rest := strings.TrimSpace(stmt[len(prefix):])
	if strings.HasPrefix(strings.ToUpper(rest), "IF NOT EXISTS") {
		rest = strings.TrimSpace(rest[len("IF NOT EXISTS"):])
	}
	name, _ := readIdent(rest)
	if name == "" {
		return
	}
	schema.table(name)
}

func applyAlterTable(schema *Schema, stmt string) {
	upper := strings.ToUpper(stmt)
	rest := strings.TrimSpace(stmt[len("ALTER TABLE"):])
	if strings.HasPrefix(upper[len("ALTER TABLE"):], " IF EXISTS") {
		rest = strings.TrimSpace(rest[len("IF EXISTS"):])
	}
	name, after := readIdent(rest)
	if name == "" {
		return
	}
	if after == "" {
		return
	}
	actions := splitCommaTopLevel(after)
	for _, action := range actions {
		applyAlterAction(schema, name, action)
	}
}

func applyAlterAction(schema *Schema, tableName, action string) {
	trimmed := strings.TrimSpace(action)
	if trimmed == "" {
		return
	}
	upper := strings.ToUpper(trimmed)

	switch {
	case strings.HasPrefix(upper, "ADD COLUMN"):
		columnDef := strings.TrimSpace(trimmed[len("ADD COLUMN"):])
		if strings.HasPrefix(strings.ToUpper(columnDef), "IF NOT EXISTS") {
			columnDef = strings.TrimSpace(columnDef[len("IF NOT EXISTS"):])
		}
		colName, colType := parseSQLColumnDefinition(columnDef)
		if colName == "" || colType == "" {
			return
		}
		table := schema.table(tableName)
		if table != nil {
			table.Columns[normalizeIdent(colName)] = Column{Name: colName, Type: colType}
		}
	case strings.HasPrefix(upper, "DROP COLUMN"):
		columnName := strings.TrimSpace(trimmed[len("DROP COLUMN"):])
		if strings.HasPrefix(strings.ToUpper(columnName), "IF EXISTS") {
			columnName = strings.TrimSpace(columnName[len("IF EXISTS"):])
		}
		columnName = normalizeIdent(columnName)
		if columnName == "" {
			return
		}
		if table := schema.table(tableName); table != nil {
			delete(table.Columns, columnName)
		}
	case strings.HasPrefix(upper, "RENAME COLUMN"):
		payload := strings.TrimSpace(trimmed[len("RENAME COLUMN"):])
		parts := strings.Fields(payload)
		if len(parts) >= 3 && strings.ToUpper(parts[1]) == "TO" {
			oldName := normalizeIdent(parts[0])
			newName := normalizeIdent(parts[2])
			if table := schema.table(tableName); table != nil {
				if col, ok := table.Columns[oldName]; ok {
					col.Name = newName
					delete(table.Columns, oldName)
					table.Columns[newName] = col
				}
			}
		}
	case strings.HasPrefix(upper, "ALTER COLUMN"):
		payload := strings.TrimSpace(trimmed[len("ALTER COLUMN"):])
		applyAlterColumnType(schema, tableName, payload)
	}
}

func applyAlterColumnType(schema *Schema, tableName, payload string) {
	upper := strings.ToUpper(payload)
	fields := strings.Fields(payload)
	if len(fields) == 0 {
		return
	}
	colName := normalizeIdent(fields[0])
	if colName == "" {
		return
	}
	idx := strings.Index(upper, " TYPE ")
	if idx == -1 {
		idx = strings.Index(upper, " SET DATA TYPE ")
		if idx == -1 {
			return
		}
		idx += len(" SET DATA TYPE ")
	} else {
		idx += len(" TYPE ")
	}
	colType := strings.TrimSpace(payload[idx:])
	if colType == "" {
		return
	}
	if usingIdx := strings.Index(strings.ToUpper(colType), " USING "); usingIdx != -1 {
		colType = strings.TrimSpace(colType[:usingIdx])
	}
	table := schema.table(tableName)
	if table == nil {
		return
	}
	if col, ok := table.Columns[colName]; ok {
		col.Type = colType
		table.Columns[colName] = col
	}
}

func applyDropTable(schema *Schema, stmt string) {
	upper := strings.ToUpper(stmt)
	rest := strings.TrimSpace(stmt[len("DROP TABLE"):])
	if strings.HasPrefix(upper[len("DROP TABLE"):], " IF EXISTS") {
		rest = strings.TrimSpace(rest[len("IF EXISTS"):])
	}
	name, _ := readIdent(rest)
	if name == "" {
		return
	}
	key := normalizeIdent(name)
	delete(schema.Tables, key)
}

func applyDropView(schema *Schema, stmt, prefix string) {
	rest := strings.TrimSpace(stmt[len(prefix):])
	if strings.HasPrefix(strings.ToUpper(rest), "IF EXISTS") {
		rest = strings.TrimSpace(rest[len("IF EXISTS"):])
	}
	name, _ := readIdent(rest)
	if name == "" {
		return
	}
	key := normalizeIdent(name)
	delete(schema.Tables, key)
}

func applyCreateIndex(schema *Schema, stmt string) {
	idx, table := parseSQLIndex(stmt)
	if table == "" || idx.Name == "" {
		return
	}
	schema.addIndex(table, idx)
}

func applyDropIndex(schema *Schema, stmt string) {
	upper := strings.ToUpper(stmt)
	rest := strings.TrimSpace(stmt[len("DROP INDEX"):])
	if strings.HasPrefix(upper[len("DROP INDEX"):], " IF EXISTS") {
		rest = strings.TrimSpace(rest[len("IF EXISTS"):])
	}
	name, _ := readIdent(rest)
	if name == "" {
		return
	}
	schema.dropIndex(name)
}

func parseSQLIndex(stmt string) (Index, string) {
	normalized := strings.Join(strings.Fields(stmt), " ")
	upper := strings.ToUpper(normalized)
	unique := strings.HasPrefix(upper, "CREATE UNIQUE INDEX")
	start := strings.Index(upper, "INDEX")
	if start == -1 {
		return Index{}, ""
	}
	rest := strings.TrimSpace(normalized[start+len("INDEX"):])
	for {
		upperRest := strings.ToUpper(rest)
		if strings.HasPrefix(upperRest, "CONCURRENTLY") {
			rest = strings.TrimSpace(rest[len("CONCURRENTLY"):])
			continue
		}
		if strings.HasPrefix(upperRest, "IF NOT EXISTS") {
			rest = strings.TrimSpace(rest[len("IF NOT EXISTS"):])
			continue
		}
		break
	}
	name, after := readIdent(rest)
	if name == "" {
		return Index{}, ""
	}
	after = strings.TrimSpace(after)
	afterUpper := strings.ToUpper(after)
	if strings.HasPrefix(afterUpper, "ON ") {
		after = strings.TrimSpace(after[len("ON "):])
	} else {
		onIdx := strings.Index(afterUpper, " ON ")
		if onIdx == -1 {
			return Index{}, ""
		}
		after = strings.TrimSpace(after[onIdx+len(" ON "):])
	}
	table, after := readIdent(after)
	if table == "" {
		return Index{}, ""
	}
	using := ""
	afterUpper = strings.ToUpper(after)
	if strings.HasPrefix(strings.TrimSpace(afterUpper), "USING ") {
		parts := strings.Fields(after)
		if len(parts) >= 2 {
			using = parts[1]
			after = strings.TrimSpace(strings.TrimPrefix(after, parts[0]))
			after = strings.TrimSpace(strings.TrimPrefix(after, parts[1]))
		}
	}
	openIdx := strings.Index(after, "(")
	if openIdx == -1 {
		return Index{}, ""
	}
	columnsChunk, ok := extractParenContent(after[openIdx:])
	if !ok {
		return Index{}, ""
	}
	cols := splitCommaTopLevel(columnsChunk)
	where := ""
	afterCols := after[openIdx+len(columnsChunk)+2:]
	if idx := strings.Index(strings.ToUpper(afterCols), "WHERE"); idx != -1 {
		where = strings.TrimSpace(afterCols[idx+len("WHERE"):])
	}

	return Index{
		Name:    name,
		Columns: cols,
		Unique:  unique,
		Where:   where,
		Using:   using,
	}, table
}

func parseSQLColumnDefinition(segment string) (string, string) {
	trimmed := strings.TrimSpace(segment)
	if trimmed == "" {
		return "", ""
	}
	upper := strings.ToUpper(trimmed)
	if strings.HasPrefix(upper, "CONSTRAINT") || strings.HasPrefix(upper, "PRIMARY KEY") || strings.HasPrefix(upper, "UNIQUE") || strings.HasPrefix(upper, "FOREIGN KEY") {
		return "", ""
	}
	name, rest := readIdent(trimmed)
	if name == "" {
		return "", ""
	}
	if rest == "" {
		return "", ""
	}
	typeName := extractColumnType(rest)
	return name, typeName
}

func extractColumnType(def string) string {
	constraintTokens := map[string]struct{}{
		"not": {}, "null": {}, "default": {}, "primary": {}, "references": {}, "constraint": {},
		"unique": {}, "check": {}, "collate": {}, "generated": {}, "identity": {},
	}
	fields := strings.Fields(def)
	if len(fields) == 0 {
		return ""
	}
	var tokens []string
	for _, field := range fields {
		clean := strings.TrimSpace(field)
		if clean == "" {
			continue
		}
		key := strings.Trim(strings.ToLower(clean), ",")
		if _, ok := constraintTokens[key]; ok {
			break
		}
		tokens = append(tokens, clean)
	}
	return strings.Join(tokens, " ")
}

func readIdent(input string) (string, string) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", ""
	}
	if strings.HasPrefix(trimmed, "\"") {
		end := strings.Index(trimmed[1:], "\"")
		if end == -1 {
			return "", ""
		}
		name := trimmed[1 : end+1]
		rest := strings.TrimSpace(trimmed[end+2:])
		return name, rest
	}
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return "", ""
	}
	name := fields[0]
	rest := strings.TrimSpace(strings.TrimPrefix(trimmed, name))
	return name, rest
}

func splitSQLStatements(input string) []string {
	var stmts []string
	var buf strings.Builder
	input = strings.ReplaceAll(input, "\r\n", "\n")
	input = strings.ReplaceAll(input, "\r", "\n")

	var inSingle, inDouble, inLineComment, inBlockComment bool
	var dollarTag string
	for i := 0; i < len(input); i++ {
		ch := input[i]
		next := byte(0)
		if i+1 < len(input) {
			next = input[i+1]
		}

		if inLineComment {
			if ch == '\n' {
				inLineComment = false
				buf.WriteByte(ch)
			}
			continue
		}
		if inBlockComment {
			if ch == '*' && next == '/' {
				inBlockComment = false
				i++
			}
			continue
		}
		if dollarTag != "" {
			if strings.HasPrefix(input[i:], dollarTag) {
				buf.WriteString(dollarTag)
				i += len(dollarTag) - 1
				dollarTag = ""
				continue
			}
			buf.WriteByte(ch)
			continue
		}
		if inSingle {
			buf.WriteByte(ch)
			if ch == '\'' {
				if next == '\'' {
					buf.WriteByte(next)
					i++
					continue
				}
				inSingle = false
			}
			continue
		}
		if inDouble {
			buf.WriteByte(ch)
			if ch == '"' {
				if next == '"' {
					buf.WriteByte(next)
					i++
					continue
				}
				inDouble = false
			}
			continue
		}

		if ch == '-' && next == '-' {
			inLineComment = true
			i++
			continue
		}
		if ch == '/' && next == '*' {
			inBlockComment = true
			i++
			continue
		}
		if ch == '\'' {
			inSingle = true
			buf.WriteByte(ch)
			continue
		}
		if ch == '"' {
			inDouble = true
			buf.WriteByte(ch)
			continue
		}
		if ch == '$' {
			tag := readDollarTag(input[i:])
			if tag != "" {
				dollarTag = tag
				buf.WriteString(tag)
				i += len(tag) - 1
				continue
			}
		}
		if ch == ';' {
			stmt := strings.TrimSpace(buf.String())
			if stmt != "" {
				stmts = append(stmts, stmt)
			}
			buf.Reset()
			continue
		}
		buf.WriteByte(ch)
	}
	last := strings.TrimSpace(buf.String())
	if last != "" {
		stmts = append(stmts, last)
	}
	return stmts
}

func readDollarTag(input string) string {
	if !strings.HasPrefix(input, "$") {
		return ""
	}
	for i := 1; i < len(input); i++ {
		if input[i] == '$' {
			return input[:i+1]
		}
		if input[i] == '\n' || input[i] == '\r' {
			return ""
		}
	}
	return ""
}

func splitCommaTopLevel(input string) []string {
	var parts []string
	var buf strings.Builder
	depth := 0
	for i := 0; i < len(input); i++ {
		ch := input[i]
		if ch == '(' {
			depth++
		} else if ch == ')' {
			if depth > 0 {
				depth--
			}
		}
		if ch == ',' && depth == 0 {
			segment := strings.TrimSpace(buf.String())
			if segment != "" {
				parts = append(parts, segment)
			}
			buf.Reset()
			continue
		}
		buf.WriteByte(ch)
	}
	segment := strings.TrimSpace(buf.String())
	if segment != "" {
		parts = append(parts, segment)
	}
	return parts
}

func extractParenContent(input string) (string, bool) {
	if !strings.HasPrefix(strings.TrimSpace(input), "(") {
		return "", false
	}
	depth := 0
	for i, ch := range input {
		if ch == '(' {
			depth++
			if depth == 1 {
				continue
			}
		}
		if ch == ')' {
			depth--
			if depth == 0 {
				return input[1:i], true
			}
		}
	}
	return "", false
}

func renderDiff(mastodon, gotodon *Schema, mastodonPath, migrationsDir string) string {
	mastodonTables := sortedKeys(mastodon.Tables)
	gotodonTables := sortedKeys(gotodon.Tables)

	missingTables := diffKeys(mastodon.Tables, gotodon.Tables)
	extraTables := diffKeys(gotodon.Tables, mastodon.Tables)

	var builder strings.Builder
	builder.WriteString("# Schema Diff: Mastodon 4.5.3 vs Gotodon\n\n")
	builder.WriteString(fmt.Sprintf("Generated: %s\n", time.Now().Format("2006-01-02")))
	builder.WriteString(fmt.Sprintf("Source: `%s` vs `%s/*.sql`\n", mastodonPath, migrationsDir))
	builder.WriteString("Generated by: `go run ./tools/schema_diff`\n\n")

	builder.WriteString("## Summary\n")
	builder.WriteString(fmt.Sprintf("- Mastodon tables: %d\n", len(mastodonTables)))
	builder.WriteString(fmt.Sprintf("- Gotodon tables: %d\n", len(gotodonTables)))
	builder.WriteString(fmt.Sprintf("- Missing in Gotodon: %d\n", len(missingTables)))
	builder.WriteString(fmt.Sprintf("- Extra in Gotodon: %d\n\n", len(extraTables)))

	builder.WriteString("## Missing Tables in Gotodon\n")
	if len(missingTables) == 0 {
		builder.WriteString("\n")
	} else {
		for _, table := range missingTables {
			builder.WriteString(fmt.Sprintf("- %s\n", table))
		}
		builder.WriteString("\n")
	}

	builder.WriteString("## Extra Tables in Gotodon (not in Mastodon schema)\n")
	if len(extraTables) == 0 {
		builder.WriteString("\n")
	} else {
		for _, table := range extraTables {
			builder.WriteString(fmt.Sprintf("- %s\n", table))
		}
		builder.WriteString("\n")
	}

	builder.WriteString("## Column Diffs (Shared Tables)\n")
	for _, table := range mastodonTables {
		mt := mastodon.Tables[table]
		gt, ok := gotodon.Tables[table]
		if !ok {
			continue
		}
		colDiffs := columnDiffs(mt, gt)
		if len(colDiffs) == 0 {
			continue
		}
		builder.WriteString(fmt.Sprintf("### %s\n", table))
		builder.WriteString("- Potential type mismatches (normalized):\n")
		for _, diff := range colDiffs {
			builder.WriteString(fmt.Sprintf("  - %s: rails=%s | go=%s\n", diff.Name, diff.RailsType, diff.GoType))
		}
		builder.WriteString("\n")
	}

	builder.WriteString("## Index Diffs (Shared Tables)\n")
	for _, table := range mastodonTables {
		mt := mastodon.Tables[table]
		gt, ok := gotodon.Tables[table]
		if !ok {
			continue
		}
		missing, extra := indexDiffs(mt, gt)
		if len(missing) == 0 && len(extra) == 0 {
			continue
		}
		builder.WriteString(fmt.Sprintf("### %s\n", table))
		if len(missing) > 0 {
			builder.WriteString("- Missing indexes in Gotodon:\n")
			for _, idx := range missing {
				builder.WriteString(fmt.Sprintf("  - %s, name: \"%s\", unique: %t\n", idx.Signature, idx.Name, idx.Unique))
			}
		}
		if len(extra) > 0 {
			builder.WriteString("- Extra indexes in Gotodon:\n")
			for _, idx := range extra {
				builder.WriteString(fmt.Sprintf("  - %s\n", idx.Signature))
			}
		}
		builder.WriteString("\n")
	}

	return builder.String()
}

type columnDiff struct {
	Name      string
	RailsType string
	GoType    string
}

func columnDiffs(railsTable, goTable *Table) []columnDiff {
	var diffs []columnDiff
	for name, col := range railsTable.Columns {
		other, ok := goTable.Columns[name]
		if !ok {
			continue
		}
		rType := normalizeType(col.Type)
		gType := normalizeType(other.Type)
		if rType != gType {
			diffs = append(diffs, columnDiff{Name: name, RailsType: rType, GoType: gType})
		}
	}
	sort.Slice(diffs, func(i, j int) bool { return diffs[i].Name < diffs[j].Name })
	return diffs
}

type indexDiff struct {
	Name      string
	Signature string
	Unique    bool
}

func indexDiffs(railsTable, goTable *Table) ([]indexDiff, []indexDiff) {
	railsIdx := indexSignatureMap(railsTable)
	goIdx := indexSignatureMap(goTable)
	var missing []indexDiff
	var extra []indexDiff
	for sig, idx := range railsIdx {
		if _, ok := goIdx[sig]; !ok {
			missing = append(missing, indexDiff{Name: idx.Name, Signature: sig, Unique: idx.Unique})
		}
	}
	for sig, idx := range goIdx {
		if _, ok := railsIdx[sig]; !ok {
			extra = append(extra, indexDiff{Name: idx.Name, Signature: sig, Unique: idx.Unique})
		}
	}
	sort.Slice(missing, func(i, j int) bool { return missing[i].Signature < missing[j].Signature })
	sort.Slice(extra, func(i, j int) bool { return extra[i].Signature < extra[j].Signature })
	return missing, extra
}

func indexSignatureMap(table *Table) map[string]Index {
	out := map[string]Index{}
	for _, idx := range table.Indexes {
		sig := indexSignature(idx)
		out[sig] = idx
	}
	return out
}

func indexSignature(idx Index) string {
	cols := normalizeIndexColumns(idx.Columns)
	where := normalizeWhere(idx.Where)
	opts := ""
	if idx.Using != "" {
		opts = "using " + idx.Using
	}
	if where != "" {
		if opts != "" {
			opts += " "
		}
		opts += "where " + where
	}
	return fmt.Sprintf("cols=%s|unique=%t|opts=%s", strings.Join(cols, ","), idx.Unique, opts)
}

func normalizeIndexColumns(columns []string) []string {
	out := make([]string, 0, len(columns))
	for _, col := range columns {
		clean := strings.TrimSpace(col)
		clean = strings.TrimSuffix(clean, ";")
		clean = strings.ReplaceAll(clean, "\n", " ")
		clean = strings.Join(strings.Fields(clean), " ")
		clean = strings.Trim(clean, " ")
		clean = strings.ToLower(clean)
		clean = strings.ReplaceAll(clean, "( ", "(")
		clean = strings.ReplaceAll(clean, " )", ")")
		clean = stripOuterParens(clean)
		out = append(out, clean)
	}
	return out
}

func stripOuterParens(expr string) string {
	expr = strings.TrimSpace(expr)
	for {
		if len(expr) < 2 || expr[0] != '(' || expr[len(expr)-1] != ')' {
			return expr
		}
		depth := 0
		valid := true
		for i, ch := range expr {
			if ch == '(' {
				depth++
			}
			if ch == ')' {
				depth--
				if depth == 0 && i < len(expr)-1 {
					valid = false
					break
				}
			}
		}
		if !valid || depth != 0 {
			return expr
		}
		expr = strings.TrimSpace(expr[1 : len(expr)-1])
	}
}

func normalizeWhere(where string) string {
	clean := strings.TrimSpace(where)
	if clean == "" {
		return ""
	}
	clean = strings.TrimSuffix(clean, ";")
	clean = strings.TrimSpace(clean)
	if strings.HasPrefix(clean, "(") && strings.HasSuffix(clean, ")") {
		clean = strings.TrimPrefix(clean, "(")
		clean = strings.TrimSuffix(clean, ")")
	}
	clean = strings.ToLower(clean)
	clean = strings.Join(strings.Fields(clean), " ")
	return clean
}

func normalizeIdent(input string) string {
	name := strings.TrimSpace(input)
	name = strings.TrimSuffix(name, ",")
	name = strings.Trim(name, "\"")
	if strings.Contains(name, ".") {
		parts := strings.Split(name, ".")
		name = parts[len(parts)-1]
	}
	return strings.ToLower(name)
}

func normalizeType(raw string) string {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return ""
	}
	array := false
	if strings.HasSuffix(clean, "[]") {
		array = true
		clean = strings.TrimSuffix(clean, "[]")
	}
	clean = strings.ToLower(clean)
	clean = strings.ReplaceAll(clean, "character varying", "varchar")
	clean = strings.ReplaceAll(clean, "timestamp with time zone", "timestamptz")
	clean = strings.ReplaceAll(clean, "timestamp without time zone", "timestamp")
	clean = strings.ReplaceAll(clean, "double precision", "double")
	clean = strings.ReplaceAll(clean, "jsonb", "jsonb")
	clean = strings.ReplaceAll(clean, "json", "json")
	clean = strings.ReplaceAll(clean, "bigint", "bigint")
	clean = strings.ReplaceAll(clean, "bigserial", "bigserial")
	clean = strings.ReplaceAll(clean, "integer", "integer")
	clean = strings.ReplaceAll(clean, "boolean", "boolean")
	clean = strings.ReplaceAll(clean, "text", "text")
	clean = strings.ReplaceAll(clean, "citext", "citext")
	clean = strings.ReplaceAll(clean, "uuid", "uuid")
	clean = strings.ReplaceAll(clean, "bytea", "bytea")
	clean = strings.ReplaceAll(clean, "inet", "inet")
	clean = strings.ReplaceAll(clean, "cidr", "cidr")
	clean = strings.ReplaceAll(clean, "tsvector", "tsvector")
	if clean == "string" {
		clean = "varchar"
	}
	if clean == "datetime" {
		clean = "timestamptz"
	}
	if clean == "binary" {
		clean = "bytea"
	}
	if strings.HasPrefix(clean, "varchar") {
		clean = "varchar"
	}
	if strings.HasPrefix(clean, "numeric") || strings.HasPrefix(clean, "decimal") {
		clean = "numeric"
	}
	if strings.HasPrefix(clean, "timestamp") {
		clean = "timestamp"
	}
	if strings.Contains(clean, "(") {
		clean = strings.Split(clean, "(")[0]
	}
	clean = strings.TrimSpace(clean)
	if array {
		clean += "[]"
	}
	return clean
}

func sortedKeys(m map[string]*Table) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func diffKeys(a, b map[string]*Table) []string {
	var diff []string
	for k := range a {
		if _, ok := b[k]; !ok {
			diff = append(diff, k)
		}
	}
	sort.Strings(diff)
	return diff
}
