<script>
  // The activity: what Frigate detected lately and what happened to each detection,
  // to understand why a notification didn't arrive.
  import { T, Tf, i18n } from "./lib/i18n.svelte.js";
  import { app } from "./lib/store.svelte.js";
  import { REASONS } from "./lib/fields.js";
  import { labelEmoji, labelText } from "./lib/labels.js";
  import { whenText } from "./lib/time.js";
  import { routeHash } from "./lib/route.js";
  import Card from "./lib/Card.svelte";

  const ctx = $derived({ tz: app.data.timezone, lang: i18n.lang, T });
  const entries = $derived(app.history || []);
  const sent = $derived(entries.filter((e) => e.sent).length);
  let all = $state(false);
  let broken = $state({}); // ids whose thumbnail failed to load: the emoji replaces it
  const shown = $derived(all ? entries : entries.slice(0, 8));
  const details = (e) => [e.camera, e.zones?.length ? "📍 " + e.zones.join(", ") : "", e.score ? Math.round(e.score * 100) + T("%") : "", whenText(e.at, ctx)]
    .filter(Boolean).join(" · ");
</script>

<section id="activity" class="flex max-w-3xl flex-col gap-4">
  <div class="flex flex-col gap-1">
    <span class="lbl">{T("Recent activity")}</span>
    <p class="m-0 text-[var(--muted)]">{T("Frigate's latest detections and what happened to them — handy to understand why a notification didn't arrive.")}</p>
  </div>
  <Card id="activity-list">
    <ul class="m-0 flex list-none flex-col gap-3 p-0">
      {#each shown as e (e.id)}
        {@const reason = REASONS(T)[e.reason] || { text: e.reason || T("ignored") }}
        <li data-id={e.id} class="flex flex-wrap items-center gap-3">
          {#if e.has_thumb && !broken[e.id]}
            <img class="h-12 w-12 flex-none rounded-xl object-cover" loading="lazy" alt="" src={"api/history/" + encodeURIComponent(e.id) + "/thumb"}
              onerror={() => { broken[e.id] = true; }} />
          {:else}
            <span class="thumb flex h-12 w-12 flex-none items-center justify-center rounded-xl bg-[var(--field)] text-xl">{labelEmoji(e.label, e.sound)}</span>
          {/if}
          <div class="min-w-0 flex-1 basis-40">
            <div>{labelText(e.label, e.sound, T)}{e.sub_label ? " · 🏷 " + e.sub_label : ""}</div>
            <div class="text-[12px] text-[var(--muted)]">{details(e)}</div>
          </div>
          <div class="max-w-full text-[12px]">
            {#if e.sent}
              <span class="text-[var(--accent-ink)]">{e.grouped ? T("📦 Grouped") : T("✅ Sent")}</span>
            {:else}
              <span class="text-[var(--warn)]">{"⛔ " + reason.text}</span>
              {#if reason.fix && app.overlay.cameras[e.camera]}
                <br><a href={routeHash({ screen: "cameras", camera: e.camera })}>{T("Adjust ") + e.camera}</a>
              {/if}
            {/if}
          </div>
        </li>
      {:else}
        <li class="text-[var(--muted)]">{T("No detection since the service started.")}</li>
      {/each}
    </ul>
    {#if entries.length}
      <div id="activity-foot" class="flex flex-wrap items-center gap-3 text-[12px] text-[var(--muted)]">
        <span>{Tf("Out of the last %d detections: %d sent, %d ignored.", entries.length, sent, entries.length - sent)}</span>
        {#if entries.length > 8}
          <button type="button" class="btn-link" onclick={() => { all = !all; }}>{all ? T("Show less") : T("Show all")}</button>
        {/if}
      </div>
    {/if}
  </Card>
</section>
