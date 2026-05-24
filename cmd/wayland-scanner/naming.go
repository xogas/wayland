package main

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// pascal converts a snake_case XML name to PascalCase, normalizing "Id" to "ID".
func pascal(s string) string {
	parts := strings.Split(s, "_")
	for i, p := range parts {
		if p != "" {
			parts[i] = capitalize(p)
		}
	}
	return strings.ReplaceAll(strings.Join(parts, ""), "Id", "ID")
}

// camel converts a snake_case XML name to camelCase, escaping Go keywords.
func camel(s string) string {
	p := pascal(s)
	if p == "" {
		return p
	}
	r, n := utf8.DecodeRuneInString(p)
	result := string(unicode.ToLower(r)) + p[n:]
	if goKeywords[result] {
		return result + "_"
	}
	return result
}

// snakeCase converts a PascalCase type name to snake_case for file names.
func snakeCase(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(r))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[n:]
}

// typeName derives the Go type name for an interface name.
func typeName(xmlName, prefix, suffix string) string {
	s := xmlName
	if prefix != "" {
		s = strings.TrimPrefix(s, prefix)
	}
	if suffix != "" {
		s = strings.TrimSuffix(s, suffix)
	}
	return pascal(s)
}

// autoPrefix returns the common "xxx_" protocol family prefix shared by all
// interfaces, or "" when there is none.
func autoPrefix(ifaces []Interface) string {
	if len(ifaces) == 0 {
		return ""
	}
	common := ifaces[0].Name
	for _, iface := range ifaces[1:] {
		common = longestCommonPrefix(common, iface.Name)
		if common == "" {
			return ""
		}
	}
	if idx := strings.IndexByte(common, '_'); idx >= 0 {
		return common[:idx+1]
	}
	return ""
}

func longestCommonPrefix(a, b string) string {
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return a[:i]
		}
	}
	return a[:n]
}

// goKeywords lists Go keywords and predeclared identifiers that must not be
// used as parameter names.
var goKeywords = map[string]bool{
	"break": true, "default": true, "func": true, "interface": true, "select": true,
	"case": true, "defer": true, "go": true, "map": true, "struct": true,
	"chan": true, "else": true, "goto": true, "package": true, "switch": true,
	"const": true, "fallthrough": true, "if": true, "range": true, "type": true,
	"continue": true, "for": true, "import": true, "return": true, "var": true,
	"error": true, "len": true, "cap": true, "string": true, "nil": true,
}
