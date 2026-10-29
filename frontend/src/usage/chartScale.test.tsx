/** Hand-calculated chart scale cases for the desktop copy of the private upstream algorithm. */
import { describe, expect, it } from "vitest";
import { niceScale } from "../../vendor/UsageProviderChart";

describe("usage chart scale", () => {
  it("rounds the peak up to a human-readable step", () => {
    expect(niceScale(11, 4)).toEqual({ max: 15, ticks: [0, 5, 10, 15] });
  });
  it("keeps an empty chart at a single zero tick", () => {
    expect(niceScale(0, 4)).toEqual({ max: 0, ticks: [0] });
  });
});
