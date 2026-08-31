package radiofrance

import (
	"net/url"
	"strings"
)

// blockedMediaTypes are rawManifestation.MediaType values observed, since
// September 2026, on manifestations that are a video-hosting page rather
// than playable audio - see IsPlayableAudioManifestation.
var blockedMediaTypes = map[string]bool{
	"youtube":     true,
	"dailymotion": true,
}

// IsPlayableAudioManifestation reports whether mediaType
// (rawManifestation.MediaType) denotes real playable audio rather than a
// video-hosting page wrongly listed as a manifestation. This is Radio
// France's own classification - preferred over guessing from the URL's
// host, which is only kept as a fallback for data that doesn't carry
// MediaType - see IsPlayableAudioURL.
func IsPlayableAudioManifestation(mediaType string) bool {
	return !blockedMediaTypes[strings.ToLower(mediaType)]
}

// blockedManifestationHosts backs IsPlayableAudioURL.
var blockedManifestationHosts = []string{
	"youtube.com",
	"youtu.be",
	"dailymotion.com",
}

// IsPlayableAudioURL reports whether rawURL looks like a playable audio file
// rather than a video-hosting page URL, judging only by host. Used as a
// defense-in-depth backstop where MediaType isn't available - e.g. an
// episodecache manifestation_cache row written before this filter shipped
// only ever stored URL, not MediaType. Matches host or any subdomain,
// case-insensitively (e.g. "youtube.com", "www.youtube.com" both match;
// "notyoutube.com" does not). An unparsable rawURL returns true - policing
// malformed URLs isn't this function's job.
func IsPlayableAudioURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return true
	}
	host := strings.ToLower(u.Hostname())
	for _, blocked := range blockedManifestationHosts {
		if host == blocked || strings.HasSuffix(host, "."+blocked) {
			return false
		}
	}
	return true
}
