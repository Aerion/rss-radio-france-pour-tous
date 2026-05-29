import { getShowDiffusions, getSearchResults } from "./src/api.js";
import { buildFeed } from "./src/feed.js";
import { getHomePageContents } from "./src/html.js";
import { routePrefixRss, routeSearch } from "./src/config.js";

if (typeof addEventListener !== "undefined") {
  addEventListener("fetch", (event) => {
    event.respondWith(handleRequest(event.request));
  });
}

export const handleRequest = async (request) => {
  try {
    const url = new URL(request.url);

    if (url.pathname.startsWith(routePrefixRss)) {
      const showId = url.pathname.substring(routePrefixRss.length);
      const page = parseInt(url.searchParams.get("page") || "0", 10);
      const showDiffusions = await getShowDiffusions(showId, page);

      let nextPageUrl;
      if (showDiffusions.nextPageIdx !== undefined) {
        nextPageUrl = new URL(url);
        nextPageUrl.searchParams.delete("page");
        nextPageUrl.searchParams.append("page", showDiffusions.nextPageIdx);
      } else {
        nextPageUrl = undefined;
      }
      const feed = buildFeed(showDiffusions, nextPageUrl);
      return new Response(feed, {
        headers: {
          "Content-Type": "application/xml; charset=utf-8",
        },
      });
    } else if (url.pathname === routeSearch) {
      const searchResults = await getSearchResults(url.searchParams.get("query"));
      return new Response(JSON.stringify(searchResults), {
        headers: {
          "Content-Type": "application/json",
        },
      });
    } else if (url.pathname === "/") {
      return new Response(getHomePageContents(), {
        headers: {
          "Content-Type": "text/html",
        },
      });
    } else {
      return new Response(null, { status: 404, statusText: "Not found" });
    }
  } catch (error) {
    console.error("Unhandled error in handleRequest", error);
    return new Response("Internal Server Error", { status: 500 });
  }
};
