// describeUserAgent turns the raw User-Agent a session recorded into the two
// facts a person recognises: which browser, which machine.
//
// It is a substring table and nothing more. A user agent is a display string
// this app only ever renders once, on one screen, for the user's own rows — the
// worst case of a parser dependency in the whole repo. Every entry below is
// ordered most-specific-first, because the format is nesting rather than
// priority: Edge's string contains "Chrome" and "Safari", Chrome's contains
// "Safari", Firefox on iOS is "FxiOS" and never "Firefox". First match wins, so
// putting Safari first would label every desktop browser Safari.

// [needle, name], in match order — see the note above on why the order is the
// whole design.
const BROWSERS: [string, string][] = [
  ["Edg/", "Edge"],
  ["OPR/", "Opera"],
  ["SamsungBrowser", "Samsung Internet"],
  ["CriOS", "Chrome"],
  ["FxiOS", "Firefox"],
  ["Chrome", "Chrome"],
  ["Firefox", "Firefox"],
  ["Safari", "Safari"],
];

// Order matters here too: an iPad's user agent says "Macintosh", and an Android
// one says "Linux", so the two that are substrings of a later entry's needle
// have to be asked about first.
const PLATFORMS: [string, string][] = [
  ["iPhone", "iPhone"],
  ["iPad", "iPad"],
  ["Android", "Android"],
  ["Windows", "Windows"],
  ["Macintosh", "Mac"],
  ["Linux", "Linux"],
];

// Unknown device is what a row with no user agent renders. It is not a rare
// path: migration 000002 backfilled a session for every refresh family that
// predates the sessions table, and those rows carry none.
const UNKNOWN = "Unknown device";

// describeUserAgent names the browser and platform in a session's user agent.
//
// It returns the raw string when it recognises neither half rather than
// Unknown device: an unrecognised agent is still evidence the user can compare
// against, and a blank or generic label would throw it away. Only a genuinely
// absent string is Unknown device.
export function describeUserAgent(ua: string): string {
  if (ua.trim() === "") return UNKNOWN;
  const browser = BROWSERS.find(([needle]) => ua.includes(needle))?.[1];
  const platform = PLATFORMS.find(([needle]) => ua.includes(needle))?.[1];
  if (browser && platform) return `${browser} on ${platform}`;
  return browser ?? platform ?? ua;
}
