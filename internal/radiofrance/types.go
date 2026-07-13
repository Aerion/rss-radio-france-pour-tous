// Types below capture a subset of an unofficial, reverse-engineered Radio
// France mobile API. Field comments are based on live inspection of real
// responses, not official documentation (none exists publicly) - notes on
// "always"/"sometimes present" reflect samples, not a spec.
//
// Entity model: Show 1:N Diffusion (episode) 1:N Manifestation (one audio
// file, in several delivery variants - see Diffusion.ManifestationID).
// Responses use a denormalized JSON:API-ish shape: a `diffusions` /
// `resultItems` list carries only foreign keys (e.g. Diffusion.Relationships
// or a `concept` field this client doesn't capture), with the actual Show
// objects deduplicated once each in a top-level `included.shows` map keyed
// by ID - callers join client-side (see getDiffusionsPage/getShowDetails).
//
// Gotcha: for "selection" and "serie" kind shows (curated collections /
// anthology groupings, e.g. a themed "best of" page), a diffusion's real
// show is a *different* underlying show than the one you queried by -
// that's the show ID that ends up in `included.shows`, so it's absent
// under the ID you asked for. GetShowDiffusions' fallback to a direct
// shows/{id} call handles the plain "show missing from included" case, but
// note it fetches details for the *queried* show, not the diffusion's real
// show - selection/serie shows may end up with slightly mismatched
// show-level metadata (title/image) versus their diffusions. Not fixed
// here since it wasn't hit in practice by the current fixtures/tests.
package radiofrance

import "time"

// Visual is a cover-image variant as returned by the Radio France API.
// Observed Name values: "square_banner", "square_visual", "background",
// "banner", "visual_vertical", "concept_visual".
type Visual struct {
	Name       string `json:"name"`
	VisualUUID string `json:"visual_uuid"`
}

// Show is a podcast show's metadata (also used for "selection"/"serie" kind
// shows, which group another show's diffusions rather than owning their
// own - see package doc).
type Show struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Path       string `json:"path"`
	Standfirst string `json:"standfirst"`
	// MainImage is a visual UUID, used as ImageURL's fallback when Visuals
	// is empty. Always present on real shows.
	MainImage string   `json:"mainImage"`
	Visuals   []Visual `json:"visuals"`
	// Extra carries a grab-bag of editorial metadata; we only pick out the
	// iTunes category fields, which map directly to <itunes:category>.
	Extra struct {
		ItunesCat    string `json:"itunesCat"`
		ItunesSubCat string `json:"itunesSubCat"`
	} `json:"extra"`
}

// Diffusion is a single podcast episode.
type Diffusion struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Path is this episode's web page URL. Absent on older/rerun episodes
	// that never got a dedicated page (observed on a substantial fraction
	// of episodes on at least one long-running show) - callers should not
	// assume it's always set.
	Path       string `json:"path"`
	Standfirst string `json:"standfirst"`
	// BodyMarkdown is the full episode description in Markdown, with some
	// Radio-France-specific shortcodes (e.g. "{% bounce %}") mixed in.
	BodyMarkdown string `json:"bodyMarkdown"`
	// CreatedTime is a Unix timestamp (seconds). Drives both the RSS
	// pubDate and the guid backward-compatibility cutoff (see
	// internal/feed).
	CreatedTime int64 `json:"createdTime"`
	// UpdatedTime is a Unix timestamp (seconds) that changes whenever this
	// diffusion is edited/republished (e.g. relinked manifestations),
	// independently of CreatedTime. Used by internal/episodecache as a
	// cheap "has this episode changed" cache-invalidation signal, cheaper
	// than re-fetching and diffing the manifestation itself.
	UpdatedTime int64 `json:"updatedTime"`
	// MainImage is a visual UUID, used as ImageURL's fallback. Like Path,
	// it's sometimes absent (correlates with Path being absent too) - the
	// show's own MainImage is the fallback in that case.
	MainImage     string   `json:"mainImage"`
	Visuals       []Visual `json:"visuals"`
	Relationships struct {
		// Manifestations lists this episode's audio file variants (not
		// duplicate episodes - typically ~8 IDs for one on-air episode:
		// {2 podcast feed IDs} x {stream vs. download host} x {format}, all
		// serving the same underlying audio). See ManifestationID for how
		// this client picks one.
		Manifestations []string `json:"manifestations"`
		// OriginDiffusion is the original broadcast a rerun repeats, present
		// on reruns only (observed as a single-element array). Reruns
		// otherwise look like independent diffusions but typically carry no
		// MainImage of their own - see OriginDiffusionID.
		OriginDiffusion []string `json:"originDiffusion"`
	} `json:"relationships"`
}

// ManifestationID returns the ID of this diffusion's audio manifestation to
// link in the feed by default, or "" if it has none (some diffusions have
// no mp3 version). This is array position 0 of Relationships.Manifestations,
// not necessarily the manifestation flagged Principal by the API (see
// rawManifestation.Principal). internal/episodecache.Resolver is what
// actually picks the principal one when possible, falling back to this
// only as a last resort; see its doc comment for why principal selection
// matters (non-principal manifestations usually carry an expiration date;
// this one is chosen not to, per samples, but that's a byproduct of it
// being array position 0 in an effectively arbitrary order, not a
// guarantee).
func (d Diffusion) ManifestationID() string {
	if len(d.Relationships.Manifestations) == 0 {
		return ""
	}
	return d.Relationships.Manifestations[0]
}

// OriginDiffusionID returns the ID of the original broadcast this diffusion
// reruns, or "" if it isn't a rerun. Live samples show a rerun's own
// MainImage is typically absent (falling back to Visuals, which are just the
// show's shared banner), while the origin diffusion carries the real
// per-episode artwork - see episodecache's image resolution.
func (d Diffusion) OriginDiffusionID() string {
	if len(d.Relationships.OriginDiffusion) == 0 {
		return ""
	}
	return d.Relationships.OriginDiffusion[0]
}

// diffusionsResponse is the raw shape of GET shows/{id}/diffusions.
type diffusionsResponse struct {
	Links struct {
		// Next is a full relative path (with page[offset] incremented) to
		// the next page, or "" on the last page. An out-of-range offset
		// returns HTTP 200 with an empty Data and no Next, not an error.
		Next string `json:"next"`
	} `json:"links"`
	Data []struct {
		Diffusions Diffusion `json:"diffusions"`
	} `json:"data"`
	Included struct {
		// Shows is keyed by show ID - see package doc for the
		// selection/serie case where that's not the ID you requested.
		Shows map[string]Show `json:"shows"`
		// Manifestations is keyed by manifestation ID, populated when the
		// request includes include=manifestations. Coverage is NOT
		// exhaustive - live samples showed anywhere from ~65% to ~99% of a
		// page's referenced manifestation IDs actually present here,
		// varying by show - so callers must still be able to fall back to
		// a per-ID GetManifestation call for whatever's missing.
		Manifestations map[string]rawManifestation `json:"manifestations"`
	} `json:"included"`
}

// showResponse is the raw shape of GET shows/{id}.
type showResponse struct {
	Data struct {
		Shows Show `json:"shows"`
	} `json:"data"`
}

// diffusionResponse is the raw shape of GET diffusions/{id}.
type diffusionResponse struct {
	Data struct {
		Diffusions Diffusion `json:"diffusions"`
	} `json:"data"`
}

// diffusionManifestationsResponse is the raw shape of GET
// diffusions/{id}?include=manifestations - same Included.Manifestations
// shape as diffusionsResponse, minus the fields GetDiffusionManifestations
// doesn't need.
type diffusionManifestationsResponse struct {
	Included struct {
		Manifestations map[string]rawManifestation `json:"manifestations"`
	} `json:"included"`
}

// manifestationResponse is the raw shape of GET manifestations/{id}.
type manifestationResponse struct {
	Data struct {
		Manifestations rawManifestation `json:"manifestations"`
	} `json:"data"`
}

// rawManifestation is a manifestation object's raw JSON shape, as returned
// both standalone (GET manifestations/{id}) and inline via
// included.manifestations on a diffusions/shows call requesting
// include=manifestations.
type rawManifestation struct {
	// URL is the playable/downloadable audio file URL. Two distinct CDN
	// hosts are used across a diffusion's sibling manifestations:
	// media.radiofrance-podcast.net (streaming) and proxycast.radiofrance.fr
	// (download).
	URL string `json:"url"`
	// Duration in seconds. This is the field the pre-rewrite feed was
	// missing itunes:duration from - it only lives here, not on the
	// diffusion. Usually (93-100% of samples) identical across a
	// diffusion's sibling manifestations, but can differ by ~1 second
	// (separate encodes), so it's tied to whichever manifestation was
	// actually selected, not diffusion-wide.
	Duration int `json:"duration"`
	// Principal is true for exactly one of a diffusion's ~8 sibling
	// manifestations. Live samples show it's reliably the
	// media.radiofrance-podcast.net (streaming) one, and reliably the one
	// variant with no expiration date - see ExpiresAt.
	Principal bool `json:"principal"`
	// DownloadExpirationDate/StreamExpirationDate (Unix seconds, absent if
	// not applicable) - the URL above can stop working after this date.
	// Live samples: the Principal manifestation never has either date set;
	// non-principal download-host manifestations carry
	// DownloadExpirationDate ~97% of the time. This is *why* principal
	// selection matters in practice, not just in theory - picking a
	// non-principal manifestation has a high chance of embedding a link
	// that goes dead after a finite date.
	DownloadExpirationDate *int64 `json:"downloadExpirationDate"`
	StreamExpirationDate   *int64 `json:"streamExpirationDate"`
}

// toDetails converts m to the exported ManifestationDetails shape, or the
// zero value if m has no URL (e.g. a zero-value rawManifestation from a
// missing/empty response).
func (m rawManifestation) toDetails() ManifestationDetails {
	if m.URL == "" {
		return ManifestationDetails{}
	}
	details := ManifestationDetails{
		URL:       m.URL,
		Duration:  time.Duration(m.Duration) * time.Second,
		Principal: m.Principal,
	}
	switch {
	case m.DownloadExpirationDate != nil:
		t := time.Unix(*m.DownloadExpirationDate, 0)
		details.ExpiresAt = &t
	case m.StreamExpirationDate != nil:
		t := time.Unix(*m.StreamExpirationDate, 0)
		details.ExpiresAt = &t
	}
	return details
}

// toManifestationDetails converts raw - an Included.Manifestations map, as
// found on diffusionsResponse and diffusionManifestationsResponse - to the
// exported ManifestationDetails shape, dropping any entry with no URL
// (toDetails' zero-value case, e.g. a manifestation the API listed but left
// otherwise empty).
func toManifestationDetails(raw map[string]rawManifestation) map[string]ManifestationDetails {
	manifestations := make(map[string]ManifestationDetails, len(raw))
	for id, m := range raw {
		if details := m.toDetails(); details.URL != "" {
			manifestations[id] = details
		}
	}
	return manifestations
}

// ManifestationDetails is a manifestation's playback-relevant fields.
type ManifestationDetails struct {
	URL       string
	Duration  time.Duration
	Principal bool
	// ExpiresAt is when URL may stop working, or nil if the API didn't
	// report an expiration for this manifestation.
	ExpiresAt *time.Time
}

// searchResponse is the raw shape of GET stations/search.
type searchResponse struct {
	Data []struct {
		ResultItems struct {
			// Model discriminates what this result is; only "show" results
			// are usable here; "diffusion" results carry no show
			// relationship in the response at all, so this client skips
			// them rather than issuing follow-up calls to resolve one.
			Model         string `json:"model"`
			Relationships struct {
				Show []string `json:"show"`
			} `json:"relationships"`
		} `json:"resultItems"`
	} `json:"data"`
	Included struct {
		Shows map[string]Show `json:"shows"`
	} `json:"included"`
}

// SearchResult is a show matched by a search query.
type SearchResult struct {
	ShowID     string
	Title      string
	Path       string
	Standfirst string
	// ImgURL is "" when the show has no usable cover image.
	ImgURL string
}

// ShowDiffusions is the result of fetching one page of a show's episode
// list.
type ShowDiffusions struct {
	Diffusions  []Diffusion
	ShowDetails Show
	// Manifestations holds whatever manifestation details came back inline
	// with this page (see diffusionsResponse.Included.Manifestations) -
	// not exhaustive, keyed by manifestation ID. A given diffusion's
	// manifestations may be partially or entirely missing here; callers
	// needing a specific one not present should fall back to
	// Client.GetManifestation.
	Manifestations map[string]ManifestationDetails
	// NextPageIdx is the page index to fetch next, or nil if there is no
	// further page.
	NextPageIdx *int
}
