<script>
  // Time ranges (from, to), as for quiet hours; a range may cross midnight. A range that
  // starts where it ends (or lacks an hour) would never apply, and the server refuses it:
  // until it is fixed the rows stay as typed, the faulty one flagged, and nothing is sent.
  import { T } from "./i18n.svelte.js";

  let { value, onchange, disabled = false, label = undefined, describedby = undefined } = $props();
  const uid = $props.id();
  const list = $derived(Array.isArray(value) ? value : []);
  let draft = $state(null);
  // A value or a disabled state that changes drops what was typed (and its flags).
  $effect.pre(() => { value; disabled; draft = null; });
  const rows = $derived(draft ?? list);
  const bad = (r) => !r.from || !r.to || r.from === r.to;

  // Every row names its field and its number, or the inputs of the rows are alike.
  const named = (i, what) => (label ? `${label} ${i + 1} – ${what}` : what);
  function send(next) {
    if (next.some(bad)) {
      draft = next;
      return;
    }
    draft = null;
    onchange(next);
  }
  const set = (i, key, v) => send(rows.map((r, j) => (j === i ? { ...r, [key]: v } : r)));
</script>

<div role={label ? "group" : undefined} aria-label={label} aria-describedby={describedby} class="flex flex-col gap-2">
  {#each rows as r, i (i)}
    {@const wrong = !!draft && bad(r)}
    {@const error = wrong ? `${uid}-error-${i}` : undefined}
    <div class="flex flex-wrap items-center gap-2">
      <span>{T("from")}</span>
      <input type="time" class="field w-auto" aria-label={named(i, T("from"))} aria-invalid={wrong ? "true" : undefined} aria-describedby={error} value={r.from} {disabled} onchange={(e) => set(i, "from", e.currentTarget.value)} />
      <span>{T("to")}</span>
      <input type="time" class="field w-auto" aria-label={named(i, T("to"))} aria-invalid={wrong ? "true" : undefined} aria-describedby={error} value={r.to} {disabled} onchange={(e) => set(i, "to", e.currentTarget.value)} />
      <button type="button" class="chip" title={T("Remove")} aria-label={named(i, T("Remove"))} {disabled} onclick={() => send(rows.filter((_, j) => j !== i))}>✕</button>
      {#if wrong}<p id={error} class="m-0 basis-full text-[12px] text-[var(--err)]" role="alert">{T("Enter a start and an end that are different.")}</p>{/if}
    </div>
  {/each}
  {#if !disabled}
    <div><button type="button" class="btn-link" onclick={() => send([...rows, { from: "22:00", to: "07:00" }])}>{T("+ Add a range")}</button></div>
  {:else if !rows.length}
    <span class="text-[12px] text-[var(--faint)]">{T("no range")}</span>
  {/if}
</div>
