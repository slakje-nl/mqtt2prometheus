package config

import (
	"regexp"
	"sort"
)

var templateRef = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)

func TemplateRefs(s string) []string {
	matches := templateRef.FindAllStringSubmatch(s, -1)
	names := make([]string, 0, len(matches))

	for _, m := range matches {
		names = append(names, m[1])
	}

	return names
}

func ExpandTemplate(s string, values map[string]string) string {
	return templateRef.ReplaceAllStringFunc(s, func(ref string) string {
		return values[templateRef.FindStringSubmatch(ref)[1]]
	})
}

func LabelNames(captures []string, labelSets ...map[string]Label) []string {
	consumed := map[string]struct{}{}
	set := map[string]struct{}{}

	for _, labels := range labelSets {
		for name, label := range labels {
			set[name] = struct{}{}

			for _, ref := range TemplateRefs(label.Value) {
				consumed[ref] = struct{}{}
			}
		}
	}

	for _, name := range captures {
		if _, taken := consumed[name]; !taken {
			set[name] = struct{}{}
		}
	}

	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}

	sort.Strings(names)

	return names
}

func CaptureNames(re *regexp.Regexp) []string {
	var names []string

	for _, name := range re.SubexpNames() {
		if name != "" {
			names = append(names, name)
		}
	}

	return names
}
