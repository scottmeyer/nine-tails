package cli

import "strings"

// StoreCommand renders a POSIX-shell recipe for the selected invocation store.
// home is an absolute runtime binding, not persisted agent knowledge.
func StoreCommand(home, tail string) string {
	command := "nine-tails"
	if home != "" {
		command += " --home " + QuoteArgument(home)
	}
	return command + " " + tail
}

// QuoteArgument preserves one complete POSIX-shell argument literally.
func QuoteArgument(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

// InlineCode keeps shell backticks inside a Markdown code span as literal data.
func InlineCode(text string) string {
	width, run := 1, 0
	for _, r := range text {
		if r == '`' {
			run++
			if run >= width {
				width = run + 1
			}
		} else {
			run = 0
		}
	}
	fence := strings.Repeat("`", width)
	if strings.HasPrefix(text, "`") || strings.HasSuffix(text, "`") {
		text = " " + text + " "
	}
	return fence + text + fence
}
