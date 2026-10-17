// Calls to the service: the page's language and the CSRF token on every request,
// and the login page when the session ended.
export const TOKEN = "frigate-telegram-enhanced";

export function headers(lang, hasBody) {
  return { "X-Lang": lang, "X-Requested-With": TOKEN, ...(hasBody ? { "Content-Type": "application/json" } : {}) };
}

const toLogin = (url) => { location.href = url; };

export async function api(path, opts = {}, { lang, next = "setup", fetch: f = fetch, onUnauthorized = toLogin } = {}) {
  const resp = await f(path, { ...opts, headers: { ...headers(lang, opts.body !== undefined), ...(opts.headers || {}) } });
  if (resp.status === 401) onUnauthorized("login?next=" + next + "&lang=" + lang);
  return resp;
}

export async function post(path, body, { method = "POST", ...ctx } = {}) {
  const r = await api(path, { method, body: JSON.stringify(body) }, ctx);
  let data = {};
  try { data = await r.json(); } catch { /* empty answer */ }
  return { ok: r.ok, status: r.status, data };
}

// problem is an error answer in words: the error, then how to fix it.
export function problem(data, T) {
  return (data.error || T("Error")) + (data.hint ? " — " + data.hint : "");
}
