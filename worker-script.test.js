import { describe, it, expect, vi, beforeEach } from "vitest";
import { getImgUrl, buildFeed } from "./src/feed.js";
import { getSearchResults } from "./src/api.js";
import { handleRequest } from "./worker-script.js";
import show0b91efaf from "./fixtures/api-show-0b91efaf.json" with { type: "json" };
import show4a41823f from "./fixtures/api-show-4a41823f.json" with { type: "json" };
import searchFixture from "./fixtures/api-search-response.json" with { type: "json" };

// ── getImgUrl ──────────────────────────────────────────────────────────────────

describe("getImgUrl", () => {
  it("returns null when visuals is empty and no fallback", () => {
    expect(getImgUrl([], null)).toBeNull();
    expect(getImgUrl(null, null)).toBeNull();
  });

  it("uses fallback id when visuals is empty", () => {
    const url = getImgUrl([], "fallback-uuid");
    expect(url).toBe(
      "https://api.radiofrance.fr/v1/services/embed/image/fallback-uuid?preset=568x568"
    );
  });

  it("prefers square_banner over other visuals", () => {
    const visuals = [
      { name: "square_visual", visual_uuid: "uuid-square" },
      { name: "square_banner", visual_uuid: "uuid-banner" },
    ];
    expect(getImgUrl(visuals, null)).toContain("uuid-banner");
  });

  it("falls back to square_visual when no square_banner", () => {
    const visuals = [
      { name: "square_visual", visual_uuid: "uuid-square" },
      { name: "other", visual_uuid: "uuid-other" },
    ];
    expect(getImgUrl(visuals, null)).toContain("uuid-square");
  });

  it("falls back to first visual when no named match", () => {
    const visuals = [{ name: "some_other", visual_uuid: "uuid-first" }];
    expect(getImgUrl(visuals, null)).toContain("uuid-first");
  });
});

// ── buildFeed helpers ─────────────────────────────────────────────────────────

/**
 * Simulate what getShowDiffusions returns, given raw API fixture data.
 * @param {object} fixture - raw API response
 * @param {string} showId
 */
function fixtureToShowDiffusions(fixture, showId) {
  const diffusions = fixture.data.map((item) => item.diffusions);
  const manifestations = {};
  for (const k in fixture.included.manifestations) {
    manifestations[k] = fixture.included.manifestations[k];
  }
  return {
    diffusions,
    showDetails: fixture.included.shows[showId],
    manifestations,
  };
}

// ── buildFeed ─────────────────────────────────────────────────────────────────

describe("buildFeed", () => {
  describe("Affaires sensibles (0b91efaf) — paginated", () => {
    const showDiffusions = fixtureToShowDiffusions(
      show0b91efaf,
      "0b91efaf-26e6-11e4-907f-782bcb6744eb"
    );

    it("produces valid XML with correct root element", () => {
      const xml = buildFeed(showDiffusions, null);
      expect(xml).toContain('<?xml version="1.0" encoding="UTF-8"?>');
      expect(xml).toContain("<rss");
      expect(xml).toContain("</rss>");
    });

    it("includes the correct show title", () => {
      const xml = buildFeed(showDiffusions, null);
      expect(xml).toContain("<title>Affaires sensibles</title>");
    });

    it("includes items for all diffusions in the page", () => {
      const xml = buildFeed(showDiffusions, null);
      const itemCount = (xml.match(/<item>/g) || []).length;
      expect(itemCount).toBe(showDiffusions.diffusions.length);
    });

    it("includes the first diffusion title (with unicode)", () => {
      const xml = buildFeed(showDiffusions, null);
      // "25 juin 1977, la première Marche des fiertés LGBT+" — é and + must survive
      expect(xml).toContain("25 juin 1977");
      expect(xml).toContain("premi");
      expect(xml).toContain("LGBT+");
    });

    it("includes an audio enclosure for each item", () => {
      const xml = buildFeed(showDiffusions, null);
      expect(xml).toContain('type="audio/mpeg"');
    });

    it("includes atom:link next-page tag when nextPageUrl is provided", () => {
      const xml = buildFeed(
        showDiffusions,
        "https://radio-france-rss.aerion.workers.dev/rss/0b91efaf-26e6-11e4-907f-782bcb6744eb?page=1"
      );
      expect(xml).toContain('rel="next"');
      expect(xml).toContain("page=1");
    });

    it("does not include atom:link when nextPageUrl is null", () => {
      const xml = buildFeed(showDiffusions, null);
      expect(xml).not.toContain('rel="next"');
    });
  });

  describe("Espions, une histoire vraie (4a41823f) — single page", () => {
    const showDiffusions = fixtureToShowDiffusions(
      show4a41823f,
      "4a41823f-f1f7-4725-8380-e428893eb93b"
    );

    it("includes the correct show title", () => {
      const xml = buildFeed(showDiffusions, null);
      expect(xml).toContain("<title>Espions, une histoire vraie</title>");
    });

    it("does not include atom:link (no next page)", () => {
      const xml = buildFeed(showDiffusions, null);
      expect(xml).not.toContain('rel="next"');
    });

    it("includes items for diffusions that have audio (some may lack a manifestation)", () => {
      const xml = buildFeed(showDiffusions, null);
      const itemCount = (xml.match(/<item>/g) || []).length;
      expect(itemCount).toBeGreaterThan(0);
      expect(itemCount).toBeLessThanOrEqual(showDiffusions.diffusions.length);
    });
  });

  describe("XML escaping", () => {
    it("escapes special characters in show title", () => {
      const showDiffusions = fixtureToShowDiffusions(
        show4a41823f,
        "4a41823f-f1f7-4725-8380-e428893eb93b"
      );
      showDiffusions.showDetails = {
        ...showDiffusions.showDetails,
        title: 'Le Podcast <Super> & "Cool"',
      };
      const xml = buildFeed(showDiffusions, null);
      expect(xml).toContain("&lt;Super&gt;");
      expect(xml).toContain("&amp;");
      expect(xml).toContain("&quot;Cool&quot;");
    });
  });
});

// ── getSearchResults ──────────────────────────────────────────────────────────

describe("getSearchResults", () => {
  beforeEach(() => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: true,
        json: () => Promise.resolve(searchFixture),
      })
    );
  });

  it("returns only show results, ignoring other models", async () => {
    const results = await getSearchResults("affaires sensibles");
    const expectedShowCount = searchFixture.data.filter(
      (i) => i.resultItems?.model === "show"
    ).length;
    expect(results).toHaveLength(expectedShowCount);
  });

  it("first result is Affaires sensibles", async () => {
    const results = await getSearchResults("affaires sensibles");
    expect(results[0].title).toBe("Affaires sensibles");
  });

  it("includes rssUrl, path, standfirst, imgUrl in results", async () => {
    const results = await getSearchResults("affaires sensibles");
    expect(results[0]).toMatchObject({
      title: expect.any(String),
      path: expect.stringContaining("radiofrance.fr"),
      standfirst: expect.any(String),
      rssUrl: expect.stringMatching(/\/rss\/0b91efaf/),
    });
  });

  it("returns empty array when no show results", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ data: [], included: { shows: {} } }),
      })
    );
    const results = await getSearchResults("rien");
    expect(results).toEqual([]);
  });

  it("throws when the API returns an error status", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({ ok: false, status: 401, statusText: "Unauthorized" })
    );
    await expect(getSearchResults("test")).rejects.toThrow("Radio France API error: 401");
  });
});

// ── handleRequest router ──────────────────────────────────────────────────────

describe("handleRequest", () => {
  it("returns 404 for unknown routes", async () => {
    const response = await handleRequest(new Request("https://example.com/unknown"));
    expect(response.status).toBe(404);
  });

  it("returns HTML for the homepage", async () => {
    const response = await handleRequest(new Request("https://example.com/"));
    expect(response.status).toBe(200);
    expect(response.headers.get("Content-Type")).toContain("text/html");
    const body = await response.text();
    expect(body).toContain("RSS Radio France pour tous");
  });

  it("returns 500 when the upstream API fails on the RSS route", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({ ok: false, status: 503, statusText: "Service Unavailable" })
    );
    const response = await handleRequest(
      new Request("https://example.com/rss/0b91efaf-26e6-11e4-907f-782bcb6744eb")
    );
    expect(response.status).toBe(500);
  });
});
