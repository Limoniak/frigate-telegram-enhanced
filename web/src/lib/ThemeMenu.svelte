<script>
  // The theme menu: a native select, transparent, over the icon of the current choice
  // (sun, moon, or half a disc for Auto, which follows the system).
  import { T } from "./i18n.svelte.js";
  import { applyTheme, savedTheme } from "./theme.js";

  let choice = $state(savedTheme());
  const choose = (v) => { choice = v; applyTheme(v); };
</script>

<label class="relative inline-flex h-[34px] flex-none items-center gap-1.5 rounded-[9px] bg-[var(--glass)] px-2.5 text-xs font-semibold text-[var(--muted)] ring-1 ring-[var(--ring)] hover:text-[var(--ink)] has-[select:focus-visible]:outline has-[select:focus-visible]:outline-2 has-[select:focus-visible]:outline-[var(--accent)]">
  <svg class="h-3.5 w-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    {#if choice === "light"}<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/>
    {:else if choice === "dark"}<path d="M12 3a9 9 0 1 0 9 9 7 7 0 0 1-9-9Z"/>
    {:else}<circle cx="12" cy="12" r="9"/><path d="M12 3a9 9 0 0 0 0 18Z" fill="currentColor"/>{/if}
  </svg>
  <svg class="h-3 w-3" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="m6 9 6 6 6-6"/></svg>
  <select class="absolute inset-0 w-full cursor-pointer text-base opacity-0" aria-label={T("Theme")}
    value={choice} onchange={(e) => choose(e.currentTarget.value)}>
    <option value="auto">{T("Auto (system)")}</option>
    <option value="light">{T("Light")}</option>
    <option value="dark">{T("Dark")}</option>
  </select>
</label>
