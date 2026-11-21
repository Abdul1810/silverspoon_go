package engine

import (
	"errors"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"silverspoon/internal/providers"
	"silverspoon/internal/segmented"
)

var spaces = regexp.MustCompile(`\s+`)

// errorType names an error the way the Python app's type(e).__name__ did, so
// messages read the same.
func errorType(err error) string {
	var hs *segmented.HTTPStatusError
	var ph *providers.HTTPError
	var rns *segmented.RangeNotSupportedError
	var re *providers.ResolveError
	var ne net.Error
	switch {
	case errors.As(err, &hs), errors.As(err, &ph):
		return "HTTPError"
	case errors.As(err, &rns):
		return "RangeNotSupported"
	case errors.As(err, &re):
		return "ResolveError"
	case errors.Is(err, segmented.ErrConnectionClosed):
		return "ConnectionError"
	case errors.As(err, &ne):
		return "URLError"
	}
	return "Error"
}

// FormatError is the user-facing one-liner for an error (format_error_message).
func FormatError(err error) string {
	const maxLen = 160
	if err == nil {
		return ""
	}
	text := strings.TrimSpace(err.Error())
	var hs *segmented.HTTPStatusError
	if errors.As(err, &hs) {
		text = "HTTP Error " + strconv.Itoa(hs.Code) + ": " + http.StatusText(hs.Code)
	}
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "connectionabortederror") || strings.Contains(text, "10053"):
		return "Connection was aborted by your computer or network security software. Try again, or check firewall/antivirus settings."
	case strings.Contains(lower, "connection reset") || strings.Contains(lower, "connectionreseterror") ||
		strings.Contains(lower, "forcibly closed"):
		return "Connection was reset by the server or your network. Try again later."
	case strings.Contains(lower, "timed out") || strings.Contains(lower, "timeout") ||
		strings.Contains(lower, "deadline exceeded"):
		return "The connection timed out. Check your network and try again."
	}
	if text == "" {
		return errorType(err)
	}
	text = spaces.ReplaceAllString(text, " ")
	if len(text) > maxLen {
		text = strings.TrimRight(text[:maxLen], " \t") + "..."
	}
	return errorType(err) + ": " + text
}
