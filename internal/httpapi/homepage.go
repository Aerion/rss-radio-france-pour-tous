package httpapi

import _ "embed"

// The homepage is fully static: its one dynamic-looking bit (the search
// fetch URL) is same-origin, since this app serves both the page and
// /search/, so a relative URL works and no templating is needed.
//
//go:embed web/index.html
var homepageHTML []byte
