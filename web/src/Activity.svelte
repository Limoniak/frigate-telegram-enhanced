<script>
  // The activity: what Frigate detected lately and what happened to each detection,
  // to understand why a notification didn't arrive.
  import { T, Tf, i18n } from "./lib/i18n.svelte.js";
  import { app } from "./lib/store.svelte.js";
  import { REASONS } from "./lib/fields.js";
  import { labelEmoji, labelText } from "./lib/labels.js";
  import { hourStart, timeText, whenText } from "./lib/time.js";
  import { inHour, tooOld } from "./lib/activity.js";
  import { routeHash } from "./lib/route.js";
  import Card from "./lib/Card.svelte";
  import Pills from "./lib/Pills.svelte";

  const ctx = $derived({ tz: app.data.timezone, lang: i18n.lang, T });
  let { route = { hour: null } } = $props();
  let camera = $state("");
  let outcome = $state(""); // "", "sent" or "ignored"
  // An hour from the overview's chart is the window (hour, hour + 1 h] of the server's
  // count per bar, which counts the notifications at their send time: arriving from a
  // bar lists the notifications sent, so that the list is what the bar counted (and
  // leaving the hour lists everything again).
  $effect.pre(() => { outcome = route.hour ? "sent" : ""; });
  const history = $derived(app.history || []);
  const cameras = $derived([...new Set(history.map((e) => e.camera))]);
  const inWindow = $derived(history.filter((e) => inHour(e, route.hour)));
  const entries = $derived(inWindow.filter((e) => (!camera || e.camera === camera) && (!outcome || e.sent === (outcome === "sent"))));
  // The chart's last 24 hours, the latest first, and the one the address names.
  const hours = $derived(app.activity ? app.activity.hours.map((_, i) => hourStart(app.activity.at, i)).reverse() : []);
  const hourOptions = $derived(route.hour && !hours.includes(route.hour) ? [route.hour, ...hours] : hours);
  const goHour = (v) => { location.hash = routeHash({ screen: "activity", hour: Number(v) || null }); };
  const sent = $derived(entries.filter((e) => e.sent).length);
  let all = $state(false);
  // The state of the rows is kept by id and start: the same event can come twice.
  const key = (e) => e.id + "|" + e.at;
  let broken = $state({}); // rows whose thumbnail failed to load: the emoji replaces it
  let brokenBig = $state({}); // same for the snapshot: the thumbnail replaces it
  let open = $state({}); // rows unfolded
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
    {#if hours.length}
      <select id="activity-hour-pick" class="field" style="width:auto" aria-label={T("Hour")} value={route.hour || ""} onchange={(e) => goHour(e.currentTarget.value)}>
        <option value="">{T("All hours")}</option>{#each hourOptions as h (h)}<option value={h}>{timeText(h * 1000, ctx)} – {timeText((h + 3600) * 1000, ctx)}</option>{/each}
      </select>
    {/if}
    {#if cameras.length > 1}
      <select id="activity-camera" class="field" style="width:auto" aria-label={T("Cameras")} bind:value={camera}>
        <option value="">{T("All cameras")}</option>{#each cameras as c (c)}<option value={c}>{c}</option>{/each}
      </select>
    {/if}
    <Pills id="activity-outcome" label={T("Outcome")} options={outcomes} values={{ outcome }} pick={(p) => { outcome = p.outcome; }} />
  </div>
  {#if route.hour}
    <div id="activity-hour" class="flex flex-wrap items-center gap-3 text-[var(--muted)]">
      <span>{Tf("Between %s and %s", timeText(route.hour * 1000, ctx), timeText((route.hour + 3600) * 1000, ctx))}</span>
      <button type="button" class="btn-link" onclick={() => { location.hash = "#activity"; }}>{T("Show all")}</button>
    </div>
  {/if}
  <Card id="activity-list">
    <ul class="m-0 flex list-none flex-col gap-3 p-0">
      {#each shown as e, i (key(e))}
        {@const k = key(e)}
        {@const reason = REASONS(T)[e.reason] || { text: e.reason || T("ignored") }}
        {@const url = "api/history/" + encodeURIComponent(e.id)}
        <li data-id={e.id} class="flex flex-wrap items-center gap-3">
          <button type="button" class="flex min-w-0 flex-1 basis-40 items-center gap-3 text-left" aria-expanded={!!open[k]} aria-controls={"detail-" + i}
            onclick={() => { open[k] = !open[k]; }}>
            {#if e.has_thumb && !broken[k]}
              <img class="h-12 w-12 flex-none rounded-xl object-cover" loading="lazy" alt="" src={url + "/thumb"}
                onerror={() => { broken[k] = true; }} />
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
          {#if open[k]}
            <div id={"detail-" + i} class="grid w-full basis-full gap-2">
              {#if e.has_snapshot && !brokenBig[k]}
                <img class="big w-full rounded-xl" alt="" src={url + "/snapshot"} onerror={() => { brokenBig[k] = true; }} />
              {:else if e.has_thumb && !broken[k]}
                <img class="big w-full rounded-xl" alt="" src={url + "/thumb"} onerror={() => { broken[k] = true; }} />
              {:else}
                <span class="thumb flex h-24 items-center justify-center rounded-xl bg-[var(--field)] text-4xl">{labelEmoji(e.label, e.sound)}</span>
              {/if}
              {#if e.link}<a href={e.link} target="_blank" rel="noopener">{T("🔗 Open in Frigate")}</a>{/if}
            </div>
          {/if}
        </li>
      {:else}
        <li class="text-[var(--muted)]">
          {#if !history.length}{T("No detection since the service started.")}
          {:else if inWindow.length}{T("No detection matches these filters.")}
          {:else if tooOld(history, route.hour)}{T("This hour is older than the last 50 detections.")}
          {:else}{T("No detection in this hour.")}{/if}
        </li>
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
