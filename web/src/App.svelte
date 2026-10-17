<script>
  // The shell of the main interface: a floating pill navigation, the save status, the
  // screens (#overview, #style, #when, #cameras, #recipients, #activity, #advanced), and the footer links.
  import { onMount } from "svelte";
  import { T, i18n } from "./lib/i18n.svelte.js";
  import { app, leave, load, startPolling } from "./lib/store.svelte.js";
  import { api } from "./lib/api.js";
  import LangMenu from "./lib/LangMenu.svelte";
  import Overview from "./Overview.svelte";
  import Style from "./Style.svelte";
  import When from "./When.svelte";
  import Recipients from "./Recipients.svelte";
  import Cameras from "./Cameras.svelte";
  import Activity from "./Activity.svelte";
  import Advanced from "./Advanced.svelte";
  import { parseRoute } from "./lib/route.js";

  let route = $state(parseRoute(location.hash));
  let failed = $state("");

  $effect(() => {
    document.documentElement.lang = i18n.lang;
    document.title = T("frigate-telegram-enhanced — notifications");
  });

  onMount(() => {
    const onHash = () => { route = parseRoute(location.hash); };
    addEventListener("hashchange", onHash);
    load().then(startPolling).catch((e) => { failed = T("Loading failed:\n") + e.message; });
    return () => removeEventListener("hashchange", onHash);
  });

  // Recipients only matter with more than one chat.
  const hasRecipients = $derived((app.data?.chats || []).length > 1);
  const screens = () => [
    ["overview", T("Overview")], ["style", T("Style")], ["when", T("When")], ["cameras", T("Cameras")],
    ...(hasRecipients ? [["recipients", T("Recipients")]] : []),
    ...(app.data?.can_history ? [["activity", T("Recent activity")]] : []),
    ["advanced", T("Advanced settings")],
  ];

  async function logout() {
    try {
      await api("logout", { method: "POST" }, { lang: i18n.lang, next: "" });
    } finally {
      location.href = "login?lang=" + i18n.lang;
    }
  }

  // Footer links finish the pending save before leaving the page.
  const goTo = (href) => (e) => { e.preventDefault(); leave(href); };
</script>

<header class="sticky top-[env(safe-area-inset-top,0px)] z-10 px-4 pt-4">
  <nav id="nav" class="mx-auto flex w-full max-w-max items-center gap-1 overflow-x-auto rounded-full bg-[var(--glass)] p-1.5 ring-1 ring-[var(--ring)] backdrop-blur-xl">
    {#each screens() as [id, label] (id)}
      <a href="#{id}" class="whitespace-nowrap rounded-full px-3 py-1.5 no-underline {route.screen === id ? 'bg-[var(--glass-hi)] text-[var(--ink)]' : 'text-[var(--muted)]'}" aria-current={route.screen === id ? "page" : undefined}>{label}</a>
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
    {#if route.screen === "style"}<Style />
    {:else if route.screen === "when"}<When />
    {:else if route.screen === "recipients" && hasRecipients}<Recipients />
    {:else if route.screen === "cameras"}<Cameras {route} />
    {:else if route.screen === "activity" && app.data.can_history}<Activity />
    {:else if route.screen === "advanced"}<Advanced />
    {:else}<Overview />{/if}
  {/if}
</main>

<footer class="mx-auto flex max-w-6xl flex-wrap items-center gap-4 px-4 pb-10 text-[12px] text-[var(--faint)]">
  {#if app.data}
    <span>{T("mode ") + app.data.mode} · {app.data.timezone}</span>
    <span class="flex-1"></span>
    <a href="classic" onclick={goTo("classic")}>{T("Classic interface")}</a>
    {#if app.data.can_connection}<a href="setup" onclick={goTo("setup")}>{T("Connection")}</a>{/if}
    {#if app.data.auth}<button type="button" class="btn-link" id="logout" onclick={logout}>{T("Log out")}</button>{/if}
  {/if}
</footer>
