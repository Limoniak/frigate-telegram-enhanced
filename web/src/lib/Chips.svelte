<script>
  // Multiple choice among options, as toggle chips; with `free`, a field adds values
  // that are not in the list. Values chosen earlier but no longer offered stay shown.
  import { T } from "./i18n.svelte.js";

  let { options, value, free = false, disabled = false, onchange } = $props();

  const shown = $derived([...options, ...value.filter((v) => !options.includes(v))]);
  const toggle = (opt) => onchange(value.includes(opt) ? value.filter((x) => x !== opt) : [...value, opt]);

  function add(e) {
    const v = e.currentTarget.value.trim();
    if (v && !value.includes(v)) onchange([...value, v]);
    e.currentTarget.value = "";
  }
</script>

<div class="flex flex-wrap items-center gap-2">
  {#each shown as opt (opt)}
    <button type="button" class="chip" aria-pressed={value.includes(opt)} {disabled} onclick={() => toggle(opt)}>{opt}</button>
  {/each}
  {#if free && !disabled}
    <input type="text" class="field w-32" placeholder={T("add…")} aria-label={T("add…")} onchange={add} />
  {/if}
</div>
