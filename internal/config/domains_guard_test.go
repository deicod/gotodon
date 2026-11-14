package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// inventedDomain matches string literals that name a placeholder domain, the
// kind code used to fall back on when no domain was configured.
var inventedDomain = regexp.MustCompile(`(^|[^A-Za-z0-9.-])(example\.(com|net|org)|localhost:\d+)($|[^A-Za-z0-9-])`)

// TestNoInventedDomains fails when production code outside this package
// names a placeholder domain or reads Instance.LocalDomain or
// Instance.WebDomain itself instead of FederationDomain or PublicWebDomain.
// Generated query code under internal/db is skipped: its SQL may match old
// stored values, as RepairRegisteredAccountURIs does.
func TestNoInventedDomains(t *testing.T) {
	moduleRoot := filepath.Join("..", "..")
	self, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	generated, err := filepath.Abs(filepath.Join(moduleRoot, "internal", "db"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}

	scanned := 0
	for _, top := range []string{"main.go", "cmd", "internal", "pkg"} {
		err := filepath.WalkDir(filepath.Join(moduleRoot, top), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				abs, err := filepath.Abs(path)
				if err != nil {
					return err
				}
				if abs == self || abs == generated {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			scanned++
			for _, finding := range domainFindings(t, path) {
				t.Errorf("%s: %s", finding.pos, finding.message)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", top, err)
		}
	}
	if scanned < 100 {
		t.Fatalf("scanned %d Go files, want the whole module", scanned)
	}
}

func TestInventedDomainDetection(t *testing.T) {
	for value, want := range map[string]bool{
		"example.com":                        true,
		"https://example.com/users/":         true,
		"https://example.org":                true,
		"example.net":                        true,
		"localhost:3000":                     true,
		"https://localhost:4000/":            true,
		"social.example":                     false,
		"cdn.example.com":                    false,
		"notexample.com":                     false,
		"example.community":                  false,
		"localhost":                          false,
		"notifications@localhost":            false,
		"must be a host name such as x.test": false,
	} {
		if got := inventedDomain.MatchString(value); got != want {
			t.Errorf("match(%q) = %t, want %t", value, got, want)
		}
	}
}

type domainFinding struct {
	pos     token.Position
	message string
}

func domainFindings(t *testing.T, path string) []domainFinding {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var findings []domainFinding
	ast.Inspect(file, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.BasicLit:
			if n.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(n.Value)
			if err != nil {
				value = n.Value
			}
			if inventedDomain.MatchString(value) {
				findings = append(findings, domainFinding{
					pos:     fset.Position(n.Pos()),
					message: "string " + strconv.Quote(value) + " names a placeholder domain; use the configured one",
				})
			}
		case *ast.SelectorExpr:
			if n.Sel.Name != "LocalDomain" && n.Sel.Name != "WebDomain" {
				return true
			}
			if parent, ok := n.X.(*ast.SelectorExpr); ok && parent.Sel.Name == "Instance" {
				findings = append(findings, domainFinding{
					pos:     fset.Position(n.Pos()),
					message: "reads Instance." + n.Sel.Name + " directly; use Config.FederationDomain or Config.PublicWebDomain",
				})
			}
		}
		return true
	})
	return findings
}
