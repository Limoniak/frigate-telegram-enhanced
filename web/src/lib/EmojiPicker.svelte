<script>
  // A camera's emoji: picked from those a camera's place suggests, searched among every
  // emoji by its name in the page's language, or typed.
  import { T, i18n } from "./i18n.svelte.js";
  import { loadEmojis, sameEmoji, searchEmojis } from "./emoji.js";

  let { value = "", label, onchange } = $props();
  let open = $state(false);
  let query = $state("");
  let all = $state(null); // every emoji, once loaded
  let failed = $state(false);

  // Doors and buildings, outdoors, vehicles, animals, rooms, then the watchful ones.
  const SUGGESTED = [
    "🚪", "🏠", "🏡", "🏢", "🏭", "🏚️", "🛖", "⛺", "🅿️", "🛣️", "🚧", "🧱",
    "🌳", "🌲", "🌷", "🌻", "🪴", "⛲", "🏊", "🌊", "🚗", "🚙", "🛻", "🚚",
    "🏍️", "🚲", "🛴", "🚤", "🐕", "🐈", "🐓", "🐴", "🐄", "🐑", "🐝", "🦊",
    "🛋️", "🛏️", "🍳", "🛁", "🧺", "🧰", "📦", "📬", "🔔", "🔒", "👶", "🧸",
    "📷", "🎥", "👁️", "⭐", "🌙", "☀️",
  ];

  $effect(() => {
    if (!open) return;
    const lang = i18n.lang; // the names follow the page's language
    loadEmojis(lang).then((list) => { all = list; failed = false; }, () => { failed = true; });
  });

  const nameOf = (u) => all?.find((e) => sameEmoji(e.unicode, u))?.label ?? u;
  const suggested = $derived(SUGGESTED.map((u) => ({ unicode: u, label: nameOf(u) })));
  const found = $derived(all && query.trim() ? searchEmojis(all, query) : null);

  function pick(e) {
    open = false;
    query = "";
    onchange(e);
  }
</script>

{#snippet grid(list, name)}
  <div role="group" aria-label={name} class="emoji-grid grid grid-cols-8 gap-1 sm:grid-cols-12">
    {#each list as e (e.unicode)}
      <button type="button" aria-label={e.label} title={e.label} aria-pressed={sameEmoji(value, e.unicode)} onclick={() => pick(e.unicode)}
        class="grid h-10 place-items-center rounded-[10px] text-[20px] transition-colors hover:bg-[var(--glass-hi)] {sameEmoji(value, e.unicode) ? 'ring-2 ring-[var(--accent)]' : ''}">{e.unicode}</button>
    {/each}
  </div>
{/snippet}

<div class="flex flex-col gap-2">
  <div class="flex flex-wrap items-center gap-2">
    <input class="field w-24 text-center" maxlength="16" placeholder="🚪" aria-label={label}
      {value} onchange={(e) => onchange(e.currentTarget.value.trim())}>
    <button type="button" class="btn" aria-expanded={open} onclick={() => (open = !open)}>{T("Choose an emoji")}</button>
    {#if value}<button type="button" class="btn-link" onclick={() => pick("")}>{T("Remove")}</button>{/if}
  </div>
  {#if open}
    <div class="flex flex-col gap-3 rounded-[16px] p-3 ring-1 ring-[var(--ring)]">
      <input type="search" class="field" placeholder={T("Search an emoji")} aria-label={T("Search an emoji")} bind:value={query}>
      <div class="flex max-h-72 flex-col gap-3 overflow-y-auto">
        {#if found}
          {#if found.length}{@render grid(found, T("Search an emoji"))}
          {:else}<p class="m-0 text-[var(--muted)]">{T("No emoji found.")}</p>{/if}
        {:else}
          <span class="lbl">{T("Suggested")}</span>
          {@render grid(suggested, T("Suggested"))}
          {#if all}
            <span class="lbl">{T("All")}</span>
            {@render grid(all, T("All"))}
          {:else if failed}<p class="m-0 text-[var(--muted)]">{T("Error")}</p>
          {:else}<p class="m-0 text-[var(--muted)]">{T("Loading…")}</p>{/if}
        {/if}
      </div>
    </div>
  {/if}
</div>
