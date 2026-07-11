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
	} `json:"relationships"`
}

// ManifestationID returns the ID of this diffusion's audio manifestation to
// link in the feed, or "" if it has none (some diffusions have no mp3
// version).
//
// This picks Relationships.Manifestations[0] rather than the manifestation
// actually flagged Principal by the API, because telling them apart
// requires fetching every sibling manifestation - which would reintroduce
// the N-calls-per-episode problem the lazy /audio/ redirect was
// specifically built to avoid (see commit bf98093). In samples checked,
// array position 0 was never the principal one, so this is a known
// imprecision, not a considered choice of "the best" variant - just "a"
// working one. Phase 4's planned episode cache (which will fetch every
// manifestation anyway) is the natural place to select Principal properly
// and cache the result, without adding calls to the hot path.
func (d Diffusion) ManifestationID() string {
	if len(d.Relationships.Manifestations) == 0 {
		return ""
	}
	return d.Relationships.Manifestations[0]
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
	} `json:"included"`
}

// showResponse is the raw shape of GET shows/{id}.
type showResponse struct {
	Data struct {
		Shows Show `json:"shows"`
	} `json:"data"`
}

// manifestationResponse is the raw shape of GET manifestations/{id}.
type manifestationResponse struct {
	Data struct {
		Manifestations struct {
			// URL is the playable/downloadable audio file URL. Two
			// distinct CDN hosts are used across a diffusion's sibling
			// manifestations: media.radiofrance-podcast.net (streaming)
			// and proxycast.radiofrance.fr (download) - not otherwise
			// distinguished by this client.
			URL string `json:"url"`
		} `json:"manifestations"`
	} `json:"data"`
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
	// NextPageIdx is the page index to fetch next, or nil if there is no
	// further page.
	NextPageIdx *int
}
