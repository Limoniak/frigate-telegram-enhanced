<script>
  // Multiple choice among options, as toggle chips; with `free`, a field adds values
  // that are not in the list. Values chosen earlier but no longer offered stay shown.
  // name gives the text of a value (an object's emoji and name); wide makes room in the
  // field for an address.
  import { T } from "./i18n.svelte.js";

  let { options, value, name = (v) => v, free = false, wide = false, disabled = false, label = undefined, describedby = undefined, onchange } = $props();

  const shown = $derived([...options, ...value.filter((v) => !options.includes(v))]);
  const toggle = (opt) => onchange(value.includes(opt) ? value.filter((x) => x !== opt) : [...value, opt]);

  function add(e) {
    const v = e.currentTarget.value.trim();
    if (v && !value.includes(v)) onchange([...value, v]);
    e.currentTarget.value = "";
  }
</script>

<div role={label ? "group" : undefined} aria-label={label} aria-describedby={describedby} class="flex flex-wrap items-center gap-2">
  {#each shown as opt (opt)}
    <button type="button" class="chip" aria-pressed={value.includes(opt)} {disabled} onclick={() => toggle(opt)}>{name(opt)}</button>
  {/each}
  {#if free && !disabled}
    <input type="text" class="field {wide ? 'w-80 max-w-full' : 'w-32'}" placeholder={T("add…")} aria-label={label ? label + " – " + T("add…") : T("add…")} onchange={add} />
  {/if}
</div>
