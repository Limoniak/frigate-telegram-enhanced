// web/src/lib/api.test.js
import { describe, expect, it } from "vitest";
import { TOKEN, api, headers, post, problem } from "./api.js";

const answer = (status, body) => async () => new Response(body === undefined ? null : JSON.stringify(body), { status });

describe("headers", () => {
  it("always sends the language and the CSRF token, and the JSON type with a body", () => {
    expect(headers("fr", false)).toEqual({ "X-Lang": "fr", "X-Requested-With": TOKEN });
    expect(headers("de", true)["Content-Type"]).toBe("application/json");
  });
});

describe("api", () => {
  it("sends the headers", async () => {
    let seen;
    const fetch = async (_path, opts) => { seen = opts; return new Response("{}"); };
    await api("api/connection", {}, { lang: "fr", fetch });
    expect(seen.headers["X-Lang"]).toBe("fr");
  });
  it("leads to the login page when the session ended", async () => {
    let to = "";
    await api("api/connection", {}, { lang: "de", fetch: answer(401), onUnauthorized: (url) => { to = url; } });
    expect(to).toBe("login?next=setup&lang=de");
  });
  it("leads back to the page the session ended on", async () => {
    let to = "";
    await api("api/settings", {}, { lang: "fr", next: "", fetch: answer(401), onUnauthorized: (url) => { to = url; } });
    expect(to).toBe("login?next=&lang=fr");
  });
});

describe("post", () => {
  it("returns the JSON answer, or an empty object", async () => {
    expect(await post("api/x", { a: 1 }, { lang: "en", fetch: answer(422, { checks: [] }) }))
      .toEqual({ ok: false, status: 422, data: { checks: [] } });
    expect((await post("api/x", {}, { lang: "en", fetch: answer(204) })).data).toEqual({});
  });
});

describe("problem", () => {
  const T = (s) => s;
  it("joins the error and its hint", () => {
    expect(problem({ error: "refused", hint: "check the token" }, T)).toBe("refused — check the token");
    expect(problem({}, T)).toBe("Error");
  });
});
