<script>
  // The minimum score: one value for every object, or one per object.
  import { T } from "./i18n.svelte.js";
  import { clampScore } from "./fields.js";

  let { value, options, onchange, disabled = false } = $props();
  const v = $derived(value && typeof value === "object" ? value : { default: 0 });
  const byLabel = $derived(v.by_label && Object.keys(v.by_label).length ? v.by_label : null);
  const remaining = $derived(options.filter((o) => !(byLabel && o in byLabel)));

  const mode = (e) => onchange(e.currentTarget.value === "global" ? { default: 0.7 } : { default: 0, by_label: { [options[0] || "person"]: 0.7 } });
  const setLabel = (label, n) => onchange({ default: 0, by_label: { ...byLabel, [label]: clampScore(n) } });
  function drop(label) {
    const next = { ...byLabel };
    delete next[label];
    onchange(Object.keys(next).length ? { default: 0, by_label: next } : { default: 0.7 });
  }
  function add(e) {
    const o = e.currentTarget.value;
    if (o) onchange({ default: 0, by_label: { ...byLabel, [o]: 0.7 } });
  }
</script>

<div class="flex flex-col gap-2">
  <select class="field" aria-label={T("Minimum score")} {disabled} onchange={mode}>
    <option value="global" selected={!byLabel}>{T("One value for every object")}</option>
    <option value="label" selected={!!byLabel}>{T("One value per object")}</option>
  </select>
  {#if !byLabel}
    <div class="flex flex-wrap items-center gap-2">
      <input type="number" class="field w-24" min="0" max="1" step="0.05" aria-label={T("Minimum score")} value={v.default ?? 0} {disabled} onchange={(e) => { const n = clampScore(e.currentTarget.value); e.currentTarget.value = n; onchange({ default: n }); }} />
      <span class="text-[12px] text-[var(--faint)]">{T("0 = no filter, 1 = certainty")}</span>
    </div>
  {:else}
    {#each Object.entries(byLabel) as [label, score] (label)}
      <div class="flex flex-wrap items-center gap-2">
        <span class="min-w-16">{label}</span>
        <input type="number" class="field w-24" min="0" max="1" step="0.05" aria-label={label} value={score} {disabled} onchange={(e) => { const n = clampScore(e.currentTarget.value); e.currentTarget.value = n; setLabel(label, n); }} />
        <button type="button" class="chip" title={T("Remove")} aria-label={T("Remove")} {disabled} onclick={() => drop(label)}>✕</button>
      </div>
    {/each}
    {#if remaining.length && !disabled}
      <select class="field" aria-label={T("Add an object…")} onchange={add}>
        <option value="">{T("Add an object…")}</option>
        {#each remaining as o (o)}<option value={o}>{o}</option>{/each}
      </select>
    {/if}
    <p class="m-0 text-[12px] text-[var(--faint)]">{T("An object missing from this list isn't filtered on score.")}</p>
  {/if}
</div>
