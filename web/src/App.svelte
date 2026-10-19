<script>
  // The shell of the main interface: a floating pill navigation, the save status, the
  // screens (#overview, #style, #when, #cameras, #activity, #advanced), and the footer links.
  import { onMount } from "svelte";
  import { T, Tf, i18n } from "./lib/i18n.svelte.js";
  import { app, flush, leave, load, startPolling } from "./lib/store.svelte.js";
  import { api } from "./lib/api.js";
  import LangMenu from "./lib/LangMenu.svelte";
  import ThemeMenu from "./lib/ThemeMenu.svelte";
  import Overview from "./Overview.svelte";
  import Style from "./Style.svelte";
  import When from "./When.svelte";
  import Cameras from "./Cameras.svelte";
  import Activity from "./Activity.svelte";
  import Advanced from "./Advanced.svelte";
  import { parseRoute } from "./lib/route.js";

  let route = $state(parseRoute(location.hash));
  let failed = $state("");
  let menu = $state(false); // the screens' list, opened from its button on a narrow screen
  let nav = $state();
  let scrollY = $state(0);
  // Scrolled down: the bar shrinks, so that it covers less of the page.
  const compact = $derived(scrollY > 24);
  let faded = $state(false);
  $effect(() => {
    faded = false;
    if (app.status.kind !== "saved") return;
    const t = setTimeout(() => { faded = true; }, 2500);
    return () => clearTimeout(t);
  });

  $effect(() => {
    document.documentElement.lang = i18n.lang;
    document.title = T("frigate-telegram-enhanced — notifications");
  });

  onMount(() => {
    const onHash = () => { route = parseRoute(location.hash); menu = false; };
    addEventListener("hashchange", onHash);
    load().then(startPolling).catch((e) => { failed = T("Loading failed:\n") + e.message; });
    return () => removeEventListener("hashchange", onHash);
  });

  const screens = () => [
    ["overview", T("Overview")], ["style", T("Style")], ["when", T("When")], ["cameras", T("Cameras")],
    ...(app.data?.can_history ? [["activity", T("Recent activity")]] : []),
    ["advanced", T("Advanced settings")],
  ];

  // A route to a screen that is not offered (no history) shows the overview.
  const current = $derived(screens().some(([id]) => id === route.screen) ? route.screen : "overview");
  async function logout() {
    try {
      await flush(); // the session ends below: the pending save must go first
      await api("logout", { method: "POST" }, { lang: i18n.lang, next: "" });
    } finally {
      location.href = "login?lang=" + i18n.lang;
    }
  }

  // Footer links finish the pending save before leaving the page.
  const goTo = (href) => (e) => { e.preventDefault(); leave(href); };
</script>

<svelte:window bind:scrollY onkeydown={(e) => { if (e.key === "Escape") menu = false; }} onclick={(e) => { if (menu && !nav.contains(e.target)) menu = false; }} />

<header class="sticky top-[env(safe-area-inset-top,0px)] z-10 px-4 transition-[padding] duration-200 {compact ? 'pt-2' : 'pt-4'}">
  <!-- Under lg the screens fold into a list under a button: the pills no longer fit on one line. -->
  <nav id="nav" bind:this={nav} class="relative mx-auto flex w-full items-center gap-1 rounded-full bg-[var(--glass)] ring-1 ring-[var(--ring)] backdrop-blur-xl transition-[padding,font-size] duration-200 lg:max-w-max {compact ? 'p-0.5 text-[13px] shadow-lg' : 'p-1.5'}">
    <button type="button" class="flex flex-1 items-center gap-2 rounded-full px-3 py-1.5 text-left text-[var(--ink)] lg:hidden" aria-expanded={menu} aria-controls="screens"
      onclick={() => { menu = !menu; }}>
      <svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="M4 6h16M4 12h16M4 18h16"/></svg>
      <span class="sr-only">{T("Menu")} · </span>{Object.fromEntries(screens())[current]}
    </button>
    <div id="screens" class="{menu ? 'flex' : 'hidden'} absolute inset-x-0 top-full mt-2 flex-col gap-1 rounded-[20px] bg-[var(--bg)] p-1.5 ring-1 ring-[var(--ring)] lg:static lg:mt-0 lg:flex lg:flex-row lg:bg-transparent lg:p-0 lg:ring-0">
      {#each screens() as [id, label] (id)}
        <a href="#{id}" class="whitespace-nowrap rounded-full px-3 py-2 no-underline lg:py-1.5 {current === id ? 'bg-[var(--glass-hi)] text-[var(--ink)]' : 'text-[var(--muted)] hover:text-[var(--ink)]'}" aria-current={current === id ? "page" : undefined} onclick={() => { menu = false; }}>{label}</a>
      {/each}
    </div>
    <LangMenu />
    <ThemeMenu />
  </nav>
</header>
<!-- Floating, so that it never shifts the page, and click-through, so that it never covers a control; "Saved" fades after a moment, "Saving…" and "Not saved" stay. Under lg it sits above the style's preview button. -->
<p id="save-status" class="result pointer-events-none fixed bottom-[calc(env(safe-area-inset-bottom)+4.5rem)] lg:bottom-4 left-1/2 z-20 m-0 -translate-x-1/2 whitespace-nowrap rounded-full bg-[var(--bg)] px-4 py-2 shadow-lg ring-1 ring-[var(--ring)] {app.status.kind === 'saved' ? 'ok' : ''}"
  role="status" hidden={!app.status.text || faded}>{app.status.text}</p>

<main class="mx-auto flex max-w-6xl flex-col gap-5 px-4 pb-16 pt-6">
  {#if failed}<div class="notice err">{failed}</div>{/if}
  {#if app.warning}<div class="notice">{app.warning}</div>{/if}
  {#if app.error}<div class="notice err" role="alert">{app.error}</div>{/if}
  {#if app.data}
    {#if current === "style"}<Style />
    {:else if current === "when"}<When />
    {:else if current === "cameras"}<Cameras {route} />
    {:else if current === "activity"}<Activity {route} />
    {:else if current === "advanced"}<Advanced />
    {:else}<Overview />{/if}
  {:else if !failed}
    <!-- Until the settings arrive: the overview's cards, empty, so that the page doesn't look broken. -->
    <div class="grid grid-cols-1 gap-4 lg:grid-cols-12" aria-busy="true">
      <div class="grid h-48 animate-pulse place-items-center rounded-[20px] bg-[var(--glass)] text-[var(--muted)] ring-1 ring-[var(--ring)] lg:col-span-7" role="status">{T("Loading…")}</div>
      <div class="h-48 animate-pulse rounded-[20px] bg-[var(--glass)] ring-1 ring-[var(--ring)] lg:col-span-5"></div>
    </div>
  {/if}
</main>

<footer class="mx-auto flex max-w-6xl flex-wrap items-center gap-4 px-4 pb-10 text-[12px] text-[var(--faint)]">
  {#if app.data}
    <span>{app.data.mode === "events" ? T("Notifications for Frigate's events") : T("Notifications for Frigate's alerts (reviews)")} · {Tf("time zone %s", app.data.timezone)}</span>
    <span class="flex-1"></span>
    {#if app.data.can_connection}<a href="setup" onclick={goTo("setup")}>{T("Telegram, Frigate and MQTT connection")}</a>{/if}
    {#if app.data.auth}<button type="button" class="btn-link" id="logout" onclick={logout}>{T("Log out")}</button>{/if}
  {/if}
</footer>
