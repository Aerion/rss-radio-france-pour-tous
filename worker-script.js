addEventListener("fetch", (event) => {
  event.respondWith(handleRequest(event.request));
});

const baseUrl = "https://radio-france-rss.aerion.workers.dev";
const routePrefixRss = "/rss/";
const routeSearch = "/search/";

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
  return await response.json();
};

const getShowDiffusions = async (showId) => {
  const json = await getRadioFranceUrl(
    `shows/${showId}/diffusions?filter[manifestations][exists]=true&include=show&include=manifestations&include=series`
  );
  return {
    diffusions: json.data.map((item) => item.diffusions),
    showDetails: json.included.shows[showId],
    manifestations: json.included.manifestations,
  };
};

const getImgUrl = (visuals, fallbackImgId) => {
  let chosenId;
  if (!visuals || visuals.length === 0) {
    chosenId = fallbackImgId;
  } else {
    const visualsMap = visuals.reduce((res, item) => {
      res[item.name] = item.visual_uuid;
      return res;
    }, {});
    chosenId =
      visualsMap["square_banner"] ??
      visualsMap["square_visual"] ??
      visuals[0].visual_uuid;
  }

  if (!chosenId) {
    return null;
  }

  return `https://api.radiofrance.fr/v1/services/embed/image/${chosenId}?preset=568x568`;
};

const buildFeed = ({ diffusions, showDetails, manifestations }) => {
  const buildElement = (name, innerText) => {
    return !!innerText ? `<${name}>${innerText}</${name}>` : "";
  };
  const buildImgElement = (url) => {
    return !!url ? `<itunes:image href="${imgUrl}"/>` : "";
  };

  const escapeXml = (unsafe) => {
    if (typeof unsafe === "undefined") return unsafe;
    // From https://stackoverflow.com/a/27979933
    return unsafe.replace(/[<>&'"]/g, function (c) {
      switch (c) {
        case "<":
          return "&lt;";
        case ">":
          return "&gt;";
        case "&":
          return "&amp;";
        case "'":
          return "&apos;";
        case '"':
          return "&quot;";
      }
    });
  };

  const buildItem = (diffusion) => {
    const manifestation =
      manifestations[
        diffusion.relationships.manifestations.find(
          (manifId) =>
            manifestations[manifId]?.principal &&
            !["youtube", "dailymotion"].includes(
              manifestations[manifId]?.mediaType
            )
        )
      ];
    if (typeof manifestation === "undefined") {
      console.log(
        `Item ${diffusion.id} visible at ${diffusion.path} has no mp3 version, skipping`
      );
      return "";
    }

    let guid = diffusion.id;
    if (new Date(diffusion.createdTime * 1000) <= new Date("Sep 12 2022")) {
      // backward compatibility: keep old id generation.
      guid = manifestation.id;
    }

    const imgUrl = getImgUrl(diffusion.visuals, diffusion.mainImage);
    return `    <item>
          <title>${escapeXml(diffusion.title)}</title>
          <guid>${guid}</guid>
          ${buildElement("link", diffusion.path)}
          <description>${escapeXml(diffusion.standfirst)}</description>
          <enclosure url="${manifestation.url}" type="audio/mpeg" />
          <pubDate>${new Date(
            diffusion.createdTime * 1000
          ).toUTCString()}</pubDate>
          <itunes:duration>${new Date(manifestation.duration * 1000)
            .toISOString()
            .substring(11, 19)}</itunes:duration>
          ${buildElement("itunes:image", diffusion.path)}
          ${buildImgElement(imgUrl)}
        </item>`;
  };

  const imgUrl = getImgUrl(showDetails.visuals, showDetails.mainImage);
  return `<?xml version="1.0" encoding="UTF-8"?>
    <rss xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd" xmlns:pa="http://podcastaddict.com" xmlns:podcastRF="http://radiofrance.fr/Lancelot/Podcast#" xmlns:googleplay="http://www.google.com/schemas/play-podcasts/1.0" version="2.0">
      <channel>
        <title>${escapeXml(showDetails.title)}</title>
        <link>${showDetails.path}</link>
        <description>${escapeXml(showDetails.standfirst)}</description>
        ${buildImgElement(imgUrl)}
    ${diffusions.map(buildItem).join("\n")}
      </channel>
    </rss>`;
};

const getHomePageContents = () => {
  return `<!DOCTYPE html>
<html>
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width,initial-scale=1" />
    <title>RSS Radio France pour tous</title>
    <link rel="stylesheet" href="https://unpkg.com/mvp.css" />
  </head>
  <body>
    <header>
      <h1>RSS Radio France pour tous</h1>
      <p>Le site pour rétablir les flux RSS de Radio France</p>
    </header>
    <main style="padding-top: 0px;">
      <section>
        <form>
          <label for="query">Titre de l'émission</label>
            <input
              type="search"
              name="query"
              id="query"
              placeholder="Rechercher un podcast…"
              required
            />
            <button>Rechercher</button>
        </form>
      </section>
      <section id="search-results-container"></section>
    </main>
    <footer>
      <section>
        <a href="https://github.com/Aerion/rss-radio-france-pour-tous">
          <svg
            xmlns="http://www.w3.org/2000/svg"
            viewBox="0 0 16 16"
            width="16"
            height="16"
            style="vertical-align: middle; margin-right: 4px"
          >
            <path
              style="fill: var(--color-link)"
              fill-rule="evenodd"
              d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z"
            ></path></svg
          >Aerion/rss-radio-france-pour-tous</a
        >
      </section>
    </footer>
  </body>

  <script>
    const searchResultsContainer = document.querySelector(
      "#search-results-container"
    );
    const setSearchResults = (searchResults) => {
      const nodes = searchResults.map((searchResult) => {
        const node = document.createElement("aside");
        const imgNodeHTML = searchResult['imgUrl'] ? \`<img src="\${searchResult['imgUrl']}" />\` : '';
        node.innerHTML = \`
          \${imgNodeHTML}
          <h3>\${searchResult["title"]}</h3>
          <small>\${searchResult["standfirst"]}</small>
          <p>
            <a href="\${searchResult['rssUrl']}" target="_blank">Flux RSS</a> - <a href="\${searchResult['path']}" target="_blank">Émission</a>
          </p>
        \`;
        return node;
      });

      searchResultsContainer.replaceChildren(...nodes);
    };

    const form = document.querySelector("form");
    const queryInput = document.querySelector("input");
    const handleSubmit = async (evt) => {
      evt.preventDefault();

      const query = queryInput.value;
      if (query.length === 0) {
        alert("Un champ de recherche doit être renseigné");
        return;
      }

      const response = await fetch(
        "${baseUrl}/search/?query=" +
          encodeURIComponent(query)
      );
      const searchResults = await response.json();
      setSearchResults(searchResults);
    };
    form.addEventListener("submit", handleSubmit);
  </script>
</html>
`;
};

const getSearchResults = async (query) => {
  const json = await getRadioFranceUrl(
    `/stations/search?value=${encodeURIComponent(query)}&include=show`
  );
  return json.data
    .filter((item) => item.resultItems.model === "show")
    .map((item) => {
      const show = json.included.shows[item.resultItems.relationships.show[0]];
      return {
        title: show.title,
        path: show.path,
        standfirst: show.standfirst,
        imgUrl: getImgUrl(show.visuals, show.mainImage),
        rssUrl: `${baseUrl}${routePrefixRss}${show.id}`,
      };
    });
};

const handleRequest = async (request) => {
  const url = new URL(request.url);

  if (url.pathname.startsWith(routePrefixRss)) {
    const showId = url.pathname.substr(routePrefixRss.length);
    const showDiffusions = await getShowDiffusions(showId);
    const feed = buildFeed(showDiffusions);
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
};
