<script>
  // The activity: what Frigate detected lately and what happened to each detection,
  // to understand why a notification didn't arrive.
  import { T, Tf, i18n } from "./lib/i18n.svelte.js";
  import { app } from "./lib/store.svelte.js";
  import { REASONS } from "./lib/fields.js";
  import { labelEmoji, labelText } from "./lib/labels.js";
  import { timeText, whenText } from "./lib/time.js";
  import { routeHash } from "./lib/route.js";
  import Card from "./lib/Card.svelte";
  import Pills from "./lib/Pills.svelte";

  const ctx = $derived({ tz: app.data.timezone, lang: i18n.lang, T });
  let { route = { hour: null } } = $props();
  let camera = $state("");
  let outcome = $state(""); // "", "sent" or "ignored"
  // An hour from the overview's chart is the window (hour, hour + 1 h], like the server's
  // count per bar, so a bar's number is what the filter then shows.
  const inHour = (e) => !route.hour || (Date.parse(e.at) > route.hour * 1000 && Date.parse(e.at) <= (route.hour + 3600) * 1000);
  const cameras = $derived([...new Set((app.history || []).map((e) => e.camera))]);
  const entries = $derived((app.history || []).filter((e) => inHour(e) && (!camera || e.camera === camera) && (!outcome || e.sent === (outcome === "sent"))));
  const sent = $derived(entries.filter((e) => e.sent).length);
  let all = $state(false);
  let broken = $state({}); // ids whose thumbnail failed to load: the emoji replaces it
  let brokenBig = $state({}); // same for the snapshot: the thumbnail replaces it
  let open = $state({}); // ids of the rows unfolded
  const shown = $derived(all ? entries : entries.slice(0, 8));
  const outcomes = $derived([["", T("All")], ["sent", T("Sent")], ["ignored", T("Ignored")]].map(([patch, label]) => ({ label, patch: { outcome: patch } })));
  const details = (e) => [e.camera, e.zones?.length ? "📍 " + e.zones.join(", ") : "", e.score ? Math.round(e.score * 100) + T("%") : "", whenText(e.at, ctx)]
    .filter(Boolean).join(" · ");
</script>

<section id="activity" class="flex max-w-3xl flex-col gap-4">
  <div class="flex flex-col gap-1">
    <h1 class="m-0 text-[26px] font-semibold tracking-tight">{T("Recent activity")}</h1>
    <p class="m-0 text-[var(--muted)]">{T("Frigate's latest detections and what happened to them — handy to understand why a notification didn't arrive.")}</p>
  </div>
  <div class="flex flex-wrap items-center gap-3">
    {#if cameras.length > 1}
      <select id="activity-camera" class="field" style="width:auto" aria-label={T("Cameras")} bind:value={camera}>
        <option value="">{T("All cameras")}</option>{#each cameras as c (c)}<option value={c}>{c}</option>{/each}
      </select>
    {/if}
    <Pills id="activity-outcome" options={outcomes} values={{ outcome }} pick={(p) => { outcome = p.outcome; }} />
  </div>
  {#if route.hour}
    <div id="activity-hour" class="flex flex-wrap items-center gap-3 text-[var(--muted)]">
      <span>{Tf("Between %s and %s", timeText(route.hour * 1000, ctx), timeText((route.hour + 3600) * 1000, ctx))}</span>
      <button type="button" class="btn-link" onclick={() => { location.hash = "#activity"; }}>{T("Show all")}</button>
    </div>
  {/if}
  <Card id="activity-list">
    <ul class="m-0 flex list-none flex-col gap-3 p-0">
      {#each shown as e (e.id + "|" + e.at)}
        {@const reason = REASONS(T)[e.reason] || { text: e.reason || T("ignored") }}
        {@const url = "api/history/" + encodeURIComponent(e.id)}
        <li data-id={e.id} class="flex flex-wrap items-center gap-3">
          <button type="button" class="flex min-w-0 flex-1 basis-40 items-center gap-3 text-left" aria-expanded={!!open[e.id]} aria-controls={"detail-" + e.id}
            onclick={() => { open[e.id] = !open[e.id]; }}>
            {#if e.has_thumb && !broken[e.id]}
              <img class="h-12 w-12 flex-none rounded-xl object-cover" loading="lazy" alt="" src={url + "/thumb"}
                onerror={() => { broken[e.id] = true; }} />
            {:else}
              <span class="thumb flex h-12 w-12 flex-none items-center justify-center rounded-xl bg-[var(--field)] text-xl">{labelEmoji(e.label, e.sound)}</span>
            {/if}
            <span class="min-w-0 flex-1">
              <span class="block">{labelText(e.label, e.sound, T)}{e.sub_label ? " · 🏷 " + e.sub_label : ""}</span>
              <span class="block text-[12px] text-[var(--muted)]">{details(e)}</span>
            </span>
          </button>
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
          {#if open[e.id]}
            <div id={"detail-" + e.id} class="grid w-full basis-full gap-2">
              {#if e.has_snapshot && !brokenBig[e.id]}
                <img class="big w-full rounded-xl" alt="" src={url + "/snapshot"} onerror={() => { brokenBig[e.id] = true; }} />
              {:else if e.has_thumb && !broken[e.id]}
                <img class="big w-full rounded-xl" alt="" src={url + "/thumb"} onerror={() => { broken[e.id] = true; }} />
              {:else}
                <span class="thumb flex h-24 items-center justify-center rounded-xl bg-[var(--field)] text-4xl">{labelEmoji(e.label, e.sound)}</span>
              {/if}
              {#if e.link}<a href={e.link} target="_blank" rel="noopener">{T("🔗 Open in Frigate")}</a>{/if}
            </div>
          {/if}
        </li>
      {:else}
        <li class="text-[var(--muted)]">{route.hour ? T("No detection in this hour.") : app.history?.length ? "—" : T("No detection since the service started.")}</li>
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
