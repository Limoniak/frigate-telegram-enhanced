import { describe, expect, it } from "vitest";
import { parseRoute, routeHash } from "./route.js";

describe("parseRoute", () => {
  it("defaults to the overview", () => {
    expect(parseRoute("")).toEqual({ screen: "overview", camera: null, hour: null });
    expect(parseRoute("#nope")).toEqual({ screen: "overview", camera: null, hour: null });
  });
  it("knows the screens", () => {
    expect(parseRoute("#when")).toEqual({ screen: "when", camera: null, hour: null });
    expect(parseRoute("#recipients").screen).toBe("recipients");
  });
  it("decodes the camera of #cameras/", () => {
    expect(parseRoute("#cameras/porte%20d'entr%C3%A9e")).toEqual({ screen: "cameras", camera: "porte d'entrée", hour: null });
  });
  it("reads the hour of #activity/", () => {
    expect(parseRoute("#activity/1760900400")).toEqual({ screen: "activity", camera: null, hour: 1760900400 });
    expect(parseRoute("#activity").hour).toBeNull();
    expect(parseRoute("#activity/abc").hour).toBeNull();
    expect(parseRoute("#activity/-5").hour).toBeNull();
    expect(parseRoute("#cameras/1760900400").hour).toBeNull();
  });
  it("drops an invalid camera sequence", () => {
    expect(parseRoute("#cameras/%E0")).toEqual({ screen: "cameras", camera: null, hour: null });
  });
});

describe("routeHash", () => {
  it("is the inverse of parseRoute", () => {
    expect(routeHash({ screen: "cameras", camera: "a b" })).toBe("#cameras/a%20b");
    expect(routeHash({ screen: "when", camera: null })).toBe("#when");
    expect(routeHash({ screen: "activity", hour: 1760900400 })).toBe("#activity/1760900400");
  });
});
