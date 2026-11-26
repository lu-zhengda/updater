package main

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const baselineHeader = `# Disallowed Go comment lines per file. Each count must match the tree.
# A pull request may lower a count. Raising one, or leaving a count above the tree, fails CI.
# A file omitted here has a count of zero. A new file must stay at zero.
# Allowed: a license header before package, a doc comment on an exported
# identifier, a line whose text starts with "platform:" (a constraint forced
# by an outside platform or protocol), a Go toolchain directive, or a line
# that links a GitHub issue or pull URL or a Linear issue URL.
`

var issueURL = regexp.MustCompile(`https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/(issues|pull)/[0-9]+|https://linear\.app/[A-Za-z0-9_.-]+/issue/[A-Za-z]+-[0-9]+`)

var cDirectives = []string{
	"#cgo",
	"#include",
	"#import",
	"#define",
	"#undef",
	"#if",
	"#ifdef",
	"#ifndef",
	"#else",
	"#elif",
	"#endif",
	"#pragma",
}

// Scan counts disallowed comment lines in Go files under root.
// Keys are slash-separated paths relative to root. Files with a zero count are omitted.
func Scan(root string) (map[string]int, error) {
	counts := map[string]int{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == ".." || strings.HasPrefix(rel, "../") {
			return fmt.Errorf("comment path escapes the root: %s", rel)
		}
		n, err := countFile(path)
		if err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		if n > 0 {
			counts[rel] = n
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return counts, nil
}

func countFile(path string) (int, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return 0, err
	}
	allowed := allowedComments(file)
	n := 0
	for _, group := range file.Comments {
		for _, comment := range group.List {
			_, whole := allowed[comment.Pos()]
			for _, line := range strings.Split(comment.Text, "\n") {
				line = strings.TrimRight(line, "\r")
				if strings.TrimSpace(line) == "" {
					continue
				}
				body := commentBody(line)
				if body == "" || whole || lineAllowed(body) {
					continue
				}
				n++
			}
		}
	}
	return n, nil
}

func allowedComments(file *ast.File) map[token.Pos]struct{} {
	allowed := map[token.Pos]struct{}{}
	allow := func(group *ast.CommentGroup) {
		if group == nil {
			return
		}
		for _, comment := range group.List {
			allowed[comment.Pos()] = struct{}{}
		}
	}
	for _, group := range file.Comments {
		if group.Pos() < file.Package && isLicenseHeader(group.Text()) {
			allow(group)
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncDecl:
			if n.Name.IsExported() {
				allow(n.Doc)
			}
		case *ast.GenDecl:
			if declExports(n) {
				allow(n.Doc)
			}
		case *ast.TypeSpec:
			if n.Name.IsExported() {
				allow(n.Doc)
				allow(n.Comment)
			}
		case *ast.ValueSpec:
			if specExports(n) {
				allow(n.Doc)
				allow(n.Comment)
			}
		case *ast.Field:
			if fieldExports(n) {
				allow(n.Doc)
				allow(n.Comment)
			}
		}
		return true
	})
	return allowed
}

func declExports(decl *ast.GenDecl) bool {
	for _, spec := range decl.Specs {
		switch spec := spec.(type) {
		case *ast.TypeSpec:
			if spec.Name.IsExported() {
				return true
			}
		case *ast.ValueSpec:
			if specExports(spec) {
				return true
			}
		}
	}
	return false
}

func specExports(spec *ast.ValueSpec) bool {
	for _, name := range spec.Names {
		if name.IsExported() {
			return true
		}
	}
	return false
}

func fieldExports(field *ast.Field) bool {
	if len(field.Names) == 0 {
		return exportedExpr(field.Type)
	}
	for _, name := range field.Names {
		if name.IsExported() {
			return true
		}
	}
	return false
}

func exportedExpr(expr ast.Expr) bool {
	switch expr := expr.(type) {
	case *ast.Ident:
		return expr.IsExported()
	case *ast.SelectorExpr:
		return expr.Sel.IsExported()
	case *ast.StarExpr:
		return exportedExpr(expr.X)
	case *ast.IndexExpr:
		return exportedExpr(expr.X)
	case *ast.IndexListExpr:
		return exportedExpr(expr.X)
	default:
		return false
	}
}

func isLicenseHeader(text string) bool {
	lower := strings.ToLower(text)
	if strings.Contains(lower, "copyright") || strings.Contains(lower, "spdx-license-identifier") {
		return true
	}
	for _, line := range strings.Split(lower, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "mit license"),
			strings.HasPrefix(line, "licensed under"),
			strings.HasPrefix(line, "all rights reserved"),
			strings.HasPrefix(line, "apache license"),
			strings.HasPrefix(line, "bsd license"),
			line == "license":
			return true
		}
	}
	return false
}

func commentBody(line string) string {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "//") {
		return strings.TrimSpace(strings.TrimPrefix(trimmed, "//"))
	}
	trimmed = strings.TrimPrefix(trimmed, "/*")
	trimmed = strings.TrimSuffix(trimmed, "*/")
	trimmed = strings.TrimSpace(trimmed)
	if trimmed == "*" || strings.HasPrefix(trimmed, "* ") {
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "*"))
	}
	return trimmed
}

func lineAllowed(body string) bool {
	if strings.HasPrefix(body, "platform:") {
		return true
	}
	if isDirective(body) {
		return true
	}
	return issueURL.MatchString(body)
}

func isDirective(body string) bool {
	switch {
	case strings.HasPrefix(body, "go:"),
		strings.HasPrefix(body, "+build"),
		strings.HasPrefix(body, "export "),
		strings.HasPrefix(body, "line "):
		return true
	}
	for _, prefix := range cDirectives {
		if strings.HasPrefix(body, prefix) {
			return true
		}
	}
	return false
}

// Check fails when the scan does not match the baseline, or when a count is
// higher than the previous baseline at ratchetPath. An empty ratchetPath
// skips that comparison.
func Check(root, baselinePath, ratchetPath string) (int, error) {
	actual, err := Scan(root)
	if err != nil {
		return 0, err
	}
	baseline, err := readBaseline(baselinePath)
	if err != nil {
		return 0, err
	}
	var lines []string
	lines = append(lines, mismatchLines("baseline", actual, baseline)...)
	if ratchetPath != "" {
		previous, err := readBaseline(ratchetPath)
		if err != nil {
			return 0, err
		}
		lines = append(lines, ratchetLines(actual, previous)...)
	}
	if len(lines) > 0 {
		return sumCounts(actual), fmt.Errorf("%s", strings.Join(lines, "\n"))
	}
	return sumCounts(actual), nil
}

func mismatchLines(label string, actual, recorded map[string]int) []string {
	keys := unionKeys(actual, recorded)
	var lines []string
	for _, file := range keys {
		if actual[file] == recorded[file] {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s: %s has %d, file has %d", label, file, recorded[file], actual[file]))
	}
	return lines
}

func ratchetLines(actual, previous map[string]int) []string {
	keys := unionKeys(actual, previous)
	var lines []string
	for _, file := range keys {
		if actual[file] > previous[file] {
			lines = append(lines, fmt.Sprintf("ratchet: %s rose from %d to %d", file, previous[file], actual[file]))
		}
	}
	return lines
}

func unionKeys(left, right map[string]int) []string {
	seen := map[string]struct{}{}
	for file := range left {
		seen[file] = struct{}{}
	}
	for file := range right {
		seen[file] = struct{}{}
	}
	keys := make([]string, 0, len(seen))
	for file := range seen {
		keys = append(keys, file)
	}
	sort.Strings(keys)
	return keys
}

func sumCounts(counts map[string]int) int {
	total := 0
	for _, n := range counts {
		total += n
	}
	return total
}

// WriteBaseline replaces path with the scanned counts.
func WriteBaseline(root, path string) (int, error) {
	counts, err := Scan(root)
	if err != nil {
		return 0, err
	}
	if err := writeCounts(path, counts); err != nil {
		return 0, err
	}
	return sumCounts(counts), nil
}

func writeCounts(path string, counts map[string]int) error {
	keys := make([]string, 0, len(counts))
	for file, n := range counts {
		if n <= 0 {
			continue
		}
		keys = append(keys, file)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(baselineHeader)
	fmt.Fprintf(&b, "# total: %d\n", sumCounts(counts))
	for _, file := range keys {
		fmt.Fprintf(&b, "%s %d\n", file, counts[file])
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func readBaseline(path string) (map[string]int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	counts := map[string]int{}
	total := -1
	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "# total:") {
			continue
		}
		if strings.HasPrefix(line, "# total:") {
			if total >= 0 {
				return nil, fmt.Errorf("%s:%d: repeated total", path, lineNo)
			}
			n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "# total:")))
			if err != nil || n < 0 {
				return nil, fmt.Errorf("%s:%d: bad total", path, lineNo)
			}
			total = n
			continue
		}
		file, count, err := parseCountLine(line)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, lineNo, err)
		}
		if _, ok := counts[file]; ok {
			return nil, fmt.Errorf("%s:%d: duplicate %s", path, lineNo, file)
		}
		counts[file] = count
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if total < 0 {
		return nil, fmt.Errorf("%s: missing # total", path)
	}
	if total != sumCounts(counts) {
		return nil, fmt.Errorf("%s: total %d, sum %d", path, total, sumCounts(counts))
	}
	return counts, nil
}

func parseCountLine(line string) (string, int, error) {
	file, raw, ok := strings.Cut(line, " ")
	if !ok || file == "" || strings.Contains(file, " ") {
		return "", 0, fmt.Errorf("want path and count")
	}
	if filepath.IsAbs(file) || file == ".." || strings.HasPrefix(file, "../") || strings.Contains(file, "\\") {
		return "", 0, fmt.Errorf("path must be relative")
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 || strings.TrimSpace(raw) != raw {
		return "", 0, fmt.Errorf("count must be a positive integer")
	}
	return filepath.ToSlash(file), n, nil
}
