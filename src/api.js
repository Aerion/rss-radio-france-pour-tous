import { getImgUrl } from "./feed.js";
import { baseUrl, routePrefixRss } from "./config.js";

const getRadioFranceUrl = async (path) => {
  const url = `https://api.radiofrance.fr/v1/${path}`;
  const response = await fetch(url, {
    method: "GET",
    headers: {
      Accept: "application/x.radiofrance.mobileapi+json",
      "User-Agent": "AppRF",
      "x-token": "9ab343ce-cae2-4bdb-90ca-526a3dede870",
    },
  });
  if (!response.ok) {
    throw new Error(`Radio France API error: ${response.status} ${response.statusText} (${path})`);
  }
  return await response.json();
};

/**
 * @param {string} showId
 * @param {number} page - 0-based page index, or -1 to fetch all pages
 * @returns {Promise<{diffusions: object[], showDetails: object, manifestations: object, nextPageIdx: number|undefined}>}
 */
export const getShowDiffusions = async (showId, page) => {
  const diffusions = [];
  const manifestations = [];
  let showDetails = undefined;

  const shouldFetchAllDiffusions = page === -1;
  if (shouldFetchAllDiffusions) {
    page = 0;
  }

  let json;

  do {
    json = await getRadioFranceUrl(
      `shows/${showId}/diffusions?filter[manifestations][exists]=true&include=show&include=manifestations&include=series&page[offset]=${page}`
    );

    if (showDetails === undefined) {
      showDetails = json.included?.shows?.[showId];
      if (showDetails === undefined) {
        // For some reason, for some podcasts, the show is not included in the manifestations
        // It's very rare, but it's the case for the show 1aaba3dd-be85-4bbd-b046-c1343affc505
        // Mitigate it by calling the show details endpoint directly
        const showDetailsJson = await getRadioFranceUrl(`shows/${showId}`);
        showDetails = showDetailsJson.data?.shows;
      }
    }

    if (Array.isArray(json.data)) {
      diffusions.push(...json.data.map((item) => item.diffusions));
    }
    for (const k in json.included?.manifestations) {
      manifestations[k] = json.included.manifestations[k];
    }

    page += 1;
  } while (shouldFetchAllDiffusions && json.links?.next !== undefined);

  return {
    diffusions,
    showDetails,
    manifestations,
    nextPageIdx: json.links?.next !== undefined ? page : undefined,
  };
};

/**
 * @param {string} query
 * @returns {Promise<Array<{title: string, path: string, standfirst: string, imgUrl: string|null, rssUrl: string}>>}
 */
export const getSearchResults = async (query) => {
  const json = await getRadioFranceUrl(
    `/stations/search?value=${encodeURIComponent(query)}&include=show`
  );
  return (json.data ?? [])
    .filter((item) => item.resultItems?.model === "show")
    .flatMap((item) => {
      try {
        const show = json.included?.shows?.[item.resultItems?.relationships?.show?.[0]];

        if (!show) {
          console.warn("Show not found in included data", item.resultItems.relationships.show[0]);
          return [];
        }

        return [
          {
            title: show.title,
            path: show.path,
            standfirst: show.standfirst,
            imgUrl: getImgUrl(show.visuals, show.mainImage),
            rssUrl: `${baseUrl}${routePrefixRss}${show.id}`,
          },
        ];
      } catch (error) {
        console.warn("Exception while parsing show", item, error);
        return [];
      }
    });
};
