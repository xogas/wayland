package main

import "strings"

func packageName(dir string) string {
	s := strings.NewReplacer("-", "", "_", "").Replace(dir)
	return strings.ToLower(s)
}

func commonPrefix(names []string) string {
	if len(names) < 2 {
		return ""
	}
	prefix := names[0]
	for _, n := range names[1:] {
		for !strings.HasPrefix(n, prefix) {
			prefix = prefix[:len(prefix)-1]
			if prefix == "" {
				return ""
			}
		}
	}
	if i := strings.LastIndex(prefix, "_"); i >= 0 {
		return prefix[:i+1]
	}
	return ""
}

func camel(s string) string {
	parts := strings.Split(s, "_")
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		if p == "id" {
			b.WriteString("ID")
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]))
		b.WriteString(p[1:])
	}
	return b.String()
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}
