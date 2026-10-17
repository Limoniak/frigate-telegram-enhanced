<script>
  // One detailed setting. Without a camera it edits the default settings; with one it
  // edits that camera's override, which a "customize" box creates or removes (unchecked,
  // the inherited value shows, inert).
  import { T } from "./i18n.svelte.js";
  import { app, edit } from "./store.svelte.js";
  import { defaultFor, optionsFor } from "./fields.js";
  import Chips from "./Chips.svelte";
  import Duration from "./Duration.svelte";
  import Ranges from "./Ranges.svelte";
  import Score from "./Score.svelte";
  import Switch from "./Switch.svelte";

  let { field, camera = null } = $props();

  const patch = $derived(camera ? app.overlay.cameras[camera] || {} : app.overlay.notify);
  const inherited = $derived(camera ? app.overlay.notify[field.key] : undefined);
  const custom = $derived(!camera || field.key in patch);
  const value = $derived(custom ? patch[field.key] : inherited);
  const options = $derived(optionsFor(app.data, field, camera));

  const write = (v) => edit((o) => { (camera ? (o.cameras[camera] ??= {}) : o.notify)[field.key] = v; });
  const toggle = (e) => {
    const on = e.currentTarget.checked;
    edit((o) => {
      const p = (o.cameras[camera] ??= {});
      if (on) p[field.key] = defaultFor(field, o.notify[field.key]);
      else delete p[field.key];
    });
  };
</script>

<div class="flex flex-col gap-2" data-key={field.key}>
  <div class="flex items-center justify-between gap-3">
    <span class="min-w-0">{field.name}</span>
    <span class="flex flex-none items-center gap-3">
      {#if camera}
        <label class="flex items-center gap-2 text-[12px] text-[var(--muted)]"><input type="checkbox" checked={custom} onchange={toggle} />{T("customize")}</label>
      {/if}
      {#if field.type === "bool"}
        <Switch checked={!!value} label={field.name} disabled={!custom} onchange={write} />
      {/if}
    </span>
  </div>
  {#if field.type === "chips"}
    <Chips label={field.name} {options} value={Array.isArray(value) ? value : []} free={field.free} disabled={!custom} onchange={write} />
  {:else if field.type === "duration"}
    <Duration label={field.name} {value} disabled={!custom} onchange={write} />
  {:else if field.type === "ranges"}
    <Ranges {value} disabled={!custom} onchange={write} />
  {:else if field.type === "score"}
    <Score {value} {options} disabled={!custom} onchange={write} />
  {:else if field.type === "choice"}
    <select class="field" aria-label={field.name} disabled={!custom} onchange={(e) => write(e.currentTarget.value)}>
      {#each field.choices as [v, label] (v)}<option value={v} selected={v === (value || "skip")}>{label}</option>{/each}
    </select>
  {/if}
  {#if field.hint}<p class="m-0 text-[12px] text-[var(--faint)]">{field.hint}</p>{/if}
</div>
