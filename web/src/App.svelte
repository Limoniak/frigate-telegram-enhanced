<script>
  // The shell of the main interface: a floating pill navigation, the save status, the
  // screens (#overview, #style), and links to the screens still in the classic
  // interface.
  import { onMount } from "svelte";
  import { T, i18n } from "./lib/i18n.svelte.js";
  import { app, load, startPolling } from "./lib/store.svelte.js";
  import { api } from "./lib/api.js";
  import LangMenu from "./lib/LangMenu.svelte";
  import Overview from "./Overview.svelte";
  import Style from "./Style.svelte";

  let route = $state(location.hash === "#style" ? "style" : "overview");
  let failed = $state("");

  $effect(() => {
    document.documentElement.lang = i18n.lang;
    document.title = T("frigate-telegram-enhanced — notifications");
  });

  onMount(() => {
    const onHash = () => { route = location.hash === "#style" ? "style" : "overview"; };
    addEventListener("hashchange", onHash);
    load().then(startPolling).catch((e) => { failed = T("Loading failed:\n") + e.message; });
    return () => removeEventListener("hashchange", onHash);
  });

  // The screens not yet moved: their section in the classic interface.
  const classic = () => [
    [T("When"), "classic#sec-when"],
    [T("Recipients"), "classic#recipients-box"],
    [T("Cameras"), "classic#sec-cameras"],
    [T("Recent activity"), "classic#activity-box"],
    [T("Advanced settings"), "classic#advanced"],
  ];

  async function logout() {
    await api("logout", { method: "POST" }, { lang: i18n.lang, next: "" });
    location.href = "login?lang=" + i18n.lang;
  }
</script>

<header class="sticky top-[env(safe-area-inset-top,0px)] z-10 px-4 pt-4">
  <nav id="nav" class="mx-auto flex w-full max-w-max items-center gap-1 overflow-x-auto rounded-full bg-[var(--glass)] p-1.5 ring-1 ring-[var(--ring)] backdrop-blur-xl">
    <a href="#overview" class="whitespace-nowrap rounded-full px-3 py-1.5 no-underline {route === 'overview' ? 'bg-[var(--glass-hi)] text-[var(--ink)]' : 'text-[var(--muted)]'}" aria-current={route === "overview" ? "page" : undefined}>{T("Overview")}</a>
    <a href="#style" class="whitespace-nowrap rounded-full px-3 py-1.5 no-underline {route === 'style' ? 'bg-[var(--glass-hi)] text-[var(--ink)]' : 'text-[var(--muted)]'}" aria-current={route === "style" ? "page" : undefined}>{T("Style")}</a>
    {#each classic() as [label, href] (href)}
      <a {href} class="whitespace-nowrap rounded-full px-3 py-1.5 text-[var(--muted)] no-underline">{label}</a>
    {/each}
    <LangMenu />
  </nav>
  <p id="save-status" class="result m-0 mt-2 text-center {app.status.kind === 'saved' ? 'ok' : ''}" role="status" hidden={!app.status.text}>{app.status.text}</p>
</header>

<main class="mx-auto flex max-w-6xl flex-col gap-5 px-4 pb-16 pt-6">
  {#if failed}<div class="notice err">{failed}</div>{/if}
  {#if app.warning}<div class="notice">{app.warning}</div>{/if}
  {#if app.error}<div class="notice err" role="alert">{app.error}</div>{/if}
  {#if app.data}
    {#if route === "style"}<Style />{:else}<Overview />{/if}
  {/if}
</main>

<footer class="mx-auto flex max-w-6xl flex-wrap items-center gap-4 px-4 pb-10 text-[12px] text-[var(--faint)]">
  {#if app.data}
    <span>{T("mode ") + app.data.mode} · {app.data.timezone}</span>
    <span class="flex-1"></span>
    <a href="classic">{T("Classic interface")}</a>
    {#if app.data.can_connection}<a href="setup">{T("Connection")}</a>{/if}
    {#if app.data.auth}<button type="button" class="btn-link" id="logout" onclick={logout}>{T("Log out")}</button>{/if}
  {/if}
</footer>
