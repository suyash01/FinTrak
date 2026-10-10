import { describe, it, expect } from "vitest";
import { describeUserAgent } from "./userAgent";

const CHROME_WINDOWS =
  "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36";
const SAFARI_IPHONE =
  "Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Mobile/15E148 Safari/604.1";

describe("describeUserAgent", () => {
  it("names a browser and a platform", () => {
    expect(describeUserAgent(CHROME_WINDOWS)).toBe("Chrome on Windows");
    expect(describeUserAgent(SAFARI_IPHONE)).toBe("Safari on iPhone");
  });

  it("says Unknown device for a session with no user agent", () => {
    // Migration 000002 backfilled a row for every refresh family that predates
    // the sessions table, and those rows carry no user agent at all. A security
    // panel must still name the row rather than render it blank.
    expect(describeUserAgent("")).toBe("Unknown device");
    expect(describeUserAgent("   ")).toBe("Unknown device");
  });

  it("returns the raw string when it recognises neither half", () => {
    // A string it cannot parse is still evidence — showing it beats showing
    // nothing, and the row stays individually revokable either way.
    expect(describeUserAgent("curl/8.4.0")).toBe("curl/8.4.0");
  });

  it("prefers the most specific browser token", () => {
    // Chrome's and Edge's user agents both end in "Safari/537.36", and every
    // Chromium one contains "Chrome", so a table that put Safari or Chrome
    // first would label every desktop browser Safari.
    expect(
      describeUserAgent(
        "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36 Edg/141.0.0.0",
      ),
    ).toBe("Edge on Windows");
    expect(describeUserAgent(CHROME_WINDOWS)).toBe("Chrome on Windows");
    // iOS names its browsers CriOS/FxiOS and never "Chrome"/"Firefox".
    expect(
      describeUserAgent(
        "Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/141.0.0.0 Mobile/15E148 Safari/604.1",
      ),
    ).toBe("Chrome on iPhone");
  });

  it("names whichever half it recognises", () => {
    expect(
      describeUserAgent("Mozilla/5.0 (X11; Linux x86_64; rv:142.0) Firefox/142.0"),
    ).toBe("Firefox on Linux");
    expect(describeUserAgent("Mozilla/5.0 (Linux; Android 15)")).toBe("Android");
  });
});
