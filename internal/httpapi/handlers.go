package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err := w.Write(homepageHTML)
	return err
}

func (s *Server) handleRobots(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, err := fmt.Fprint(w, "User-agent: *\nDisallow: /rss/\nDisallow: /audio/\n")
	return err
}

// searchResultJSON is the shape expected by the homepage's client-side
// search UI (internal/httpapi/web/index.html.tmpl).
type searchResultJSON struct {
	Title      string `json:"title"`
	Path       string `json:"path"`
	Standfirst string `json:"standfirst"`
	ImgURL     string `json:"imgUrl"`
	RSSURL     string `json:"rssUrl"`
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) error {
	query := r.URL.Query().Get("query")
	if query == "" {
		http.Error(w, "Missing query parameter", http.StatusBadRequest)
		return nil
	}

	results, err := s.api.Search(r.Context(), query)
	if err != nil {
		return err
	}

	out := make([]searchResultJSON, len(results))
	for i, res := range results {
		out[i] = searchResultJSON{
			Title:      res.Title,
			Path:       res.Path,
			Standfirst: res.Standfirst,
			ImgURL:     res.ImgURL,
			RSSURL:     s.publicBaseURL + "/rss/" + res.ShowID,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(out)
}

func (s *Server) handleRSS(w http.ResponseWriter, r *http.Request) error {
	showID := r.PathValue("showId")

	page := 0
	if p := r.URL.Query().Get("page"); p != "" {
		if parsed, err := strconv.Atoi(p); err == nil && parsed > 0 {
			page = parsed
		}
	}

	showDiffusions, err := s.api.GetShowDiffusions(r.Context(), showID, page)
	if err != nil {
		return err
	}

	var nextPageURL string
	if showDiffusions.NextPageIdx != nil {
		nextPageURL = fmt.Sprintf("%s%s?page=%d", s.publicBaseURL, r.URL.Path, *showDiffusions.NextPageIdx)
	}

	body, err := s.feedBuilder.Build(showDiffusions.Diffusions, showDiffusions.ShowDetails, nextPageURL)
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	_, err = fmt.Fprint(w, body)
	return err
}

func (s *Server) handleAudio(w http.ResponseWriter, r *http.Request) error {
	manifestationID := r.PathValue("manifestationId")

	mp3URL, err := s.api.GetManifestationURL(r.Context(), manifestationID)
	if err != nil {
		return err
	}

	http.Redirect(w, r, mp3URL, http.StatusFound)
	return nil
}
