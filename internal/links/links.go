// Package links finds, checks and tidies the reel link the Shortcut sends.
package links

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// InvalidLinkError means the text didn't contain a link we're willing to download.
// Its message is safe to show to the user.
type InvalidLinkError struct{ msg string }

func (e *InvalidLinkError) Error() string { return e.msg }

func invalid(msg string) error { return &InvalidLinkError{msg} }

var (
	urlRE = regexp.MustCompile(`(?i)https?://[^\s<>"']+`)
	// /reel/<code>, /reels/<code>, /p/<code>, /tv/<code>, optionally after a /<username>/ prefix.
	instagramPathRE = regexp.MustCompile(`^/(?:[A-Za-z0-9._]+/)?(reels?|p|tv)/([A-Za-z0-9_-]+)`)

	instagramHosts = set("instagram.com", "www.instagram.com", "m.instagram.com", "instagr.am", "www.instagr.am")
	tiktokHosts    = set("tiktok.com", "www.tiktok.com", "m.tiktok.com", "vm.tiktok.com", "vt.tiktok.com")
)

func set(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, item := range items {
		m[item] = true
	}
	return m
}

// ExtractURL returns the first http(s) URL in text. The share sheet sometimes hands over
// "Check out this reel https://..." rather than a bare link.
func ExtractURL(text string) (string, error) {
	match := urlRE.FindString(text)
	if match == "" {
		return "", invalid("That doesn't look like a link. Share a reel from Instagram.")
	}
	return strings.TrimRight(match, ".,;:!?)"), nil
}

// Normalize pulls the link out of text, checks it's an Instagram (or TikTok) post and returns
// a clean URL. Instagram links lose their tracking query (?igsh=...) and become
// https://www.instagram.com/reel/<code>/, which is what gets stored as the recipe's source.
//
// The result is rebuilt from scratch, so userinfo, ports and plain http never reach yt-dlp.
func Normalize(text string, allowTikTok bool) (string, error) {
	raw, err := ExtractURL(text)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", invalid("That doesn't look like a link. Share a reel from Instagram.")
	}
	host := strings.ToLower(u.Hostname())

	switch {
	case instagramHosts[host]:
		m := instagramPathRE.FindStringSubmatch(u.Path)
		if m == nil {
			return "", invalid("That Instagram link isn't a reel or post.")
		}
		kind, code := m[1], m[2]
		if kind == "reels" {
			kind = "reel"
		}
		return fmt.Sprintf("https://www.instagram.com/%s/%s/", kind, code), nil

	case tiktokHosts[host] && allowTikTok:
		if strings.Trim(u.Path, "/") == "" {
			return "", invalid("That TikTok link isn't a video.")
		}
		// Short links (vm./vt.) are redirects that yt-dlp follows; keep them as they are.
		return "https://" + host + u.EscapedPath(), nil

	case allowTikTok:
		return "", invalid("Only Instagram and TikTok links are supported.")
	default:
		return "", invalid("Only Instagram links are supported.")
	}
}
