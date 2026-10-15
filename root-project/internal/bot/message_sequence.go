package bot

import (
	"math/rand"
	"strings"
)

func renderMessageTemplate(template string, rng *rand.Rand, assignedUsername string) string {
	template = strings.TrimSpace(template)
	if template == "" {
		return ""
	}

	if strings.Contains(template, "%username%") {
		template = strings.ReplaceAll(template, "%username%", assignedUsername)
	}

	parts := make([]string, 0)
	for index := 0; index < len(template); {
		if template[index] == '{' {
			closing := strings.IndexByte(template[index+1:], '}')
			if closing >= 0 {
				appendNormalizedParts(&parts, chooseTemplateOption(template[index+1:index+1+closing], rng))
				index += closing + 2
				continue
			}
		}

		nextGroup := strings.IndexByte(template[index:], '{')
		if nextGroup < 0 {
			appendNormalizedParts(&parts, template[index:])
			break
		}

		appendNormalizedParts(&parts, template[index:index+nextGroup])
		index += nextGroup
	}

	return strings.Join(parts, " ")
}

func chooseUsername(usernames []string, rng *rand.Rand) string {
	cleaned := make([]string, 0, len(usernames))
	for _, username := range usernames {
		username = strings.TrimSpace(username)
		if username == "" {
			continue
		}
		cleaned = append(cleaned, username)
	}

	if len(cleaned) == 0 {
		return ""
	}
	if len(cleaned) == 1 || rng == nil {
		return cleaned[0]
	}
	return cleaned[rng.Intn(len(cleaned))]
}

func chooseTemplateOption(group string, rng *rand.Rand) string {
	options := strings.Split(group, "|")
	cleaned := make([]string, 0, len(options))

	for _, option := range options {
		option = strings.TrimSpace(option)
		if option == "" {
			continue
		}
		cleaned = append(cleaned, option)
	}

	if len(cleaned) == 0 {
		return ""
	}
	if len(cleaned) == 1 || rng == nil {
		return cleaned[0]
	}

	return cleaned[rng.Intn(len(cleaned))]
}

func appendNormalizedParts(parts *[]string, value string) {
	for _, field := range strings.Fields(strings.TrimSpace(value)) {
		if field == "" {
			continue
		}
		*parts = append(*parts, field)
	}
}
