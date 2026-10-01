"use strict";

// The setup page: the connection to Telegram, Frigate and MQTT. It is the only
// page of a service not configured yet, then the "Connection" link of the
// interface. Texts follow the same rules as ui.js: T(`English text`), data-t in
// the page (data-t-placeholder for a placeholder).

const TOKEN = "frigate-telegram-enhanced";
const LANG_KEY = "frigate-telegram-enhanced-lang";
const CODES = LANGUAGES.map((l) => l.code);

function initialLang() {
  try {
    const saved = localStorage.getItem(LANG_KEY);
    if (CODES.includes(saved)) return saved;
  } catch (e) { /* storage unavailable: follow the browser */ }
  for (const pref of navigator.languages || [navigator.language || ""]) {
    const code = pref.toLowerCase().split("-")[0];
    if (CODES.includes(code)) return code;
  }
  return "en";
}

let lang = initialLang();
const T = (s) => (CATALOGS[lang] && CATALOGS[lang][s]) || s;
const Tf = (s, ...args) => { let i = 0; return T(s).replace(/%[sd]/g, () => String(args[i++])); };
const $ = (id) => document.getElementById(id);

// el builds an element; children are nodes or texts (never HTML: chat names come from Telegram).
function el(tag, props, ...children) {
  const n = document.createElement(tag);
  for (const [k, v] of Object.entries(props || {})) {
    if (k === "class") n.className = v;
    else if (k.startsWith("on")) n.addEventListener(k.slice(2), v);
    else n.setAttribute(k, v);
  }
  for (const c of children) if (c != null) n.append(c);
  return n;
}

function api(path, opts) {
  opts = Object.assign({}, opts);
  opts.headers = Object.assign({"X-Lang": lang, "X-Requested-With": TOKEN}, opts.body ? {"Content-Type": "application/json"} : {}, opts.headers || {});
  return fetch(path, opts);
}

async function post(path, body, method) {
  const r = await api(path, {method: method || "POST", body: JSON.stringify(body)});
  let data = {};
  try { data = await r.json(); } catch (e) { /* empty answer */ }
  return {ok: r.ok, status: r.status, data};
}

// ---- state ---------------------------------------------------------------------

let first = true;
let saved = {};      // which secrets are saved (an empty field keeps them)
let bot = "";        // @name of the bot, once the token is checked
let chats = [];      // [{id, name, private, on}]
let msgLang = "";    // language of the Telegram messages
let allowedHosts = []; // host names the interface accepts without a password
let pollTimer = null;
let pollUntil = 0;
// What Frigate said of its broker: {state: "ok" | "password" | "error" | "", error,
// address}; null until Frigate is checked.
let broker = null;
let searching = false; // Frigate being looked for on the network

function setResult(id, state, text) {
  const n = $(id);
  n.className = "result" + (state ? " " + state : "");
  n.textContent = text || "";
}

function problem(data) {
  return (data.error || T("Error")) + (data.hint ? " — " + data.hint : "");
}

// ---- language ----------------------------------------------------------------

function applyStatic() {
  document.documentElement.lang = lang;
  document.title = T("frigate-telegram-enhanced — setup");
  for (const n of document.querySelectorAll("[data-t]")) n.textContent = T(n.dataset.t);
  for (const n of document.querySelectorAll("[data-t-placeholder]")) n.placeholder = T(n.dataset.tPlaceholder);
  for (const b of document.querySelectorAll(".lang button")) b.setAttribute("aria-pressed", String(b.dataset.lang === lang));
  $("save").textContent = first ? T("Save and start") : T("Save and restart");
  withLink($("tg-lead"), T("In Telegram, open %s, send /newbot and choose a name: it gives you a token. Paste it here."),
    "https://t.me/BotFather", "@BotFather");
  if (searching) setResult("frigate-result", "", T("Looking for Frigate on your network…"));
  if (!msgLang) $("msg-lang").value = lang; // not chosen: the messages follow the page
  for (const [id, key] of [["token", "token"], ["frigate-pass", "frigate_password"], ["mqtt-pass", "mqtt_password"], ["web-pass", "web_password"]]) {
    if (saved[key]) $(id).placeholder = T("unchanged");
  }
  renderChats();
  renderBroker();
}

// withLink fills node with text, its %s replaced by a link.
function withLink(node, text, href, label) {
  const [before, after] = text.split("%s");
  node.replaceChildren(before, el("a", {href, target: "_blank", rel: "noopener"}, label), after || "");
}

function setLang(l) {
  lang = l;
  try { localStorage.setItem(LANG_KEY, l); } catch (e) { /* not remembered */ }
  applyStatic();
}

function initLang() {
  const box = document.querySelector(".lang");
  for (const l of LANGUAGES) {
    box.append(el("button", {type: "button", "data-lang": l.code, onclick: () => setLang(l.code)}, l.code.toUpperCase()));
    $("msg-lang").append(el("option", {value: l.code}, l.name));
  }
  $("msg-lang").addEventListener("change", () => { msgLang = $("msg-lang").value; });
}

// ---- Telegram --------------------------------------------------------------------

function renderChats() {
  const box = $("chats");
  box.replaceChildren();
  for (const c of chats) {
    const label = (c.private ? "👤 " : "👥 ") + c.name;
    box.append(el("button", {type: "button", class: "chip", "aria-pressed": String(c.on), title: String(c.id),
      onclick: () => { c.on = !c.on; renderChats(); }}, label));
  }
  $("chats-field").hidden = !bot && chats.length === 0;
  $("chats-waiting").hidden = chats.length > 0 || !bot;
  const howto = $("chats-howto");
  howto.replaceChildren();
  if (bot) {
    withLink(howto, T("Send /start to %s from each Telegram account that should receive the notifications. For a group, add the bot to it and send /start there."),
      "https://t.me/" + encodeURIComponent(bot.replace(/^@/, "")), bot);
  }
}

// addChats merges the chats found by the bot. On the first setup they are the
// people setting it up: selected. Later, they are users the bot refused: offered only.
function addChats(found) {
  for (const f of found || []) {
    if (!chats.some((c) => c.id === f.id)) chats.push({id: f.id, name: f.name, private: f.private, on: first});
  }
  renderChats();
}

async function checkToken(quiet) {
  const token = $("token").value.trim();
  if (!token && !saved.token) {
    setResult("token-result", "err", T("Paste the bot token first."));
    return false;
  }
  if (!quiet) setResult("token-result", "", T("Checking…"));
  const r = await post("api/connection/telegram", {token});
  if (!r.ok) {
    setResult("token-result", "err", problem(r.data));
    return false;
  }
  bot = r.data.bot;
  setResult("token-result", "ok", "✓ " + bot);
  addChats(r.data.chats);
  return true;
}

// pollChats asks again who wrote to the bot, for a few minutes, until someone is found.
function pollChats() {
  clearTimeout(pollTimer);
  pollUntil = Date.now() + 5 * 60 * 1000;
  const tick = async () => {
    if (chats.length > 0 || Date.now() > pollUntil) return;
    await checkToken(true);
    pollTimer = setTimeout(tick, 4000);
  };
  pollTimer = setTimeout(tick, 4000);
}

function addManual() {
  const id = Number($("manual-id").value.trim());
  if (!Number.isSafeInteger(id) || id === 0) {
    $("manual-id").focus();
    return;
  }
  const name = $("manual-name").value.trim() || String(id);
  chats = chats.filter((c) => c.id !== id);
  chats.push({id, name, private: id > 0, on: true});
  $("manual-id").value = $("manual-name").value = "";
  renderChats();
}

// ---- Frigate ---------------------------------------------------------------------

function frigateValues() {
  return {
    url: $("frigate-url").value.trim(),
    username: $("frigate-user").value.trim(),
    password: $("frigate-pass").value,
    insecure_skip_verify: $("frigate-insecure").checked,
  };
}

async function checkFrigate() {
  const f = frigateValues();
  if (!f.url) {
    setResult("frigate-result", "err", T("Enter Frigate's address first."));
    return;
  }
  setResult("frigate-result", "", T("Checking…"));
  const r = await post("api/connection/frigate", f);
  if (!r.ok) {
    setResult("frigate-result", "err", problem(r.data));
    return;
  }
  applyFrigate(r.data);
}

// mqttTouched: the MQTT fields were typed in; what Frigate says no longer replaces them.
let mqttTouched = false;

// applyFrigate shows the Frigate found and fills in its broker.
function applyFrigate(found) {
  $("frigate-url").value = found.url;
  setResult("frigate-result", "ok", Tf("✓ Frigate %s", found.version));
  const m = found.mqtt || {};
  if (!m.broker || mqttTouched) return;
  $("mqtt-broker").value = m.broker;
  $("mqtt-user").value = m.username || "";
  $("mqtt-prefix").value = m.topic_prefix || "";
  broker = {state: found.broker_state || "", error: found.broker_error || "", address: m.broker};
  renderBroker();
  if (broker.state === "password") $("mqtt-pass").focus();
}

// discoverFrigate looks for Frigate on the network, for an empty address.
async function discoverFrigate() {
  searching = true;
  setResult("frigate-result", "", T("Looking for Frigate on your network…"));
  const r = await post("api/connection/discover", {});
  searching = false;
  if ($("frigate-url").value.trim()) return; // typed in the meantime
  if (r.ok && r.data.found) {
    applyFrigate(r.data.found);
    return;
  }
  setResult("frigate-result", "err", T("Frigate was not found by itself: enter its address."));
  $("frigate-url").focus();
}

function renderBroker() {
  const box = $("broker-status");
  const b = broker;
  let state = "", text = T("Frigate's broker is filled in once Frigate is found.");
  if (b && b.state === "ok") {
    state = "ok"; text = Tf("✓ Found through Frigate: %s", b.address);
  } else if (b && b.state === "password") {
    state = "warn"; text = Tf("The broker %s asks for a password.", b.address);
  } else if (b && b.state === "error") {
    state = "err"; text = Tf("Frigate's broker does not answer from here (%s). Enter its address below.", b.error);
  } else if (b) {
    text = Tf("Broker read from Frigate: %s", b.address);
  } else if (!first || $("mqtt-broker").value.trim()) {
    text = "";
  }
  box.className = "broker-status result" + (state ? " " + state : "");
  box.textContent = text;
  box.hidden = !text;
  // The password only when it is asked for, or already part of the settings.
  const typedUser = $("mqtt-user").value.trim() && !(b && b.state === "ok");
  $("mqtt-pass-field").hidden = !((b && b.state === "password") || saved.mqtt_password || $("mqtt-pass").value || typedUser);
  if (b && b.state === "error") $("mqtt-edit").open = true;
}

// ---- save --------------------------------------------------------------------

function connection() {
  const named = {};
  for (const c of chats.filter((c) => c.on)) {
    let name = c.name;
    if (named[name] !== undefined) name += " (" + c.id + ")";
    named[name] = c.id;
  }
  return {
    language: msgLang || lang,
    timezone: $("timezone").value.trim(),
    mode: $("mode").value,
    frigate: frigateValues(),
    mqtt: {
      broker: $("mqtt-broker").value.trim(),
      username: $("mqtt-user").value.trim(),
      password: $("mqtt-pass").value,
      topic_prefix: $("mqtt-prefix").value.trim(),
      insecure_skip_verify: false,
    },
    telegram: {token: $("token").value.trim(), admins: [], chats: named},
    web: {password: $("web-pass").value, allowed_hosts: allowedHosts},
  };
}

function renderChecks(checks) {
  const box = $("checks");
  box.replaceChildren();
  const failed = (checks || []).filter((c) => c.state !== "ok");
  box.hidden = failed.length === 0;
  for (const c of failed) {
    box.append(el("div", {class: "health-error"},
      el("b", {}, c.name + " — "), c.detail,
      c.hint ? el("div", {class: "fix"}, c.hint) : null));
  }
  if (failed.length) {
    box.append(el("div", {}, el("button", {type: "button", class: "link danger", onclick: () => save(true)}, T("Save anyway"))));
  }
}

async function save(force) {
  const c = connection();
  if (Object.keys(c.telegram.chats).length === 0) {
    setResult("save-result", "err", T("Choose at least one recipient (step 1)."));
    return;
  }
  $("save").disabled = true;
  setResult("save-result", "", T("Checking the connections…"));
  renderChecks([]);
  const r = await post("api/connection", {connection: c, force: !!force}, "PUT");
  if (r.status === 422) {
    $("save").disabled = false;
    setResult("save-result", "err", T("Some connections don't work yet."));
    renderChecks(r.data.checks);
    return;
  }
  if (!r.ok) {
    $("save").disabled = false;
    setResult("save-result", "err", problem(r.data));
    return;
  }
  clearTimeout(pollTimer);
  setResult("save-result", "ok", T("Saved. Starting…"));
  waitForService();
}

// waitForService waits for the service to answer with its settings, then opens them.
function waitForService() {
  const tick = async () => {
    try {
      const r = await fetch("api/settings", {headers: {"X-Lang": lang}});
      if (r.ok || r.status === 401) {
        location.href = "./";
        return;
      }
    } catch (e) { /* restarting */ }
    setTimeout(tick, 1500);
  };
  setTimeout(tick, 1500);
}

// ---- load --------------------------------------------------------------------

async function load() {
  const r = await api("api/connection");
  if (!r.ok) {
    $("messages").replaceChildren(el("div", {class: "notice err"}, T("Cannot read the connection.")));
    return;
  }
  const d = await r.json();
  first = d.first;
  saved = d.secrets || {};
  const c = d.connection || {};
  $("intro-first").hidden = !first;
  $("intro-edit").hidden = first;
  const f = c.frigate || {}, m = c.mqtt || {}, t = c.telegram || {};
  $("frigate-url").value = f.url || "";
  $("frigate-user").value = f.username || "";
  $("frigate-insecure").checked = !!f.insecure_skip_verify;
  if (f.username || f.insecure_skip_verify) $("frigate-auth").open = true;
  $("mqtt-broker").value = (m.broker || "").replace(/^tcp:\/\//, "");
  $("mqtt-user").value = m.username || "";
  $("mqtt-prefix").value = m.topic_prefix || "";
  $("timezone").value = c.timezone || Intl.DateTimeFormat().resolvedOptions().timeZone || "";
  $("mode").value = c.mode || "events";
  msgLang = c.language || "";
  if (msgLang) $("msg-lang").value = msgLang;
  allowedHosts = (c.web && c.web.allowed_hosts) || [];
  for (const [name, id] of Object.entries(t.chats || {})) chats.push({id, name, private: id > 0, on: true});
  applyStatic();
  if (saved.token) {
    if (await checkToken(true)) pollChats();
  }
  if (!first) $("mqtt-edit").open = true; // changing a connection: the settings in view
  if (!f.url) discoverFrigate();
}

$("check-token").addEventListener("click", async () => { if (await checkToken()) pollChats(); });
$("token").addEventListener("keydown", (e) => { if (e.key === "Enter") $("check-token").click(); });
$("check-frigate").addEventListener("click", checkFrigate);
for (const id of ["mqtt-broker", "mqtt-user", "mqtt-prefix"]) {
  $(id).addEventListener("input", () => { mqttTouched = true; renderBroker(); });
}
// A pasted token is checked right away: no button to look for.
const TOKEN_RE = /^\d{5,}:[\w-]{30,}$/;
$("token").addEventListener("input", async () => {
  if (TOKEN_RE.test($("token").value.trim()) && await checkToken()) pollChats();
});
$("frigate-url").addEventListener("keydown", (e) => { if (e.key === "Enter") checkFrigate(); });
$("manual-add").addEventListener("click", addManual);
$("save").addEventListener("click", () => save(false));
initLang();
applyStatic();
load();
