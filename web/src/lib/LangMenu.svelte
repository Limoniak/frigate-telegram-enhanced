<script>
  // The language menu: a native select, transparent, over the globe and the current
  // code. Its first choice, Auto, follows the browser.
  import { LANGUAGES, T, i18n, setChoice } from "./i18n.svelte.js";
</script>

<label class="relative inline-flex h-[34px] flex-none items-center gap-1.5 rounded-[9px] bg-[var(--glass)] px-2.5 text-xs font-semibold text-[var(--muted)] ring-1 ring-[var(--ring)] hover:text-[var(--ink)] has-[select:focus-visible]:outline has-[select:focus-visible]:outline-2 has-[select:focus-visible]:outline-[var(--accent)]">
  <svg class="h-3.5 w-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><circle cx="12" cy="12" r="10"/><path d="M12 2a14.5 14.5 0 0 0 0 20 14.5 14.5 0 0 0 0-20M2 12h20"/></svg>
  <span class="lang-code">{i18n.lang.toUpperCase()}</span>
  <svg class="h-3 w-3" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="m6 9 6 6 6-6"/></svg>
  <select class="absolute inset-0 w-full cursor-pointer text-base opacity-0" aria-label="Language / Langue"
    value={i18n.choice} onchange={(e) => setChoice(e.currentTarget.value)}>
    <option value="auto">{T("Auto (browser)")}</option>
    {#each LANGUAGES as l (l.code)}<option value={l.code}>{l.name}</option>{/each}
  </select>
</label>
