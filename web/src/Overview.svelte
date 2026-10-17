<script>
  // The overview: whether everything runs, the day's notifications, the cameras, and
  // the last notification as it arrived.
  import { T, Tf, i18n } from "./lib/i18n.svelte.js";
  import { app, change, pause, resume } from "./lib/store.svelte.js";
  import { effective } from "./lib/settings.js";
  import { previewModel } from "./lib/preview.js";
  import { hourStart, timeText, untilText, whenText } from "./lib/time.js";
  import { routeHash } from "./lib/route.js";
  import Card from "./lib/Card.svelte";
  import Phone from "./lib/Phone.svelte";
  import Switch from "./lib/Switch.svelte";

  const tz = $derived(app.data.timezone);
  const ctx = $derived({ tz, lang: i18n.lang, T });
  const healthy = $derived(app.health ? app.health.every((c) => c.state === "ok") : null);
  const problems = $derived((app.health || []).filter((c) => c.state === "error" || c.state === "warn"));
  const hours = $derived(app.activity?.hours ?? []);
  const peak = $derived(Math.max(1, ...hours));
  const last = $derived((app.history || []).find((e) => e.sent));
  const ignored = $derived((app.history || []).filter((e) => !e.sent).length);
  const mutes = $derived(Object.fromEntries((app.live?.mutes || []).map((m) => [m.camera, m.until])));
  const home = $derived((app.live?.home || []).map((t) => { const p = t.split("/"); return p.length >= 2 && p.at(-1) === "state" ? p.at(-2) : p.at(-1); }));
  const lastEntry = $derived(last && {
    label: last.label, sound: last.sound, camera: last.camera, time: whenText(last.at, ctx),
    details: [last.zones?.length ? "📍 " + last.zones.join(", ") : "", last.score ? Math.round(last.score * 100) + T("%") : ""].filter(Boolean).join(" · "),
  });
</script>

<section id="overview" class="grid grid-cols-1 gap-4 lg:grid-cols-12">
  <h1 class="sr-only">{T("Overview")}</h1>
  <div class="grid content-start gap-3 lg:col-span-7">
    <Card id="today">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <span class="lbl">{T("Sent in the last 24 h")}</span>
        {#if healthy !== null}
          <span class="flex items-center gap-1.5 rounded-full px-3 py-1 text-[11px] {healthy ? 'bg-[var(--accent)]/10 text-[var(--accent-ink)]' : 'bg-amber-400/10 text-[var(--warn)]'}">
            <span class="h-1.5 w-1.5 rounded-full {healthy ? 'bg-[var(--accent)]' : 'bg-amber-400'}"></span>{healthy ? T("Everything works") : T("Something needs attention")}
          </span>
        {/if}
      </div>
      {#if app.activity}
        <div class="flex flex-wrap items-end gap-4">
          <span id="sent-24h" class="text-6xl font-light tracking-tight tabular-nums">{app.activity.sent}</span>
          {#if app.history?.length}<a class="pb-2 text-[var(--muted)]" href="#activity">{Tf("%d ignored among the last %d detections", ignored, app.history.length)}</a>{/if}
        </div>
        <div class="flex h-16 items-end gap-[3px]">
          {#each hours as n, i (i)}
            {@const start = hourStart(app.activity.at, i)}
            <a class="flex h-full flex-1 items-end" href={routeHash({ screen: "activity", hour: start })}
              aria-label={Tf("%d sent between %s and %s", n, timeText(start * 1000, ctx), timeText((start + 3600) * 1000, ctx))}>
              <span class="block w-full rounded-[2px] {i === 23 ? 'bg-[var(--accent)]' : 'bg-[var(--accent)]/25'}" style="height:{Math.max(4, (n / peak) * 100)}%"></span>
            </a>
          {/each}
        </div>
        <div class="flex justify-between font-mono text-[11px] text-[var(--faint)]"><span>−24 h</span><span>{T("now")}</span></div>
      {/if}
    </Card>

    {#if app.live}
      <Card id="pause-bar">
        {#if app.live.paused}
          <div class="flex flex-wrap items-center gap-3">
            <span class="flex-1">{T("Notifications paused ") + untilText(app.live.paused_until, ctx)}</span>
            <button type="button" class="btn btn-primary" onclick={() => resume()}>{T("▶ Resume")}</button>
          </div>
        {:else}
          <div class="flex flex-wrap items-center gap-2">
            <span class="flex-1">{T("Notifications active")}</span>
            <span class="text-[var(--muted)]">{T("Pause:")}</span>
            {#each [["30 min", 30], ["1 h", 60], ["8 h", 480], [T("until resumed"), 0]] as [label, minutes] (minutes)}
              <button type="button" class="btn" onclick={() => pause(minutes)}>{label}</button>
            {/each}
          </div>
        {/if}
        {#if app.data.presence}<p class="m-0 text-[var(--muted)]">{home.length ? T("🏠 At home: ") + home.join(", ") : T("🚪 Nobody at home")}</p>{/if}
      </Card>
    {/if}

    <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
      {#if app.health}
        <Card id="connections">
          <span class="lbl">{T("Connections")}</span>
          {#each app.health as c (c.name)}
            <div class="flex items-center gap-3"><span class="flex-1">{c.name}</span><span class="truncate font-mono text-[11px] text-[var(--faint)]">{c.state === "ok" ? c.detail : ""}</span>
              <span class="h-1.5 w-1.5 flex-none rounded-full {c.state === 'ok' ? 'bg-[var(--accent)]' : c.state === 'warn' ? 'bg-amber-400' : 'bg-red-400'}"></span></div>
          {/each}
          {#each problems as c (c.name)}<p class="result err m-0"><b>{c.name} —</b> {c.detail}{#if c.hint}<br>👉 {c.hint}{/if}</p>{/each}
          {#if app.refused.length}
            <p class="result warn m-0"><b>{T("Telegram — users refused by the bot")}</b>
              {#each app.refused as u (u.id)}<br>{[u.name, u.username && "@" + u.username].filter(Boolean).join(" ") || T("unknown")} · {T("ID ")}<code>{u.id}</code>{/each}
              <br>👉 {app.data.can_connection ? T("To allow someone, add their ID as a recipient with the Connection link at the bottom of the page.") : T("To allow someone, add their ID to telegram.chats in config.yml (and to telegram.admins if you set it), then restart the container.")}</p>
          {/if}
        </Card>
      {/if}
      <Card id="cameras-list">
        <span class="lbl">{T("Cameras")}</span>
        {#each app.data.cameras || [] as c (c.name)}
          {@const on = effective(app.overlay, c.name).enabled !== false}
          <div class="flex items-center gap-3">
            <a class="min-w-0 flex-1 truncate" href={routeHash({ screen: "cameras", camera: c.name })}>{c.name}</a>
            {#if mutes[c.name] !== undefined}
              <button type="button" class="btn-link text-[12px]" onclick={() => resume(c.name)}>🔇 {untilText(mutes[c.name], ctx)} · {T("Re-enable")}</button>
            {/if}
            <Switch checked={on} label={c.name} onchange={(v) => change({ enabled: v }, c.name)} />
          </div>
        {:else}
          <p class="m-0 text-[var(--muted)]">{T("No known camera.")}</p>
        {/each}
      </Card>
    </div>
  </div>

  {#if app.history}
    <div id="last-notification" class="flex flex-col items-center gap-3 lg:col-span-5">
      <span class="lbl">{T("Last notification")}{lastEntry ? " · " + lastEntry.time : ""}</span>
      {#if lastEntry}
        <Phone model={previewModel(effective(app.overlay, last.camera), { homeAssistant: !!app.overlay.home_assistant_url })} entry={lastEntry}
          image={last.has_thumb ? "api/history/" + encodeURIComponent(last.id) + "/thumb" : ""} />
      {:else}
        <p class="m-0 text-[var(--muted)]">{T("No notification sent yet.")}</p>
      {/if}
    </div>
  {/if}
</section>
