import { baseUrl, routePrefixAudio } from "./config.js";

/**
 * @param {Array<{name: string, visual_uuid: string}>|null} visuals
 * @param {string|null} fallbackImgId - visual_uuid used when visuals is empty
 * @returns {string|null}
 */
export const getImgUrl = (visuals, fallbackImgId) => {
  let chosenId;
  if (!visuals || visuals.length === 0) {
    chosenId = fallbackImgId;
  } else {
    const visualsMap = visuals.reduce((res, item) => {
      res[item.name] = item.visual_uuid;
      return res;
    }, {});
    chosenId = visualsMap["square_banner"] ?? visualsMap["square_visual"] ?? visuals[0].visual_uuid;
  }

  if (!chosenId) {
    return null;
  }

  return `https://api.radiofrance.fr/v1/services/embed/image/${chosenId}?preset=568x568`;
};

/**
 * @param {{diffusions: object[], showDetails: object}} showData
 * @param {string|URL|null} nextPageUrl - URL for the atom:link next-page tag, or null
 * @returns {string} RSS XML feed
 */
export const buildFeed = ({ diffusions, showDetails }, nextPageUrl) => {
  const buildElement = (name, innerText) => {
    return innerText ? `<${name}>${innerText}</${name}>` : "";
  };
  const buildImgElement = (url) => {
    return url ? `<itunes:image href="${url}"/>` : "";
  };

  const escapeXml = (unsafe) => {
    if (unsafe == null) return unsafe;

    let escaped;
    // From https://stackoverflow.com/a/27979933
    escaped = unsafe.replace(/[<>&'"]/g, function (c) {
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

    return removeXMLInvalidChars(escaped, true);
  };

  /**
   * Removes invalid XML characters from a string
   * @param {string} str - a string containing potentially invalid XML characters (non-UTF8 characters, STX, EOX etc)
   * @param {boolean} removeDiscouragedChars - should it remove discouraged but valid XML characters
   * @return {string} a sanitized string stripped of invalid XML characters
   */
  // https://gist.github.com/john-doherty/b9195065884cdbfd2017a4756e6409cc
  function removeXMLInvalidChars(str, removeDiscouragedChars) {
    // remove everything forbidden by XML 1.0 specifications, plus the unicode replacement character U+FFFD
    var regex =
      /((?:[\0-\x08\x0B\f\x0E-\x1F�￾￿]|[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?:[^\uD800-\uDBFF]|^)[\uDC00-\uDFFF]))/g; // eslint-disable-line no-control-regex

    // ensure we have a string
    str = String(str || "").replace(regex, "");

    if (removeDiscouragedChars) {
      // remove everything discouraged by XML 1.0 specifications
      regex = new RegExp(
        "([\\x7F-\\x84]|[\\x86-\\x9F]|[\\uFDD0-\\uFDEF]|(?:\\uD83F[\\uDFFE\\uDFFF])|(?:\\uD87F[\\uDF" +
          "FE\\uDFFF])|(?:\\uD8BF[\\uDFFE\\uDFFF])|(?:\\uD8FF[\\uDFFE\\uDFFF])|(?:\\uD93F[\\uDFFE\\uD" +
          "FFF])|(?:\\uD97F[\\uDFFE\\uDFFF])|(?:\\uD9BF[\\uDFFE\\uDFFF])|(?:\\uD9FF[\\uDFFE\\uDFFF])" +
          "|(?:\\uDA3F[\\uDFFE\\uDFFF])|(?:\\uDA7F[\\uDFFE\\uDFFF])|(?:\\uDABF[\\uDFFE\\uDFFF])|(?:\\" +
          "uDAFF[\\uDFFE\\uDFFF])|(?:\\uDB3F[\\uDFFE\\uDFFF])|(?:\\uDB7F[\\uDFFE\\uDFFF])|(?:\\uDBBF" +
          "[\\uDFFE\\uDFFF])|(?:\\uDBFF[\\uDFFE\\uDFFF])(?:[\\0-\\t\\x0B\\f\\x0E-\\u2027\\u202A-\\uD7FF\\" +
          "uE000-\\uFFFF]|[\\uD800-\\uDBFF][\\uDC00-\\uDFFF]|[\\uD800-\\uDBFF](?![\\uDC00-\\uDFFF])|" +
          "(?:[^\\uD800-\\uDBFF]|^)[\\uDC00-\\uDFFF]))",
        "g"
      );

      str = str.replace(regex, "");
    }

    return str;
  }

  const buildItem = (diffusion) => {
    const manifestationId = diffusion.relationships?.manifestations?.[0];
    if (!manifestationId) {
      console.log(`Item ${diffusion.id} visible at ${diffusion.path} has no mp3 version, skipping`);
      return "";
    }

    if (diffusion.createdTime * 1000 > Date.now()) {
      // Radio France pre-schedules rerun slots (e.g. summer replacement
      // programming) weeks ahead in its CMS, and createdTime on those
      // diffusions is the future slot's own timestamp rather than a past
      // broadcast date. A future-dated pubDate confuses podcast clients
      // (observed: AntennaPod displaying the current date instead of the
      // real, future one) - simplest fix is to not publish the episode
      // until its scheduled time has actually passed.
      console.log(`Item ${diffusion.id} is scheduled in the future (createdTime=${diffusion.createdTime}), skipping`);
      return "";
    }

    let guid = diffusion.id;
    if (new Date(diffusion.createdTime * 1000) <= new Date("Sep 12 2022")) {
      // backward compatibility: keep old id generation.
      guid = manifestationId;
    }

    const description = diffusion.standfirst ?? diffusion.bodyMarkdown ?? "";
    const enclosureUrl = `${baseUrl}${routePrefixAudio}${manifestationId}`;

    const imgUrl = getImgUrl(diffusion.visuals, diffusion.mainImage);
    return `    <item>
          <title>${escapeXml(diffusion.title)}</title>
          <guid>${guid}</guid>
          ${buildElement("link", diffusion.path)}
          <description>${escapeXml(description)}</description>
          <enclosure url="${escapeXml(enclosureUrl)}" type="audio/mpeg" />
          <pubDate>${new Date(diffusion.createdTime * 1000).toUTCString()}</pubDate>
          ${buildImgElement(imgUrl)}
        </item>`;
  };

  const nextPageXmlTag = nextPageUrl
    ? `
  <atom:link href="${nextPageUrl}" rel="next" />`
    : "";

  const imgUrl = getImgUrl(showDetails.visuals, showDetails.mainImage);
  return `<?xml version="1.0" encoding="UTF-8"?>
    <rss xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd" xmlns:pa="http://podcastaddict.com" xmlns:podcastRF="http://radiofrance.fr/Lancelot/Podcast#" xmlns:googleplay="http://www.google.com/schemas/play-podcasts/1.0" version="2.0" xmlns:atom="http://www.w3.org/2005/Atom">
      <channel>
        <title>${escapeXml(showDetails.title)}</title>
        <link>${showDetails.path}</link>
        ${nextPageXmlTag}
        <description>${escapeXml(showDetails.standfirst)}</description>
        ${buildImgElement(imgUrl)}
    ${diffusions.map(buildItem).join("\n")}
      </channel>
    </rss>`;
};
