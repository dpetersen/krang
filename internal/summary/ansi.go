package summary

import "regexp"

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b\][^\x1b]*\x1b\\|\x1b\].*?\a|\x1b[()][AB012]|\x1b\[[\?]?[0-9;]*[hlm]`)

func StripANSI(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}
