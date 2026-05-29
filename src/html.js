import { baseUrl } from "./config.js";

export const getHomePageContents = () => {
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
      if (searchResults.length === 0) {
        const noResultsNode = document.createElement("p");
        noResultsNode.innerHTML = "Aucun résultat trouvé pour cette recherche.<br>Êtes-vous sûr que ce podcast existe sur le site de Radio France ?";
        noResultsNode.style.textAlign = "center";
        noResultsNode.style.fontWeight = "bold";
        searchResultsContainer.replaceChildren(noResultsNode);
      } else {
        const nodes = searchResults.map((searchResult) => {
          const node = document.createElement("aside");
          const imgNodeHTML = searchResult['imgUrl'] ? \`<img src="\${searchResult['imgUrl']}" />\` : '<div style="width: 100%;background: lightgray;aspect-ratio: 1/1;"></div>';
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
      }
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

      try {
        const response = await fetch(
          "${baseUrl}/search/?query=" +
            encodeURIComponent(query)
        );
        if (!response.ok) {
          throw new Error(\`Erreur serveur : \${response.status}\`);
        }
        const searchResults = await response.json();
        setSearchResults(searchResults);
      } catch (error) {
        console.error("Erreur lors de la recherche", error);
        alert("Une erreur est survenue lors de la recherche. Veuillez réessayer.");
      }
    };
    form.addEventListener("submit", handleSubmit);
  </script>
</html>
`;
};
