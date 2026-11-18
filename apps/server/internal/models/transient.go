package models

import (
	"context"
	"errors"
	"net"
	"regexp"
)

// Network trouble and provider overload, in the words providers and Go use.
var transientText = regexp.MustCompile(`(?i)overloaded|too many requests|rate.?limit|timeout|timed out|temporar|ECONNREFUSED|ECONNRESET|ENOTFOUND|EAI_AGAIN|fetch failed|socket hang up|connection refused|connection reset|no such host|broken pipe|unexpected EOF|TLS handshake|i/o timeout|network is unreachable|bad gateway|service unavailable`)

// Out of money or quota: retrying in a few minutes won't fix it.
var exhaustedText = regexp.MustCompile(`(?i)quota|credit|billing|exhaust|insufficient`)

// IsTransient says whether a failed model call is worth trying again in a
// minute: no response (network down, TLS or DNS failure), a timeout, rate
// limiting or an overloaded provider. Bad requests, auth errors and an
// exhausted quota are permanent: the same call fails the same way.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var api *APICallError
	if errors.As(err, &api) {
		s := api.StatusCode
		switch {
		case s == 0:
			return true
		case s == 429:
			return !exhaustedText.MatchString(api.Message + " " + api.ResponseBody)
		case s == 408 || s == 409 || s >= 500:
			return true
		}
		return false
	}
	var se *StreamError
	if errors.As(err, &se) {
		if se.StatusCode == 429 || se.StatusCode >= 500 {
			return !exhaustedText.MatchString(se.Message + " " + string(se.Data))
		}
		if se.IsRetryable {
			return true
		}
		return transientText.MatchString(se.Message+" "+se.Type) && !exhaustedText.MatchString(se.Message)
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	if cause := errors.Unwrap(err); cause != nil && cause != err {
		return IsTransient(cause)
	}
	return transientText.MatchString(err.Error()) && !exhaustedText.MatchString(err.Error())
}
