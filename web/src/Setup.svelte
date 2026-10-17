<script>
  // The setup page: the connection to Telegram, Frigate and MQTT. It is the only page
  // of a service not configured yet, then the "Connection" link of the interface.
  // Element ids are the ones the browser tests read: keep them.
  import { onDestroy, onMount, tick } from "svelte";
  import { LANGUAGES, T, Tf, i18n } from "./lib/i18n.svelte.js";
  import { api as rawApi, post as rawPost, problem as rawProblem } from "./lib/api.js";
  import Card from "./lib/Card.svelte";
  import LangMenu from "./lib/LangMenu.svelte";
  import WithLink from "./lib/WithLink.svelte";

  const api = (path, opts) => rawApi(path, opts, { lang: i18n.lang });
  const post = (path, body, method) => rawPost(path, body, { lang: i18n.lang, method });
  const problem = (data) => rawProblem(data, T);

  const TOKEN_RE = /^\d{5,}:[\w-]{30,}$/; // a pasted token is checked right away
  const POLL_EVERY = 4000, POLL_FOR = 30 * 60 * 1000;

  // ---- state ----
  let first = $state(null); // null until api/connection answers
  let saved = $state({}); // which secrets are saved (an empty field keeps them)
  let bot = $state(""); // @name of the bot, once the token is checked
  let chats = $state([]); // [{id, name, private, on}]
  let msgLang = $state(""); // language of the Telegram messages, "" = follows the page
  let allowedHosts = []; // host names the interface accepts without a password
  // What Frigate said of its broker: {state: "ok" | "password" | "error" | "", error, address}.
  let broker = $state(null);
  let searching = $state(false);
  let mqttTouched = false; // the MQTT fields were typed in: Frigate no longer replaces them
  let loaded = { frigate: "", broker: "" }; // the saved addresses
  let loadFailed = $state(false);
  let saving = $state(false);
  let checks = $state([]);
  let pollTimer, pollUntil = 0;

  let token = $state(""), frigateUrl = $state(""), frigateUser = $state(""), frigatePass = $state("");
  let frigateInsecure = $state(false);
  let mqttBroker = $state(""), mqttUser = $state(""), mqttPass = $state(""), mqttPrefix = $state("");
  let webPass = $state(""), timezone = $state(""), mode = $state("reviews");
  let manualId = $state(""), manualName = $state("");
  let clear = $state({ frigate_password: false, mqtt_password: false, web_password: false });
  let manualOpen = $state(false), frigateAuthOpen = $state(false), mqttEditOpen = $state(false);
  let manualIdEl, frigateUrlEl, mqttBrokerEl, mqttPassEl;

  const idle = () => ({ state: "", text: "" });
  let res = $state({ token: idle(), manual: idle(), frigate: idle(), mqtt: idle(), save: idle() });
  const set = (key, state, text) => { res[key] = { state, text }; };

  $effect(() => {
    document.documentElement.lang = i18n.lang;
    document.title = T("frigate-telegram-enhanced — setup");
  });
  // A password being removed empties its field.
  $effect(() => { if (clear.frigate_password) frigatePass = ""; });
  $effect(() => { if (clear.mqtt_password) mqttPass = ""; });
  $effect(() => { if (clear.web_password) webPass = ""; });
  // Nobody found yet: the ID by hand in view.
  $effect(() => { if (bot && chats.length === 0) manualOpen = true; });
  $effect(() => { if (broker && broker.state === "error") mqttEditOpen = true; });

  // ---- saved passwords: kept by an empty field, for the address they were saved with ----
  const normURL = (u) => u.trim().toLowerCase().replace(/\/+$/, "");
  function normBroker(b) {
    b = b.trim().toLowerCase().replace(/^tcp:\/\//, "");
    return b && !/:\d+$/.test(b) ? b + ":1883" : b;
  }
  function keepsSaved(key) {
    if (!saved[key] || clear[key]) return false;
    if (key === "frigate_password") return normURL(frigateUrl) === loaded.frigate;
    if (key === "mqtt_password") return normBroker(mqttBroker) === loaded.broker;
    return true;
  }
  const clears = () => Object.keys(clear).filter((k) => clear[k]);
  const clearQuery = (key) => (clears().includes(key) ? "?clear=" + key : "");

  // ---- Telegram ----
  function addChats(found) {
    // On the first setup they are the people setting it up: selected. Later, users the bot refused: offered.
    for (const f of found || []) {
      if (!chats.some((c) => c.id === f.id)) chats.push({ id: f.id, name: f.name, private: f.private, on: first !== false });
    }
  }

  async function checkToken(quiet) {
    if (!token.trim() && !saved.token) {
      set("token", "err", T("Paste the bot token first."));
      return false;
    }
    if (!quiet) set("token", "", T("Checking…"));
    const r = await post("api/connection/telegram", { token: token.trim() });
    if (!r.ok) {
      set("token", "err", problem(r.data));
      return false;
    }
    bot = r.data.bot;
    set("token", "ok", "✓ " + bot);
    addChats(r.data.chats);
    return true;
  }

  // pollChats asks again who wrote to the bot while the page is open.
  function pollChats() {
    clearTimeout(pollTimer);
    pollUntil = Date.now() + POLL_FOR;
    const next = async () => {
      if (Date.now() > pollUntil) return;
      if (!document.hidden) await checkToken(true);
      pollTimer = setTimeout(next, POLL_EVERY);
    };
    pollTimer = setTimeout(next, POLL_EVERY);
  }

  async function checkTokenAndPoll() {
    if (await checkToken()) pollChats();
  }

  function addManual() {
    const raw = manualId.replace(/\s/g, "");
    const id = Number(raw);
    if (!/^-?\d+$/.test(raw) || !Number.isSafeInteger(id) || id === 0) {
      set("manual", "err", T("Enter a number: a person's ID is positive, a group's is negative."));
      manualIdEl?.focus();
      return;
    }
    const name = manualName.trim() || String(id);
    chats = chats.filter((c) => c.id !== id);
    chats.push({ id, name, private: id > 0, on: true });
    manualId = manualName = "";
    set("manual", "ok", Tf("✓ %s added: save to apply.", name));
  }

  // ---- Frigate ----
  const frigateValues = () => ({
    url: frigateUrl.trim(), username: frigateUser.trim(), password: frigatePass, insecure_skip_verify: frigateInsecure,
  });

  async function checkFrigate() {
    searching = false; // a manual check takes over from the automatic search
    const f = frigateValues();
    if (!f.url) {
      set("frigate", "err", T("Enter Frigate's address first."));
      return;
    }
    set("frigate", "", T("Checking…"));
    const r = await post("api/connection/frigate" + clearQuery("frigate_password"), f);
    if (!r.ok) {
      set("frigate", "err", problem(r.data));
      return;
    }
    await applyFrigate(r.data);
  }

  // applyFrigate shows the Frigate found and fills in its broker.
  async function applyFrigate(found) {
    frigateUrl = found.url;
    set("frigate", "ok", Tf("✓ Frigate %s", found.version));
    const m = found.mqtt || {};
    if (!m.broker || mqttTouched) return;
    mqttBroker = m.broker;
    mqttUser = m.username || "";
    mqttPrefix = m.topic_prefix || "";
    broker = { state: found.broker_state || "", error: found.broker_error || "", address: m.broker };
    if (broker.state === "password") {
      await tick();
      mqttPassEl?.focus();
    }
  }

  async function discoverFrigate() {
    searching = true;
    const r = await post("api/connection/discover", {});
    searching = false;
    if (frigateUrl.trim()) return; // typed in the meantime
    if (r.ok && r.data.found) {
      await applyFrigate(r.data.found);
      return;
    }
    set("frigate", "err", T("Frigate was not found by itself: enter its address."));
    if (document.activeElement === document.body) frigateUrlEl?.focus(); // never steal it from a field being typed in
  }

  // ---- MQTT ----
  const brokerStatus = $derived.by(() => {
    const b = broker;
    if (b && b.state === "ok") return { state: "ok", text: Tf("✓ Found through Frigate: %s", b.address) };
    if (b && b.state === "password") return { state: "warn", text: Tf("The broker %s asks for a password.", b.address) };
    if (b && b.state === "error") return { state: "err", text: Tf("Frigate's broker does not answer from here (%s). Enter its address below.", b.error) };
    if (b) return { state: "", text: Tf("Broker read from Frigate: %s", b.address) };
    if (first === false || mqttBroker.trim()) return { state: "", text: "" };
    return { state: "", text: T("Frigate's broker is filled in once Frigate is found.") };
  });
  // The password only when it is asked for, or already part of the settings.
  const mqttPassShown = $derived(
    (broker && broker.state === "password") || !!saved.mqtt_password || mqttPass !== "" ||
      (mqttUser.trim() !== "" && !(broker && broker.state === "ok")),
  );

  // The broker is read from its field: a value set without an input event (as the browser
  // tests do) counts, as it did before the page used bound state.
  const mqttValues = () => ({
    broker: (mqttBrokerEl ? mqttBrokerEl.value : mqttBroker).trim(), username: mqttUser.trim(), password: mqttPass, topic_prefix: mqttPrefix.trim(), insecure_skip_verify: false,
  });

  function mqttEdited() {
    mqttTouched = true;
    set("mqtt", "", "");
  }

  async function checkMqtt() {
    const m = mqttValues();
    if (!m.broker) {
      set("mqtt", "err", T("Enter the broker address first."));
      mqttEditOpen = true;
      await tick();
      mqttBrokerEl?.focus();
      return;
    }
    set("mqtt", "", T("Checking…"));
    const r = await post("api/connection/mqtt" + clearQuery("mqtt_password"), m);
    if (!r.ok) {
      set("mqtt", "err", problem(r.data));
      return;
    }
    set("mqtt", "ok", Tf("✓ The broker %s accepts the connection.", r.data.broker.replace(/^tcp:\/\//, "")));
  }

  // ---- save ----
  function connection() {
    const named = {};
    for (const c of chats.filter((c) => c.on)) {
      let name = c.name;
      if (named[name] !== undefined) name += " (" + c.id + ")";
      named[name] = c.id;
    }
    return {
      language: msgLang || i18n.lang,
      timezone: timezone.trim(),
      mode,
      frigate: frigateValues(),
      mqtt: mqttValues(),
      telegram: { token: token.trim(), admins: [], chats: named },
      web: { password: webPass, allowed_hosts: allowedHosts },
    };
  }

  const failed = $derived(checks.filter((c) => c.state !== "ok"));

  async function save(force) {
    const c = connection();
    if (Object.keys(c.telegram.chats).length === 0) {
      set("save", "err", T("Choose at least one recipient (step 1)."));
      return;
    }
    saving = true;
    set("save", "", T("Checking the connections…"));
    checks = [];
    const r = await post("api/connection", { connection: c, force: !!force, clear: clears() }, "PUT");
    if (r.status === 422) {
      saving = false;
      set("save", "err", T("Some connections don't work yet."));
      checks = r.data.checks || [];
      return;
    }
    if (!r.ok) {
      saving = false;
      set("save", "err", problem(r.data));
      return;
    }
    clearTimeout(pollTimer);
    set("save", "ok", T("Saved. Starting…"));
    waitForService();
  }

  // waitForService waits for the service to answer with its settings, then opens them.
  function waitForService() {
    const next = async () => {
      try {
        const r = await fetch("api/settings", { headers: { "X-Lang": i18n.lang } });
        if (r.ok || r.status === 401) {
          location.href = "./";
          return;
        }
      } catch { /* restarting */ }
      setTimeout(next, 1500);
    };
    setTimeout(next, 1500);
  }

  // ---- load ----
  onMount(async () => {
    const r = await api("api/connection");
    if (!r.ok) {
      loadFailed = true;
      return;
    }
    const d = await r.json();
    first = d.first;
    saved = d.secrets || {};
    const c = d.connection || {};
    const f = c.frigate || {}, m = c.mqtt || {}, t = c.telegram || {};
    frigateUrl = f.url || "";
    frigateUser = f.username || "";
    frigateInsecure = !!f.insecure_skip_verify;
    if (f.username || f.insecure_skip_verify) frigateAuthOpen = true;
    mqttBroker = (m.broker || "").replace(/^tcp:\/\//, "");
    mqttUser = m.username || "";
    mqttPrefix = m.topic_prefix || "";
    loaded = { frigate: normURL(f.url || ""), broker: normBroker(m.broker || "") };
    timezone = c.timezone || Intl.DateTimeFormat().resolvedOptions().timeZone || "";
    mode = c.mode || "reviews";
    msgLang = c.language || "";
    allowedHosts = (c.web && c.web.allowed_hosts) || [];
    for (const [name, id] of Object.entries(t.chats || {})) chats.push({ id, name, private: id > 0, on: true });
    if (saved.token && (await checkToken(true))) pollChats();
    if (!first) mqttEditOpen = true; // changing a connection: the settings in view
    if (!f.url) discoverFrigate();
  });
  onDestroy(() => clearTimeout(pollTimer));

  // Listens to "input" on the element itself: Svelte delegates oninput to the root, which a
  // synthetic, non-bubbling input event (what the browser tests dispatch) never reaches.
  const typed = (node, fn) => {
    node.addEventListener("input", fn);
    return { destroy: () => node.removeEventListener("input", fn) };
  };
  const onEnter = (fn) => (e) => { if (e.key === "Enter") fn(); };
</script>

<header class="sticky top-[env(safe-area-inset-top,0px)] z-10 px-4 pt-4">
  <div class="mx-auto flex max-w-3xl items-center gap-3 rounded-full bg-[var(--glass)] py-1.5 pl-2 pr-1.5 ring-1 ring-[var(--ring)] backdrop-blur-xl">
    <span class="grid h-8 w-8 flex-none place-items-center rounded-full bg-[var(--accent)] text-white" aria-hidden="true">
      <svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M6 8a6 6 0 0 1 12 0c0 7 3 9 3 9H3s3-2 3-9"/><path d="M10.3 21a1.94 1.94 0 0 0 3.4 0"/></svg>
    </span>
    <h1 class="m-0 min-w-0 flex-1 truncate text-[15px] font-semibold">{T("Connection")}</h1>
    <LangMenu />
  </div>
</header>

<main class="mx-auto flex max-w-3xl flex-col gap-5 px-4 pb-16 pt-6">
  <div id="messages">
    {#if loadFailed}<div class="notice err">{T("Cannot read the connection.")}</div>{/if}
  </div>
  <div class="flex flex-col gap-2" id="intro-first" hidden={first !== true}>
    <span class="text-[clamp(28px,6vw,40px)] font-semibold leading-[1.05] tracking-tight">{T("Welcome!")}</span>
    <span class="text-[var(--muted)]">{T("Three steps and your notifications are running.")}</span>
  </div>
  <p class="m-0" id="intro-edit" hidden={first !== false}><a href="./">{T("← Back to the notifications")}</a></p>

  <Card id="sec-telegram">
    <div class="flex items-start gap-3">
      <span data-step class="flex h-8 w-8 flex-none items-center justify-center rounded-full bg-[var(--accent)]/15 text-[15px] font-semibold text-[var(--accent-ink)] tabular-nums" aria-hidden="true">1</span>
      <div class="flex min-w-0 flex-col gap-1">
        <h2 class="m-0 text-lg font-semibold">{T("Telegram bot")}</h2>
        <p class="m-0 text-[var(--muted)]"><WithLink text={T("In Telegram, open %s, send /newbot and choose a name: it gives you a token. Paste it here.")} href="https://t.me/BotFather" label="@BotFather" /></p>
      </div>
    </div>
    <label class="lbl" for="token">{T("Bot token")}</label>
    <div class="flex flex-wrap items-center gap-2">
      <input class="field font-mono sm:flex-1" type="text" id="token" autocomplete="off" spellcheck="false"
        placeholder={saved.token ? T("unchanged") : "123456789:AAH…"} bind:value={token}
        use:typed={() => { if (TOKEN_RE.test(token.trim())) checkTokenAndPoll(); }} onkeydown={onEnter(checkTokenAndPoll)}>
      <button type="button" class="btn" id="check-token" onclick={checkTokenAndPoll}>{T("Check")}</button>
      <span class="result {res.token.state}" id="token-result" role="status">{res.token.text}</span>
    </div>
    <div class="flex flex-col gap-3" id="chats-field" hidden={!bot && chats.length === 0}>
      <span class="lbl">{T("Who receives the notifications?")}</span>
      <p class="m-0 text-[var(--muted)]" id="chats-howto">
        {#if bot}<WithLink text={T("Send /start to %s from each Telegram account that should receive the notifications. For a group, add the bot to it and send /start there.")} href={"https://t.me/" + encodeURIComponent(bot.replace(/^@/, ""))} label={bot} />{/if}
      </p>
      <div class="flex flex-wrap gap-2" id="chats">
        {#each chats as c (c.id)}
          <button type="button" class="chip" aria-pressed={c.on} title={String(c.id)} onclick={() => { c.on = !c.on; }}>{(c.private ? "👤 " : "👥 ") + c.name}</button>
        {/each}
      </div>
      <p class="m-0 flex items-center gap-2 text-[var(--muted)]" id="chats-waiting" hidden={chats.length > 0 || !bot}>
        <span class="h-2 w-2 animate-pulse rounded-full bg-[var(--accent)]"></span>{T("Waiting for a message to the bot…")}
      </p>
      <details class="more" id="manual" bind:open={manualOpen}>
        <summary>{T("Add an ID by hand")}</summary>
        <div class="mt-3 flex flex-wrap items-center gap-2">
          <input class="field font-mono sm:w-40" type="text" id="manual-id" inputmode="numeric" placeholder="123456789" aria-label="ID"
            bind:this={manualIdEl} bind:value={manualId} use:typed={() => set("manual", "", "")} onkeydown={onEnter(addManual)}>
          <input class="field sm:w-40" type="text" id="manual-name" placeholder={T("Name")} aria-label={T("Name")} bind:value={manualName} onkeydown={onEnter(addManual)}>
          <button type="button" class="btn" id="manual-add" onclick={addManual}>{T("Add")}</button>
          <span class="result {res.manual.state}" id="manual-result" role="status">{res.manual.text}</span>
        </div>
        <p class="mb-0 mt-2 text-[12.5px] text-[var(--faint)]">{T("A person's ID is positive, a group's is negative (-100…).")}</p>
      </details>
    </div>
  </Card>

  <Card id="sec-frigate">
    <div class="flex items-start gap-3">
      <span data-step class="flex h-8 w-8 flex-none items-center justify-center rounded-full bg-[var(--accent)]/15 text-[15px] font-semibold text-[var(--accent-ink)] tabular-nums" aria-hidden="true">2</span>
      <div class="flex min-w-0 flex-col gap-1">
        <h2 class="m-0 text-lg font-semibold">{T("Frigate")}</h2>
        <p class="m-0 text-[var(--muted)]">{T("Looked for by itself on your network. If it is not found, enter its address: the one of its web page, with port 5000.")}</p>
      </div>
    </div>
    <label class="lbl" for="frigate-url">{T("Frigate address")}</label>
    <div class="flex flex-wrap items-center gap-2">
      <input class="field font-mono sm:flex-1" type="url" id="frigate-url" autocomplete="off" spellcheck="false" placeholder="http://192.168.1.10:5000"
        bind:this={frigateUrlEl} bind:value={frigateUrl} onkeydown={onEnter(checkFrigate)}>
      <button type="button" class="btn" id="check-frigate" onclick={checkFrigate}>{T("Check")}</button>
      <span class="result {searching ? '' : res.frigate.state}" id="frigate-result" role="status">{searching ? T("Looking for Frigate on your network…") : res.frigate.text}</span>
    </div>
    <details class="more" id="frigate-auth" bind:open={frigateAuthOpen}>
      <summary>{T("Frigate asks for a login")}</summary>
      <div class="mt-3 flex flex-col gap-2 sm:flex-row">
        <input class="field" type="text" id="frigate-user" autocomplete="off" placeholder={T("Username")} aria-label={T("Username")} bind:value={frigateUser}>
        <input class="field" type="password" id="frigate-pass" autocomplete="new-password" aria-label={T("Password")} disabled={clear.frigate_password}
          placeholder={keepsSaved("frigate_password") ? T("unchanged") : T("Password")} bind:value={frigatePass}>
      </div>
      <label class="mt-2 flex items-center gap-2" id="clear-frigate-pass-row" hidden={!saved.frigate_password}><input type="checkbox" id="clear-frigate-pass" bind:checked={clear.frigate_password}> {T("Remove the saved password")}</label>
      <label class="mt-2 flex items-center gap-2"><input type="checkbox" id="frigate-insecure" bind:checked={frigateInsecure}> {T("Accept a self-signed certificate")}</label>
    </details>
  </Card>

  <Card id="sec-mqtt">
    <div class="flex items-start gap-3">
      <span data-step class="flex h-8 w-8 flex-none items-center justify-center rounded-full bg-[var(--accent)]/15 text-[15px] font-semibold text-[var(--accent-ink)] tabular-nums" aria-hidden="true">3</span>
      <div class="flex min-w-0 flex-col gap-1">
        <h2 class="m-0 text-lg font-semibold">{T("MQTT")}</h2>
        <p class="m-0 text-[var(--muted)]">{T("How Frigate announces its detections. Read from Frigate's configuration: usually nothing to do.")}</p>
      </div>
    </div>
    <p class="result m-0 {brokerStatus.state}" id="broker-status" role="status" hidden={!brokerStatus.text}>{brokerStatus.text}</p>
    <div class="flex flex-col gap-2" id="mqtt-pass-field" hidden={!mqttPassShown}>
      <label class="lbl" for="mqtt-pass">{T("MQTT password")}</label>
      <input class="field" type="password" id="mqtt-pass" autocomplete="new-password" disabled={clear.mqtt_password}
        placeholder={keepsSaved("mqtt_password") ? T("unchanged") : T("Password")}
        bind:this={mqttPassEl} bind:value={mqttPass} use:typed={() => set("mqtt", "", "")} onkeydown={onEnter(checkMqtt)}>
      <label class="flex items-center gap-2" id="clear-mqtt-pass-row" hidden={!saved.mqtt_password}><input type="checkbox" id="clear-mqtt-pass" bind:checked={clear.mqtt_password}> {T("Remove the saved password")}</label>
      <p class="m-0 text-[12.5px] text-[var(--faint)]">{T("The one in the mqtt section of Frigate's configuration (Frigate does not share it).")}</p>
    </div>
    <div class="flex flex-wrap items-center gap-2">
      <button type="button" class="btn" id="check-mqtt" onclick={checkMqtt}>{T("Check")}</button>
      <span class="result {res.mqtt.state}" id="mqtt-result" role="status">{res.mqtt.text}</span>
    </div>
    <details class="more" id="mqtt-edit" bind:open={mqttEditOpen}>
      <summary>{T("Change the MQTT settings")}</summary>
      <div class="mt-3 flex flex-col gap-2">
        <label class="lbl" for="mqtt-broker">{T("Broker address")}</label>
        <input class="field font-mono" type="text" id="mqtt-broker" autocomplete="off" spellcheck="false" placeholder="192.168.1.10:1883"
          bind:this={mqttBrokerEl} bind:value={mqttBroker} use:typed={mqttEdited} onkeydown={onEnter(checkMqtt)}>
        <input class="field" type="text" id="mqtt-user" autocomplete="off" placeholder={T("Username (if any)")} aria-label={T("Username")} bind:value={mqttUser} use:typed={mqttEdited}>
        <input class="field font-mono" type="text" id="mqtt-prefix" placeholder="frigate" aria-label="Topic prefix" bind:value={mqttPrefix} use:typed={mqttEdited}>
        <p class="m-0 text-[12.5px] text-[var(--faint)]">{T("Frigate's mqtt.topic_prefix: frigate unless you changed it.")}</p>
      </div>
    </details>
  </Card>

  <Card id="sec-more">
    <div class="flex min-w-0 flex-col gap-1">
      <h2 class="m-0 text-lg font-semibold">{T("Password of this interface")}</h2>
      <p class="m-0 text-[var(--muted)]">{T("Optional. Without one, anyone on your network can open this page.")}</p>
    </div>
    <input class="field" type="password" id="web-pass" autocomplete="new-password" aria-label={T("Password")} disabled={clear.web_password}
      placeholder={keepsSaved("web_password") ? T("unchanged") : T("None")} bind:value={webPass}>
    <label class="flex items-center gap-2" id="clear-web-pass-row" hidden={!saved.web_password}><input type="checkbox" id="clear-web-pass" bind:checked={clear.web_password}> {T("Remove the saved password")}</label>
    <details class="more">
      <summary>{T("More options")}</summary>
      <div class="mt-3 flex flex-col gap-3">
        <label class="lbl" for="msg-lang">{T("Language of the Telegram messages")}</label>
        <select class="field" id="msg-lang" value={msgLang || i18n.lang} onchange={(e) => { msgLang = e.currentTarget.value; }}>
          {#each LANGUAGES as l (l.code)}<option value={l.code}>{l.name}</option>{/each}
        </select>
        <label class="lbl" for="timezone">{T("Time zone")}</label>
        <input class="field" type="text" id="timezone" spellcheck="false" placeholder="Europe/Paris" bind:value={timezone}>
        <label class="lbl" for="mode">{T("What triggers a notification")}</label>
        <select class="field" id="mode" bind:value={mode}>
          <option value="reviews">{T("Frigate's alerts (review items, Frigate ≥ 0.14)")}</option>
          <option value="events">{T("Each object Frigate detects")}</option>
        </select>
      </div>
    </details>
  </Card>

  <div class="flex flex-col gap-2" id="checks" hidden={failed.length === 0}>
    {#each failed as c (c.name)}
      <div class="notice err"><b>{c.name} — </b>{c.detail}{#if c.hint}<div class="text-[var(--muted)]">{c.hint}</div>{/if}</div>
    {/each}
    <div><button type="button" class="btn-link" onclick={() => save(true)}>{T("Save anyway")}</button></div>
  </div>

  <div class="flex flex-wrap items-center gap-3">
    <button type="button" class="btn btn-primary" id="save" disabled={saving} onclick={() => save(false)}>{first !== false ? T("Save and start") : T("Save and restart")}</button>
    <span class="result {res.save.state}" id="save-result" role="status">{res.save.text}</span>
  </div>
</main>
