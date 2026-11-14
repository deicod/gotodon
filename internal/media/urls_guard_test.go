package media

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

// handBuiltFileURL matches string literals that spell out where stored files
// or the default images are served. Outside this package such a literal means
// a URL is being built by hand instead of through FileURL, MissingAvatarURL
// or MissingHeaderURL, and would bypass media.base_url.
var handBuiltFileURL = regexp.MustCompile(`/files(/|$)|missing\.png`)

// TestNoHandBuiltFileURLs fails when code outside this package builds a file
// URL itself.
func TestNoHandBuiltFileURLs(t *testing.T) {
	moduleRoot := filepath.Join("..", "..")
	self, err := filepath.Abs(".")
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
				if abs == self {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			scanned++
			for _, finding := range handBuiltFileURLs(t, path) {
				t.Errorf("%s: string %q builds a file URL by hand; use media.FileURL, media.MissingAvatarURL or media.MissingHeaderURL", finding.pos, finding.value)
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

func TestHandBuiltFileURLDetection(t *testing.T) {
	for value, want := range map[string]bool{
		"/files/":                         true,
		"/files":                          true,
		"https://example.com/files/a.png": true,
		"media_attachments/files/1/a.png": true,
		"/avatars/original/missing.png":   true,
		"/headers/original/missing.png":   true,
		"/filesystem":                     false,
		"profiles":                        false,
		`json:"files"`:                    false,
		"/api/v1/media":                   false,
	} {
		if got := handBuiltFileURL.MatchString(value); got != want {
			t.Errorf("match(%q) = %t, want %t", value, got, want)
		}
	}
}

type literalFinding struct {
	pos   token.Position
	value string
}

func handBuiltFileURLs(t *testing.T, path string) []literalFinding {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var findings []literalFinding
	ast.Inspect(file, func(node ast.Node) bool {
		lit, ok := node.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(lit.Value)
		if err != nil {
			value = lit.Value
		}
		if handBuiltFileURL.MatchString(value) {
			findings = append(findings, literalFinding{pos: fset.Position(lit.Pos()), value: value})
		}
		return true
	})
	return findings
}
