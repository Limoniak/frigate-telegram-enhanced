<script>
  // Time ranges (from, to), as for quiet hours; a range may cross midnight.
  import { T } from "./i18n.svelte.js";

  let { value, onchange, disabled = false, label = undefined, describedby = undefined } = $props();
  const list = $derived(Array.isArray(value) ? value : []);

  // Every row names its field and its number, or the inputs of the rows are alike.
  const named = (i, what) => (label ? `${label} ${i + 1} – ${what}` : what);
  const set = (i, key, v) => onchange(list.map((r, j) => (j === i ? { ...r, [key]: v } : r)));
</script>

<div role={label ? "group" : undefined} aria-label={label} aria-describedby={describedby} class="flex flex-col gap-2">
  {#each list as r, i (i)}
    <div class="flex flex-wrap items-center gap-2">
      <span>{T("from")}</span>
      <input type="time" class="field w-auto" aria-label={named(i, T("from"))} value={r.from} {disabled} onchange={(e) => set(i, "from", e.currentTarget.value)} />
      <span>{T("to")}</span>
      <input type="time" class="field w-auto" aria-label={named(i, T("to"))} value={r.to} {disabled} onchange={(e) => set(i, "to", e.currentTarget.value)} />
      <button type="button" class="chip" title={T("Remove")} aria-label={T("Remove")} {disabled} onclick={() => onchange(list.filter((_, j) => j !== i))}>✕</button>
    </div>
  {/each}
  {#if !disabled}
    <div><button type="button" class="btn-link" onclick={() => onchange([...list, { from: "22:00", to: "07:00" }])}>{T("+ Add a range")}</button></div>
  {:else if !list.length}
    <span class="text-[12px] text-[var(--faint)]">{T("no range")}</span>
  {/if}
</div>
