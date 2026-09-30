package i18n

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"html"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

func TestParse(t *testing.T) {
	for in, want := range map[string]Lang{"": EN, "en": EN, "EN-us": EN, "fr": FR, "fr_FR": FR, " Français ": FR, "english": EN} {
		if got, err := Parse(in); err != nil || got != want {
			t.Errorf("Parse(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	_, err := Parse("xx")
	if err == nil || !strings.Contains(err.Error(), "en, fr") {
		t.Errorf("Parse(xx) error = %v, want the list of languages", err)
	}
	if msg := FR.Message(err); !strings.Contains(msg, "non prise en charge") {
		t.Errorf("French message = %q", msg)
	}
}

func TestTranslate(t *testing.T) {
	if EN.T("Help") != "Help" || FR.T("Help") != "Aide" {
		t.Errorf("T: en %q, fr %q", EN.T("Help"), FR.T("Help"))
	}
	if got := FR.T("a text missing from every catalog"); got != "a text missing from every catalog" {
		t.Errorf("missing translation = %q, want the English text", got)
	}
	if got := Lang("xx").T("Help"); got != "Help" {
		t.Errorf("unknown language = %q, want English", got)
	}
	if got := FR.Tf("%s back on", "garage"); got != "garage réactivée" {
		t.Errorf("Tf = %q", got)
	}
	base := errors.New("boom")
	if err := FR.Errorf("invalid configuration: %w", base); !errors.Is(err, base) || !strings.HasPrefix(err.Error(), "config invalide") {
		t.Errorf("Errorf = %v", err)
	}
	if FR.DateTimeShort() != "02/01 15:04" || EN.DateTime() != "Jan 2 15:04:05" {
		t.Error("date layouts")
	}
}

func TestError(t *testing.T) {
	err := NewError("missing camera")
	if err.Error() != "missing camera" || FR.Message(err) != "caméra manquante" || EN.Message(err) != "missing camera" {
		t.Errorf("Error %q, fr %q", err.Error(), FR.Message(err))
	}
	wrapped := fmt.Errorf("context: %w", err)
	if FR.Message(wrapped) != "caméra manquante" {
		t.Errorf("wrapped error = %q", FR.Message(wrapped))
	}
	if plain := errors.New("plain"); FR.Message(plain) != "plain" {
		t.Error("an ordinary error is shown as is")
	}
}

func TestLanguages(t *testing.T) {
	if got := Languages(); len(got) < 2 || got[0] != EN || !slices.Contains(got, FR) {
		t.Errorf("Languages() = %v", got)
	}
	if FR.Name() != "Français" || EN.Name() != "English" || Lang("xx").Name() != "xx" {
		t.Errorf("names: %q %q", FR.Name(), EN.Name())
	}
}

func TestLoadRejectsABrokenCatalog(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("a broken catalog must be refused")
		}
	}()
	mustLoad(fstest.MapFS{"locales/de.json": {Data: []byte("{not json")}})
}

// verbs are the printf verbs of a format, in order (%% excluded).
var verbRe = regexp.MustCompile(`%[-+# 0]*(?:\[\d+\])?\d*(?:\.\d+)?[a-zA-Z%]`)

func verbs(s string) []string {
	var out []string
	for _, v := range verbRe.FindAllString(s, -1) {
		if v != "%%" {
			out = append(out, v)
		}
	}
	return out
}

func TestTranslationsKeepTheFormatVerbs(t *testing.T) {
	for l, c := range Catalogs() {
		if c.Name == "" {
			t.Errorf("%s: the catalog has no name", l)
		}
		for en, tr := range c.Messages {
			if !slices.Equal(verbs(en), verbs(tr)) {
				t.Errorf("%s: %q translated as %q: verbs %v, want %v", l, en, tr, verbs(tr), verbs(en))
			}
		}
	}
}

// TestFrenchCatalogMatchesTheCode checks that every text the code translates has
// a French translation, and that the catalog holds no text the code no longer uses.
func TestFrenchCatalogMatchesTheCode(t *testing.T) {
	keys, literals := map[string]string{}, map[string]bool{}
	scanGo(t, filepath.Join("..", ".."), keys, literals)
	scanWeb(t, filepath.Join("..", "web"), keys, literals)
	if len(keys) < 100 {
		t.Fatalf("only %d texts found in the code: is the scan broken?", len(keys))
	}
	fr := Catalogs()[FR].Messages
	for _, k := range slices.Sorted(mapsKeys(keys)) {
		if _, ok := fr[k]; !ok {
			t.Errorf("%s: %q has no French translation (locales/fr.json)", keys[k], k)
		}
	}
	for _, k := range slices.Sorted(mapsKeys(fr)) {
		if !literals[k] {
			t.Errorf("locales/fr.json: %q is no longer used", k)
		}
	}
}

func mapsKeys[V any](m map[string]V) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// scanGo collects the first argument of translation calls (x.T, x.Tf, x.Errorf
// other than fmt, NewError, and config's add helper) and every string literal.
func scanGo(t *testing.T, root string, keys map[string]string, literals map[string]bool) {
	t.Helper()
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.BasicLit:
				if s, err := strconv.Unquote(n.Value); err == nil && n.Kind == token.STRING {
					literals[s] = true
				}
			case *ast.CallExpr:
				if len(n.Args) == 0 || !translates(n.Fun) {
					return true
				}
				if s, ok := constString(n.Args[0]); ok {
					keys[s] = fset.Position(n.Args[0].Pos()).String()
					literals[s] = true
				}
				if len(n.Args) > 1 {
					if _, ok := constString(n.Args[1]); ok {
						t.Errorf("%s: a constant text as second argument: is this call still bilingual?", fset.Position(n.Pos()))
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// constString evaluates a constant string expression: a literal, or literals joined with +.
func constString(e ast.Expr) (string, bool) {
	switch e := e.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(e.Value)
		return s, err == nil
	case *ast.BinaryExpr:
		a, ok1 := constString(e.X)
		b, ok2 := constString(e.Y)
		return a + b, ok1 && ok2 && e.Op == token.ADD
	case *ast.ParenExpr:
		return constString(e.X)
	}
	return "", false
}

func translates(fun ast.Expr) bool {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name == "NewError" || f.Name == "add"
	case *ast.SelectorExpr:
		if x, ok := f.X.(*ast.Ident); ok && (x.Name == "fmt" || x.Name == "t") {
			return false
		}
		return f.Sel.Name == "T" || f.Sel.Name == "Tf" || f.Sel.Name == "Errorf" || f.Sel.Name == "NewError"
	}
	return false
}

var (
	jsString = regexp.MustCompile(`"((?:[^"\\\n]|\\.)*)"`)
	jsCall   = regexp.MustCompile(`(?:^|[^\w.$])Tf?\(\s*"((?:[^"\\\n]|\\.)*)"`)
	dataT    = regexp.MustCompile(`data-t="([^"]*)"`)
	// T("…", …) or T(`…${x}…`): a call left bilingual, or a key that changes with its values.
	jsBilingual = regexp.MustCompile(`(?:^|[^\w.$])T\(\s*(?:"(?:[^"\\\n]|\\.)*"\s*,|` + "`" + `[^` + "`" + `]*\$\{)`)
)

// scanWeb does the same for the web interface: T("…")/Tf("…") in ui.js, data-t in ui.html.
func scanWeb(t *testing.T, dir string, keys map[string]string, literals map[string]bool) {
	t.Helper()
	js, err := os.ReadFile(filepath.Join(dir, "ui.js"))
	if err != nil {
		t.Fatal(err)
	}
	unquote := func(s string) string {
		u, err := strconv.Unquote(`"` + s + `"`)
		if err != nil {
			t.Fatalf("ui.js: cannot read the string %q: %v", s, err)
		}
		return u
	}
	for _, m := range jsString.FindAllStringSubmatch(string(js), -1) {
		literals[unquote(m[1])] = true
	}
	for _, m := range jsCall.FindAllStringSubmatch(string(js), -1) {
		keys[unquote(m[1])] = "ui.js"
	}
	for _, m := range jsBilingual.FindAllString(string(js), -1) {
		t.Errorf("ui.js: %q: T takes one English text; use Tf for a text with values", m)
	}
	page, err := os.ReadFile(filepath.Join(dir, "ui.html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range dataT.FindAllStringSubmatch(string(page), -1) {
		s := html.UnescapeString(m[1])
		keys[s], literals[s] = "ui.html", true
	}
}
