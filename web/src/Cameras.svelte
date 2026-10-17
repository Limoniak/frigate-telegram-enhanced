<script>
  // One card per camera: its switch, the few choices that matter (style, objects,
  // zones), and every setting folded below, each of which the camera can override.
  import { tick, untrack } from "svelte";
  import { T, Tf, i18n } from "./lib/i18n.svelte.js";
  import { app, change, edit, sendTest } from "./lib/store.svelte.js";
  import { GROUPS, cameraSummary, fieldsFor, unionOf, untracked } from "./lib/fields.js";
  import { labelText } from "./lib/labels.js";
  import { QUESTIONS } from "./lib/questions.js";
  import { STYLES, effective, same } from "./lib/settings.js";
  import Field from "./lib/Field.svelte";
  import Pills from "./lib/Pills.svelte";
  import Switch from "./lib/Switch.svelte";

  let { route } = $props();

  const names = $derived(
    [...new Set([...(app.data.cameras || []).map((c) => c.name), ...Object.keys(app.overlay.cameras)])].sort());
  const ctx = $derived({ tz: app.data.timezone, lang: i18n.lang, T });
  const styles = $derived(STYLES.map((s) => ({ patch: s.patch, label: {
    clip: T("Photo + video"), photo: T("Photo only"), gif: T("Photo + GIF"), text: T("Text only"),
  }[s.id] })));
  const objects = $derived(QUESTIONS(T).find((q) => q.id === "objects"));
  const home = $derived(app.data.presence ? QUESTIONS(T).find((q) => q.presence) : null);
  const groups = (name) => {
    const fields = fieldsFor(app.data, name, T);
    return GROUPS(T).filter((g) => !g.globalOnly)
      .map((g) => ({ ...g, fields: fields.filter((f) => g.keys.includes(f.key)) })).filter((g) => g.fields.length);
  };
  const cls = (on) => "rounded-full px-3 py-1 text-[12px] " + (on ? "bg-[var(--glass-hi)] text-[var(--ink)]" : "text-[var(--muted)]");

  // The card of the route opens and scrolls into view, now and when the hash changes.
  let cards = $state({});
  $effect(() => {
    const name = route.camera;
    if (!name) return;
    const card = untrack(() => cards[name]); // the camera list changing must not reopen a closed card
    if (!card) return;
    card.open = true;
    tick().then(() => {
      card.scrollIntoView({ block: "start" });
      card.querySelector("summary").focus({ preventScroll: true }); // else the focus falls to the page
    });
  });

  function setZones(name, next) {
    edit((o) => {
      const cam = (o.cameras[name] ??= {});
      if (same("zones", next, o.notify.zones)) delete cam.zones;
      else cam.zones = next;
    });
  }

  let results = $state({}); // per camera: the state of the last test
  async function test(name) {
    results[name] = { state: "", text: T("Sending…"), busy: true };
    try {
      await sendTest(name);
      results[name] = { state: "ok", text: T("Sent ✓ — check Telegram"), busy: true };
    } catch (e) {
      results[name] = { state: "err", text: T("Failed: ") + e.message, busy: true };
    } finally {
      setTimeout(() => { results[name] = { ...results[name], busy: false }; }, 3000);
    }
  }
</script>

{#snippet question(title, hint, body)}
  <div class="flex flex-col gap-2"><div>{title}</div>{#if hint}<div class="text-[12px] text-[var(--faint)]">{hint}</div>{/if}{@render body()}</div>
{/snippet}

<section id="cameras" class="flex max-w-3xl flex-col gap-4">
  <div class="flex flex-col gap-1">
    <h1 class="m-0 text-[26px] font-semibold tracking-tight">{T("Cameras")}</h1>
  </div>
  {#each names as name (name)}
    {@const eff = effective(app.overlay, name)}
    {@const on = eff.enabled !== false}
    {@const missing = untracked(app.data, app.overlay, name)}
    {@const zones = unionOf(app.data, "zones", name)}
    {@const current = eff.zones || []}
    {@const result = results[name]}
    <div class="relative rounded-[22px] bg-[var(--shell)] p-[5px] ring-1 ring-[var(--ring)] {on ? '' : 'opacity-70'}">
      <!-- The switch sits over the summary's left padding, not inside it: no interactive content in a summary. -->
      <div class="absolute left-[21px] top-[23px] z-[1]">
        <Switch checked={on} label={T("Notifications for ") + name} onchange={(v) => change({ enabled: v }, name)} />
      </div>
      <details data-camera={name} bind:this={cards[name]}>
        <summary class="flex cursor-pointer list-none flex-wrap items-center gap-x-3 gap-y-1 rounded-[17px] bg-[var(--glass)] py-4 pl-[68px] pr-4 shadow-[inset_0_1px_1px_var(--glass-hi)]">
          <span class="min-w-0 flex-none font-medium">{name}</span>
          <span class="min-w-0 flex-1 basis-40 text-[12px] text-[var(--muted)]">{cameraSummary(app.data, app.overlay, app.live, name, ctx)}</span>
        </summary>
        <div class="mt-[5px] flex flex-col gap-5 rounded-[17px] bg-[var(--glass)] p-5 shadow-[inset_0_1px_1px_var(--glass-hi)]">
          {#snippet style()}<Pills label={T("Notification style")} options={styles} camera={name} />{/snippet}
          {@render question(T("Notification style"), null, style)}
          {#snippet what()}
            <Pills label={objects.title} options={objects.options} camera={name} />
            {#if missing.length}
              <p class="m-0 text-[12px] text-[var(--warn)]">{Tf("⚠️ Frigate doesn't track %s on this camera: these objects will never be reported. Add them to objects.track in Frigate's configuration.", missing.map((l) => Tf("“%s”", labelText(l, false, T))).join(", "))}</p>
            {/if}
          {/snippet}
          {@render question(T("What should be reported?"), null, what)}
          {#if home}
            {#snippet presence()}<Pills label={home.title} options={home.options} camera={name} />{/snippet}
            {@render question(home.title, T("E.g. keep outdoor cameras on while you're home."), presence)}
          {/if}
          {#if zones.length}
            {#snippet where()}
              <div role="group" aria-label={T("Where?")} class="inline-flex max-w-full flex-wrap gap-0.5 rounded-[20px] bg-[var(--field)] p-[3px] ring-1 ring-[var(--ring)]">
                <button type="button" aria-pressed={!current.length} class={cls(!current.length)} onclick={() => setZones(name, [])}>{T("Everywhere")}</button>
                {#each zones as z (z)}
                  <button type="button" aria-pressed={current.includes(z)} class={cls(current.includes(z))}
                    onclick={() => setZones(name, current.includes(z) ? current.filter((x) => x !== z) : [...current, z])}>{"📍 " + z}</button>
                {/each}
                {#each current.filter((z) => !zones.includes(z)) as z (z)}
                  <button type="button" aria-pressed="true" class={cls(true)} title={T("Zone missing from Frigate: click to remove it")}
                    onclick={() => setZones(name, current.filter((x) => x !== z))}>{"📍 " + z + T(" (unknown)")}</button>
                {/each}
              </div>
            {/snippet}
            {@render question(T("Where?"), T("Only when the object enters one of the chosen zones (defined in Frigate)."), where)}
          {/if}
          {#if app.data.can_test && on}
            <div class="flex flex-wrap items-center gap-3">
              <button type="button" class="btn" disabled={result?.busy} onclick={() => test(name)}>{T("📨 Test this camera")}</button>
              {#if result}<span class="result {result.state}" role="status">{result.text}</span>{/if}
            </div>
          {/if}
          <details class="more">
            <summary>{T("All settings for ") + name}</summary>
            <div class="mt-4 flex flex-col gap-5">
              {#each groups(name) as g (g.title)}
                <div class="flex flex-col gap-4">
                  <span class="lbl">{g.title}</span>
                  {#each g.fields as f (f.key)}<Field field={f} camera={name} />{/each}
                </div>
              {/each}
              <div><button type="button" class="btn-link" onclick={() => edit((o) => { o.cameras[name] = {}; })}>{T("Reset everything to default")}</button></div>
            </div>
          </details>
        </div>
      </details>
    </div>
  {:else}
    <p class="m-0 text-[var(--muted)]">{T("No known camera.")}</p>
  {/each}
</section>
