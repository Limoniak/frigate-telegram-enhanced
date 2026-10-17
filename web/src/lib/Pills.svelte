<script>
  // A group of exclusive choices, each a patch of settings. For a camera, a first
  // "Default" pill removes its override; when no choice matches, an inert "Custom"
  // pill says so. Same look as the burst group of the Style screen.
  import { T } from "./i18n.svelte.js";
  import { app, change, edit } from "./store.svelte.js";
  import { clearKeys, effective, inheritsAll, matches } from "./settings.js";

  // values and pick serve a level that is not a camera's settings (a recipient): the
  // values to compare with, and what a click does.
  let { options, camera = null, id = undefined, values = undefined, pick = undefined } = $props();

  const keys = $derived(Object.keys(Object.assign({}, ...options.map((o) => o.patch))));
  const inherit = $derived(!!camera && inheritsAll(app.overlay, keys, camera));
  // The first matching option is the active one, as in the classic interface.
  const active = $derived(inherit ? -1 : options.findIndex((o) => matches(o.patch, values ?? effective(app.overlay, camera))));
  const cls = (on) => "rounded-full px-3 py-1 text-[12px] " + (on ? "bg-[var(--glass-hi)] text-[var(--ink)]" : "text-[var(--muted)]");
</script>

<div {id} class="inline-flex max-w-full flex-wrap gap-0.5 rounded-[20px] bg-[var(--field)] p-[3px] ring-1 ring-[var(--ring)]">
  {#if camera}
    <button type="button" aria-pressed={inherit} class={cls(inherit)} onclick={() => edit((o) => clearKeys(o, keys, camera))}>{T("Default")}</button>
  {/if}
  {#each options as o, i (o.label)}
    <button type="button" aria-pressed={active === i} class={cls(active === i)} onclick={() => (pick ? pick(o.patch) : change(o.patch, camera))}>{o.label}</button>
  {/each}
  {#if !inherit && active < 0}
    <button type="button" disabled aria-pressed="true" class={cls(true)}>{T("Custom")}</button>
  {/if}
</div>
