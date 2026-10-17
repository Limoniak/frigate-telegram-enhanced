import { describe, expect, it } from "vitest";
import { parseRoute, routeHash } from "./route.js";

describe("parseRoute", () => {
  it("defaults to the overview", () => {
    expect(parseRoute("")).toEqual({ screen: "overview", camera: null });
    expect(parseRoute("#nope")).toEqual({ screen: "overview", camera: null });
  });
  it("knows the screens", () => {
    expect(parseRoute("#when")).toEqual({ screen: "when", camera: null });
    expect(parseRoute("#recipients").screen).toBe("recipients");
  });
  it("decodes the camera of #cameras/", () => {
    expect(parseRoute("#cameras/porte%20d'entr%C3%A9e")).toEqual({ screen: "cameras", camera: "porte d'entrée" });
  });
  it("drops an invalid camera sequence", () => {
    expect(parseRoute("#cameras/%E0")).toEqual({ screen: "cameras", camera: null });
  });
});

describe("routeHash", () => {
  it("is the inverse of parseRoute", () => {
    expect(routeHash({ screen: "cameras", camera: "a b" })).toBe("#cameras/a%20b");
    expect(routeHash({ screen: "when", camera: null })).toBe("#when");
  });
});
