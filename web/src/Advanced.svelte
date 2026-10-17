<script>
  // Every setting, group by group, for what the choices of the other screens don't
  // cover; plus the two addresses and the way back to the config.yml settings.
  import { T } from "./lib/i18n.svelte.js";
  import { app, edit, resetSettings } from "./lib/store.svelte.js";
  import { GROUPS, fieldsFor, validURL } from "./lib/fields.js";
  import Card from "./lib/Card.svelte";
  import Field from "./lib/Field.svelte";

  const fields = $derived(fieldsFor(app.data, null, T));
  const groups = $derived(GROUPS(T).map((g) => ({ ...g, fields: fields.filter((f) => g.keys.includes(f.key)) })).filter((g) => g.links || g.fields.length));

  // Typed text of each address (for the test link), until saved; and the addresses
  // found invalid when their field was left (not while typing).
  let drafts = $state({});
  let errors = $state({});
  // A reload (reset) replaces the overlay: what was typed is forgotten with it.
  $effect.pre(() => { app.overlay; drafts = {}; errors = {}; });
  const draft = (key) => (drafts[key] ?? app.overlay[key] ?? "").trim();
  function commit(key, e) {
    const u = e.currentTarget.value.trim().replace(/\/+$/, "");
    drafts[key] = u;
    e.currentTarget.value = u;
    errors[key] = !validURL(u);
    if (validURL(u)) edit((o) => { o[key] = u; });
  }

  async function reset() {
    const question = app.data.can_connection
      ? T("Go back to the default settings? The choices made on this page will be lost.")
      : T("Delete the settings saved here and go back to the config.yml ones?");
    if (confirm(question)) await resetSettings();
  }
</script>

{#snippet url(id, key, name, hint, placeholder, label, expected, fallback)}
  {@const typed = draft(key)}
  {@const shown = typed || fallback}
  {@const bad = !!errors[key]}
  <div class="flex flex-col gap-2" data-key={key}>
    <span>{name}</span>
    <div class="flex items-center gap-3">
      <input {id} type="url" class="field min-w-0 flex-1" {placeholder} aria-label={label} aria-invalid={bad ? "true" : undefined} aria-describedby={bad ? id + "-hint " + id + "-error" : id + "-hint"}
        value={app.overlay[key] || ""} oninput={(e) => { drafts[key] = e.currentTarget.value; errors[key] = false; }} onchange={(e) => commit(key, e)} />
      {#if shown && validURL(shown)}<a class="flex-none" aria-label={name + " – " + T("Test ↗")} href={shown} target="_blank" rel="noopener">{T("Test ↗")}</a>{/if}
    </div>
    {#if bad}<p id="{id}-error" class="m-0 text-[12px] text-[var(--err)]" role="alert">{expected}</p>{/if}
    <p id="{id}-hint" class="m-0 text-[12px] text-[var(--faint)]">{hint}</p>
  </div>
{/snippet}

<section id="advanced" class="flex max-w-3xl flex-col gap-4">
  <div class="flex flex-col gap-1">
    <h1 class="m-0 text-[26px] font-semibold tracking-tight">{T("Advanced settings")}</h1>
    <p class="m-0 text-[var(--muted)]">{T("Every setting, for what the choices above don't cover. They apply to every camera, unless it has its own.")}</p>
  </div>
  {#key app.overlay}
  {#each groups as g (g.title)}
    <Card>
      <span class="lbl">{g.title}</span>
      {#each g.fields as f (f.key)}<Field field={f} />{/each}
      {#if g.links}
        {@render url("external-url", "external_url", T("“Open in Frigate” link"), T("Empty: the address the service reaches Frigate at. Set another one to open Frigate from outside."), app.data.frigate_url || "https://frigate.example.com", T("Frigate address for links"), T("Expected an address like https://frigate.example.com"), app.data.frigate_url || "")}
        {@render url("ha-url", "home_assistant_url", T("“Home Assistant” button"), T("Adds a button that opens Home Assistant under each notification. Empty: no button."), "https://homeassistant.example.com", T("Home Assistant address"), T("Expected an address like https://homeassistant.example.com"), "")}
      {/if}
    </Card>
  {/each}
  {/key}
  {#if app.data.custom}
    <div><button type="button" id="reset" class="btn" onclick={reset}>{app.data.can_connection ? T("Back to the default settings") : T("Back to the config.yml settings")}</button></div>
  {/if}
</section>
